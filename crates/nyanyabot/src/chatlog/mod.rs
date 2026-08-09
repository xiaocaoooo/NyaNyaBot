use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};

use anyhow::{Context, Result};
use chrono::Utc;
use parking_lot::RwLock as SyncRwLock;
use serde_json::{Value, json};
use sqlx::PgPool;
use sqlx::postgres::PgPoolOptions;
use tokio::sync::{RwLock, mpsc};
use tracing::{error, info, warn};

use crate::config::ChatLogConfig;
use crate::onebot::ob11::ApiResponse;

/// Resolve group name via OneBot when missing from the event.
pub type FetchGroupNameFn = Arc<
    dyn Fn(
            i64,
            i64,
        ) -> std::pin::Pin<
            Box<dyn std::future::Future<Output = Result<String, anyhow::Error>> + Send>,
        > + Send
        + Sync,
>;

#[derive(Clone, Debug)]
pub struct GroupMessage {
    pub group_id: i64,
    pub real_seq: String,
    pub group_name: String,
    pub user_id: i64,
    pub user_display_name: String,
    pub raw_message: String,
    pub message_segments: Value,
    pub recorded_at: chrono::DateTime<Utc>,
    pub self_id: i64,
}

struct CacheEntry {
    name: String,
    expires_at: Instant,
}

/// group_id -> group_name TTL cache (Go GroupCache parity).
pub struct GroupCache {
    entries: SyncRwLock<HashMap<i64, CacheEntry>>,
    ttl: Duration,
}

impl GroupCache {
    pub fn new(ttl: Duration) -> Self {
        Self {
            entries: SyncRwLock::new(HashMap::new()),
            ttl,
        }
    }

    pub fn get(&self, group_id: i64) -> Option<String> {
        let guard = self.entries.read();
        let entry = guard.get(&group_id)?;
        if Instant::now() > entry.expires_at {
            return None;
        }
        Some(entry.name.clone())
    }

    pub fn set(&self, group_id: i64, name: &str) {
        if name.trim().is_empty() {
            return;
        }
        self.entries.write().insert(
            group_id,
            CacheEntry {
                name: name.to_string(),
                expires_at: Instant::now() + self.ttl,
            },
        );
    }

    pub fn clear(&self) {
        self.entries.write().clear();
    }

    pub fn clean_expired(&self) {
        let now = Instant::now();
        self.entries.write().retain(|_, e| now <= e.expires_at);
    }
}

pub struct Recorder {
    database_uri: RwLock<String>,
    queue_size: i64,
    tx: RwLock<Option<mpsc::Sender<GroupMessage>>>,
    worker: RwLock<Option<tokio::task::JoinHandle<()>>>,
    pool: RwLock<Option<PgPool>>,
    cache: Arc<GroupCache>,
    fetch_group_name: RwLock<Option<FetchGroupNameFn>>,
}

impl Recorder {
    pub fn new(cfg: &ChatLogConfig) -> Arc<Self> {
        Arc::new(Self {
            database_uri: RwLock::new(cfg.database_uri.clone()),
            queue_size: if cfg.queue.size > 0 {
                cfg.queue.size
            } else {
                1000
            },
            tx: RwLock::new(None),
            worker: RwLock::new(None),
            pool: RwLock::new(None),
            // Go default was typically hours; use 1h TTL.
            cache: Arc::new(GroupCache::new(Duration::from_secs(3600))),
            fetch_group_name: RwLock::new(None),
        })
    }

    pub async fn set_group_name_fetcher(&self, f: FetchGroupNameFn) {
        *self.fetch_group_name.write().await = Some(f);
    }

