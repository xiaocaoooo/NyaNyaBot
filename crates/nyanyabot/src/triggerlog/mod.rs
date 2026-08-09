use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicI64, Ordering};

use anyhow::{Context, Result};
use serde::Serialize;
use serde_json::Value;
use sqlx::PgPool;
use sqlx::postgres::PgPoolOptions;
use tokio::sync::{RwLock, mpsc};
use tracing::{error, info, warn};

use crate::config::TriggerLogConfig;

#[derive(Debug, Clone, Serialize)]
pub struct TriggerLogStats {
    pub total: i64,
}

#[derive(Clone)]
pub struct TriggerRecord {
    pub trace_id: String,
    pub plugin_id: String,
    pub listener_id: String,
    pub trace_type: String,
    pub self_id: i64,
    pub user_id: i64,
    pub group_id: i64,
    pub success: bool,
    pub error: String,
    pub duration_ms: i64,
    pub data: Value,
}

pub struct Recorder {
    enabled: AtomicBool,
    database_uri: RwLock<String>,
    queue_size: i64,
    batch_size: i64,
    batch_interval: String,
    tx: RwLock<Option<mpsc::Sender<TriggerRecord>>>,
    worker: RwLock<Option<tokio::task::JoinHandle<()>>>,
    pool: RwLock<Option<PgPool>>,
    total: AtomicI64,
}

impl Recorder {
    pub fn new(cfg: &TriggerLogConfig) -> Arc<Self> {
        Arc::new(Self {
            enabled: AtomicBool::new(cfg.enabled),
            database_uri: RwLock::new(cfg.database_uri.clone()),
            queue_size: if cfg.queue_size > 0 {
                cfg.queue_size
            } else {
                1000
            },
            batch_size: if cfg.batch_size > 0 {
                cfg.batch_size
            } else {
                100
            },
            batch_interval: if cfg.batch_interval.trim().is_empty() {
                "5s".into()
            } else {
                cfg.batch_interval.clone()
            },
            tx: RwLock::new(None),
            worker: RwLock::new(None),
            pool: RwLock::new(None),
            total: AtomicI64::new(0),
        })
    }

    pub fn start(&self) {
        self.enabled.store(true, Ordering::Relaxed);
        info!("triggerlog marked enabled");
    }

    pub async fn start_async(self: &Arc<Self>) {
        self.enabled.store(true, Ordering::Relaxed);
        let uri = self.database_uri.read().await.clone();
        if uri.trim().is_empty() {
            info!("triggerlog memory-only mode");
            return;
        }
        match PgPoolOptions::new().max_connections(5).connect(&uri).await {
            Ok(pool) => {
                if let Err(err) = ensure_schema(&pool).await {
                    error!(error = %err, "triggerlog schema init failed");
                    return;
                }
                *self.pool.write().await = Some(pool.clone());
                let (tx, mut rx) = mpsc::channel::<TriggerRecord>(self.queue_size.max(1) as usize);
                *self.tx.write().await = Some(tx);
                let batch_size = self.batch_size.max(1) as usize;
                let interval = parse_duration(&self.batch_interval);
                let handle = tokio::spawn(async move {
                    let mut buf = Vec::with_capacity(batch_size);
                    let mut ticker = tokio::time::interval(interval);
                    loop {
                        tokio::select! {
                            maybe = rx.recv() => {
                                match maybe {
                                    Some(rec) => {
                                        buf.push(rec);
                                        if buf.len() >= batch_size {
                                            flush_batch(&pool, &mut buf).await;
                                        }
                                    }
                                    None => {
                                        if !buf.is_empty() {
                                            flush_batch(&pool, &mut buf).await;
                                        }
                                        break;
                                    }
                                }
                            }
                            _ = ticker.tick() => {
                                if !buf.is_empty() {
                                    flush_batch(&pool, &mut buf).await;
                                }
                            }
                        }
                    }
                });
                *self.worker.write().await = Some(handle);
                info!("triggerlog DB writer started");
            }
            Err(err) => error!(error = %err, "triggerlog connect failed"),
        }
    }

    pub async fn stop(&self) -> Result<()> {
        self.enabled.store(false, Ordering::Relaxed);
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
        self.enabled.store(true, Ordering::Relaxed);
        Ok(())
    }

