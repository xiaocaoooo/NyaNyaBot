use std::sync::Arc;

use anyhow::{Context, Result};
use serde_json::Value;
use sqlx::PgPool;
use sqlx::postgres::PgPoolOptions;
use tokio::sync::{RwLock, mpsc};
use tracing::{error, info, warn};

use crate::config::ChatLogConfig;

#[derive(Clone)]
struct ChatEvent {
    self_id: i64,
    user_id: i64,
    group_id: i64,
    message_type: String,
    message_id: i64,
    raw_message: String,
    raw: Value,
}

pub struct Recorder {
    database_uri: RwLock<String>,
    queue_size: i64,
    tx: RwLock<Option<mpsc::Sender<ChatEvent>>>,
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
                let (tx, mut rx) = mpsc::channel::<ChatEvent>(self.queue_size as usize);
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
        if event.get("post_type").and_then(|v| v.as_str()) != Some("message") {
            return;
        }
        let ev = ChatEvent {
            self_id: event.get("self_id").and_then(|v| v.as_i64()).unwrap_or(0),
            user_id: event.get("user_id").and_then(|v| v.as_i64()).unwrap_or(0),
            group_id: event.get("group_id").and_then(|v| v.as_i64()).unwrap_or(0),
            message_type: event
                .get("message_type")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string(),
            message_id: event
                .get("message_id")
                .and_then(|v| v.as_i64())
                .unwrap_or(0),
            raw_message: event
                .get("raw_message")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string(),
            raw: event.clone(),
        };
        // best-effort non-blocking enqueue
        let tx_opt = self.tx.try_read().ok().and_then(|g| g.clone());
        if let Some(tx) = tx_opt {
            let _ = tx.try_send(ev);
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

async fn ensure_schema(pool: &PgPool) -> Result<()> {
    sqlx::query(
        r#"
        CREATE TABLE IF NOT EXISTS chat_messages (
            id BIGSERIAL PRIMARY KEY,
            self_id BIGINT NOT NULL DEFAULT 0,
            user_id BIGINT NOT NULL DEFAULT 0,
            group_id BIGINT NOT NULL DEFAULT 0,
            message_type TEXT NOT NULL DEFAULT '',
            message_id BIGINT NOT NULL DEFAULT 0,
            raw_message TEXT NOT NULL DEFAULT '',
            raw JSONB NOT NULL DEFAULT '{}'::jsonb,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )
        "#,
    )
    .execute(pool)
    .await?;
    Ok(())
}

async fn insert_event(pool: &PgPool, ev: &ChatEvent) -> Result<()> {
    sqlx::query(
        r#"
        INSERT INTO chat_messages
            (self_id, user_id, group_id, message_type, message_id, raw_message, raw)
        VALUES ($1,$2,$3,$4,$5,$6,$7)
        "#,
    )
    .bind(ev.self_id)
    .bind(ev.user_id)
    .bind(ev.group_id)
    .bind(&ev.message_type)
    .bind(ev.message_id)
    .bind(&ev.raw_message)
    .bind(&ev.raw)
    .execute(pool)
    .await?;
    Ok(())
}