    pub async fn start(self: &Arc<Self>) {
        let uri = self.database_uri.read().await.clone();
        if uri.trim().is_empty() {
            info!("chatlog disabled (empty database_uri)");
            return;
        }
        match self.connect(&uri).await {
            Ok(pool) => {
                if let Err(err) = ensure_schema(&pool).await {
                    error!(error = %err, "chatlog schema init failed");
                    return;
                }
                *self.pool.write().await = Some(pool.clone());
                let (tx, mut rx) = mpsc::channel::<GroupMessage>(self.queue_size as usize);
                *self.tx.write().await = Some(tx);
                let this = Arc::clone(self);
                let handle = tokio::spawn(async move {
                    let mut n = 0u64;
                    while let Some(mut ev) = rx.recv().await {
                        if ev.group_name.trim().is_empty() {
                            if let Some(cached) = this.cache.get(ev.group_id) {
                                ev.group_name = cached;
                            } else if let Some(fetch) = this.fetch_group_name.read().await.clone() {
                                match fetch(ev.group_id, ev.self_id).await {
                                    Ok(name) if !name.trim().is_empty() => {
                                        this.cache.set(ev.group_id, &name);
                                        ev.group_name = name;
                                    }
                                    Ok(_) => {}
                                    Err(err) => {
                                        warn!(
                                            group_id = ev.group_id,
                                            error = %err,
                                            "chatlog fetch group_name failed"
                                        );
                                    }
                                }
                            }
                        } else {
                            this.cache.set(ev.group_id, &ev.group_name);
                        }
                        if let Err(err) = insert_event(&pool, &ev).await {
                            warn!(error = %err, "chatlog insert failed");
                        }
                        n += 1;
                        if n.is_multiple_of(256) {
                            this.cache.clean_expired();
                        }
                    }
                });
                *self.worker.write().await = Some(handle);
                info!("chatlog recorder started");
            }
            Err(err) => error!(error = %err, "chatlog connect failed"),
        }
    }

    pub async fn stop(&self) -> Result<()> {
        *self.tx.write().await = None;
        if let Some(handle) = self.worker.write().await.take() {
            let _ = handle.await;
        }
        *self.pool.write().await = None;
        Ok(())
    }

    pub async fn reconnect(self: &Arc<Self>, database_uri: String) -> Result<()> {
        self.stop().await?;
        *self.database_uri.write().await = database_uri;
        self.start().await;
        Ok(())
    }

    pub fn handle_event(&self, event: &Value) {
        let Some(msg) = parse_group_message(event) else {
            return;
        };
        let tx_opt = self.tx.try_read().ok().and_then(|g| g.clone());
        if let Some(tx) = tx_opt {
            let _ = tx.try_send(msg);
        }
    }

    async fn connect(&self, uri: &str) -> Result<PgPool> {
        PgPoolOptions::new()
            .max_connections(5)
            .connect(uri)
            .await
            .context("connect postgres for chatlog")
    }
}

/// Parse OneBot group message into a chat log row. Non-group / invalid real_seq => None.
pub fn parse_group_message(event: &Value) -> Option<GroupMessage> {
    if event.get("post_type").and_then(|v| v.as_str()) != Some("message") {
        return None;
    }
    if event.get("message_type").and_then(|v| v.as_str()) != Some("group") {
        return None;
    }

    let real_seq = parse_real_seq(event.get("real_seq"))?;
    let group_id = event.get("group_id").and_then(as_i64)?;
    let user_id = event.get("user_id").and_then(as_i64)?;
    let self_id = event.get("self_id").and_then(as_i64).unwrap_or(0);
    let raw_message = event
        .get("raw_message")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();

    let mut group_name = String::new();
    if let Some(name) = event.get("group_name").and_then(|v| v.as_str()) {
        group_name = name.to_string();
    }

    let mut user_display_name = String::new();
    if let Some(sender) = event.get("sender").and_then(|v| v.as_object()) {
        if let Some(card) = sender.get("card").and_then(|v| v.as_str())
            && !card.is_empty()
        {
            user_display_name = card.to_string();
        } else if let Some(nickname) = sender.get("nickname").and_then(|v| v.as_str()) {
            user_display_name = nickname.to_string();
        }
    }

    let message_segments = event.get("message").cloned().unwrap_or_else(|| json!([]));
    if !message_segments.is_array() {
        return Some(GroupMessage {
            group_id,
            real_seq,
            group_name,
            user_id,
            user_display_name,
            raw_message,
            message_segments: json!([]),
            recorded_at: Utc::now(),
            self_id,
        });
    }

    Some(GroupMessage {
        group_id,
        real_seq,
        group_name,
        user_id,
        user_display_name,
        raw_message,
        message_segments,
        recorded_at: Utc::now(),
        self_id,
    })
}

fn parse_real_seq(v: Option<&Value>) -> Option<String> {
    let v = v?;
    if let Some(s) = v.as_str() {
        let s = s.trim();
        if s.is_empty() || s == "0" {
            return None;
        }
        return Some(s.to_string());
    }
    if let Some(n) = v.as_i64() {
        if n <= 0 {
            return None;
        }
        return Some(n.to_string());
    }
    if let Some(n) = v.as_u64() {
        if n == 0 {
            return None;
        }
        return Some(n.to_string());
    }
    None
}

