use std::sync::Arc;

use anyhow::{Context, Result};
use chrono::Utc;
use serde_json::{Value, json};
use sqlx::PgPool;
use sqlx::postgres::PgPoolOptions;
use tokio::sync::{RwLock, mpsc};
use tracing::{error, info, warn};

use crate::config::ChatLogConfig;

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

pub struct Recorder {
    database_uri: RwLock<String>,
    queue_size: i64,
    tx: RwLock<Option<mpsc::Sender<GroupMessage>>>,
    worker: RwLock<Option<tokio::task::JoinHandle<()>>>,
    pool: RwLock<Option<PgPool>>,
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
        })
    }

    pub async fn start(&self) {
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
                let handle = tokio::spawn(async move {
                    while let Some(ev) = rx.recv().await {
                        if let Err(err) = insert_event(&pool, &ev).await {
                            warn!(error = %err, "chatlog insert failed");
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

    pub async fn reconnect(&self, database_uri: String) -> Result<()> {
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

fn parse_real_seq(raw: Option<&Value>) -> Option<String> {
    let raw = raw?;
    let s = match raw {
        Value::String(s) => s.trim().to_string(),
        Value::Number(n) => n.to_string(),
        Value::Bool(b) => b.to_string(),
        _ => return None,
    };
    if s.is_empty() || s == "0" {
        return None;
    }
    Some(s)
}

fn as_i64(v: &Value) -> Option<i64> {
    v.as_i64()
        .or_else(|| v.as_u64().map(|n| n as i64))
        .or_else(|| v.as_f64().map(|n| n as i64))
        .or_else(|| v.as_str().and_then(|s| s.parse().ok()))
}

async fn ensure_schema(pool: &PgPool) -> Result<()> {
    for stmt in [
        r#"
        CREATE TABLE IF NOT EXISTS group_message_logs (
            group_id BIGINT NOT NULL,
            real_seq TEXT NOT NULL,
            group_name TEXT NOT NULL DEFAULT '',
            user_id BIGINT NOT NULL,
            user_display_name TEXT NOT NULL DEFAULT '',
            raw_message TEXT NOT NULL DEFAULT '',
            message_segments JSONB NOT NULL DEFAULT '[]'::jsonb,
            recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            self_id BIGINT NOT NULL DEFAULT 0,
            CONSTRAINT group_message_logs_group_real_seq_key UNIQUE (group_id, real_seq)
        )
        "#,
        r#"ALTER TABLE group_message_logs ADD COLUMN IF NOT EXISTS self_id BIGINT NOT NULL DEFAULT 0"#,
        r#"CREATE INDEX IF NOT EXISTS idx_group_message_logs_group_id ON group_message_logs (group_id)"#,
        r#"CREATE INDEX IF NOT EXISTS idx_group_message_logs_recorded_at ON group_message_logs (recorded_at)"#,
        r#"CREATE INDEX IF NOT EXISTS idx_group_message_logs_user_id ON group_message_logs (user_id)"#,
        r#"CREATE INDEX IF NOT EXISTS idx_group_message_logs_self_id ON group_message_logs (self_id)"#,
    ] {
        sqlx::query(stmt)
            .execute(pool)
            .await
            .with_context(|| format!("chatlog schema stmt failed: {stmt}"))?;
    }
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_group_message_with_real_seq() {
        let event = json!({
            "post_type": "message",
            "message_type": "group",
            "group_id": 100,
            "user_id": 200,
            "self_id": 1,
            "real_seq": "42",
            "raw_message": "hi",
            "sender": {"card": "Card", "nickname": "Nick"},
            "message": [{"type":"text","data":{"text":"hi"}}]
        });
        let msg = parse_group_message(&event).expect("parsed");
        assert_eq!(msg.group_id, 100);
        assert_eq!(msg.real_seq, "42");
        assert_eq!(msg.user_display_name, "Card");
        assert!(msg.message_segments.is_array());
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
}