    pub async fn record_async(&self, rec: TriggerRecord) {
        if !self.enabled.load(Ordering::Relaxed) {
            return;
        }
        self.total.fetch_add(1, Ordering::Relaxed);
        if let Some(tx) = self.tx.read().await.as_ref() {
            let _ = tx.try_send(rec);
        }
    }

    pub async fn stats(&self) -> TriggerLogStats {
        let mut total = self.total.load(Ordering::Relaxed);
        if let Some(pool) = self.pool.read().await.as_ref()
            && let Ok(row) = sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM trigger_logs")
                .fetch_one(pool)
                .await
        {
            total = total.max(row);
        }
        TriggerLogStats { total }
    }

    pub async fn list_recent(&self, limit: i64) -> Vec<Value> {
        let Some(pool) = self.pool.read().await.clone() else {
            return vec![];
        };
        let limit = if limit <= 0 { 50 } else { limit.min(500) };
        match sqlx::query_as::<
            _,
            (
                i64,
                String,
                String,
                String,
                bool,
                String,
                i64,
                chrono::DateTime<chrono::Utc>,
            ),
        >(
            r#"
            SELECT id, plugin_id, listener_id, trace_id, success, error, duration_ms, created_at
            FROM trigger_logs
            ORDER BY id DESC
            LIMIT $1
            "#,
        )
        .bind(limit)
        .fetch_all(&pool)
        .await
        {
            Ok(rows) => rows
                .into_iter()
                .map(
                    |(
                        id,
                        plugin_id,
                        listener_id,
                        trace_id,
                        success,
                        error,
                        duration_ms,
                        created_at,
                    )| {
                        serde_json::json!({
                            "id": id,
                            "plugin_id": plugin_id,
                            "listener_id": listener_id,
                            "trace_id": trace_id,
                            "success": success,
                            "error": error,
                            "duration_ms": duration_ms,
                            "created_at": created_at,
                        })
                    },
                )
                .collect(),
            Err(err) => {
                warn!(error = %err, "triggerlog list failed");
                vec![]
            }
        }
    }
}

async fn ensure_schema(pool: &PgPool) -> Result<()> {
    sqlx::query(
        r#"
        CREATE TABLE IF NOT EXISTS trigger_logs (
            id BIGSERIAL PRIMARY KEY,
            trace_id TEXT NOT NULL DEFAULT '',
            plugin_id TEXT NOT NULL DEFAULT '',
            listener_id TEXT NOT NULL DEFAULT '',
            trace_type TEXT NOT NULL DEFAULT '',
            self_id BIGINT NOT NULL DEFAULT 0,
            user_id BIGINT NOT NULL DEFAULT 0,
            group_id BIGINT NOT NULL DEFAULT 0,
            success BOOLEAN NOT NULL DEFAULT TRUE,
            error TEXT NOT NULL DEFAULT '',
            duration_ms BIGINT NOT NULL DEFAULT 0,
            data JSONB NOT NULL DEFAULT '{}'::jsonb,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )
        "#,
    )
    .execute(pool)
    .await
    .context("create trigger_logs")?;
    Ok(())
}

async fn flush_batch(pool: &PgPool, buf: &mut Vec<TriggerRecord>) {
    for rec in buf.drain(..) {
        if let Err(err) = sqlx::query(
            r#"
            INSERT INTO trigger_logs
                (trace_id, plugin_id, listener_id, trace_type, self_id, user_id, group_id,
                 success, error, duration_ms, data)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
            "#,
        )
        .bind(&rec.trace_id)
        .bind(&rec.plugin_id)
        .bind(&rec.listener_id)
        .bind(&rec.trace_type)
        .bind(rec.self_id)
        .bind(rec.user_id)
        .bind(rec.group_id)
        .bind(rec.success)
        .bind(&rec.error)
        .bind(rec.duration_ms)
        .bind(&rec.data)
        .execute(pool)
        .await
        {
            warn!(error = %err, "triggerlog insert failed");
        }
    }
}

fn parse_duration(s: &str) -> std::time::Duration {
    let s = s.trim();
    if let Some(num) = s.strip_suffix('s')
        && let Ok(v) = num.parse::<u64>()
    {
        return std::time::Duration::from_secs(v.max(1));
    }
    if let Some(num) = s.strip_suffix("ms")
        && let Ok(v) = num.parse::<u64>()
    {
        return std::time::Duration::from_millis(v.max(1));
    }
    std::time::Duration::from_secs(5)
}