fn as_i64(v: &Value) -> Option<i64> {
    if let Some(n) = v.as_i64() {
        return Some(n);
    }
    if let Some(n) = v.as_u64() {
        return i64::try_from(n).ok();
    }
    if let Some(s) = v.as_str() {
        return s.trim().parse().ok();
    }
    None
}

async fn ensure_schema(pool: &PgPool) -> Result<()> {
    sqlx::query(
        r#"
        CREATE TABLE IF NOT EXISTS group_message_logs (
            id BIGSERIAL PRIMARY KEY,
            group_id BIGINT NOT NULL,
            real_seq TEXT NOT NULL,
            group_name TEXT NOT NULL DEFAULT '',
            user_id BIGINT NOT NULL,
            user_display_name TEXT NOT NULL DEFAULT '',
            raw_message TEXT NOT NULL DEFAULT '',
            message_segments JSONB NOT NULL DEFAULT '[]'::jsonb,
            recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            self_id BIGINT NOT NULL DEFAULT 0,
            UNIQUE (group_id, real_seq)
        );
        "#,
    )
    .execute(pool)
    .await?;
    sqlx::query(
        r#"
        CREATE INDEX IF NOT EXISTS idx_group_message_logs_group_time
            ON group_message_logs (group_id, recorded_at DESC);
        "#,
    )
    .execute(pool)
    .await?;
    // Compatibility: older DBs may miss self_id.
    let _ = sqlx::query(
        r#"ALTER TABLE group_message_logs ADD COLUMN IF NOT EXISTS self_id BIGINT NOT NULL DEFAULT 0"#,
    )
    .execute(pool)
    .await;
    Ok(())
}

async fn insert_event(pool: &PgPool, ev: &GroupMessage) -> Result<()> {
    sqlx::query(
        r#"
        INSERT INTO group_message_logs
            (group_id, real_seq, group_name, user_id, user_display_name, raw_message,
             message_segments, recorded_at, self_id)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
        ON CONFLICT (group_id, real_seq) DO NOTHING
        "#,
    )
    .bind(ev.group_id)
    .bind(&ev.real_seq)
    .bind(&ev.group_name)
    .bind(ev.user_id)
    .bind(&ev.user_display_name)
    .bind(&ev.raw_message)
    .bind(&ev.message_segments)
    .bind(ev.recorded_at)
    .bind(ev.self_id)
    .execute(pool)
    .await?;
    Ok(())
}

/// Helper used by app wiring: parse get_group_info API response.
pub fn group_name_from_api(resp: &ApiResponse) -> String {
    resp.data
        .get("group_name")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string()
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn parses_group_message_with_real_seq() {
        let msg = parse_group_message(&json!({
            "post_type":"message",
            "message_type":"group",
            "group_id": 10,
            "user_id": 20,
            "self_id": 1,
            "real_seq": "99",
            "raw_message": "hi",
            "message": [{"type":"text","data":{"text":"hi"}}],
            "sender": {"card":"Card","nickname":"Nick"}
        }))
        .unwrap();
        assert_eq!(msg.group_id, 10);
        assert_eq!(msg.real_seq, "99");
        assert_eq!(msg.user_display_name, "Card");
    }

    #[test]
    fn skips_private_and_missing_seq() {
        assert!(
            parse_group_message(&json!({
                "post_type":"message","message_type":"private","user_id":1,"real_seq":"1"
            }))
            .is_none()
        );
        assert!(
            parse_group_message(&json!({
                "post_type":"message","message_type":"group","group_id":1,"user_id":2
            }))
            .is_none()
        );
        assert!(
            parse_group_message(&json!({
                "post_type":"message","message_type":"group","group_id":1,"user_id":2,"real_seq":"0"
            }))
            .is_none()
        );
    }

    #[test]
    fn group_cache_ttl_and_set() {
        let cache = GroupCache::new(Duration::from_secs(60));
        assert!(cache.get(1).is_none());
        cache.set(1, "G1");
        assert_eq!(cache.get(1).as_deref(), Some("G1"));
        cache.clear();
        assert!(cache.get(1).is_none());
    }
}
