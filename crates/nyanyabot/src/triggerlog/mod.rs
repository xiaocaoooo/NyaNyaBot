use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicI64, Ordering};

use anyhow::{Context, Result};
use chrono::{DateTime, Utc};
use serde::Serialize;
use serde_json::Value;
use sqlx::postgres::PgPoolOptions;
use sqlx::{PgPool, Postgres, QueryBuilder, Row};
use tokio::sync::{RwLock, mpsc};
use tracing::{error, info, warn};

use crate::config::TriggerLogConfig;

#[derive(Debug, Clone, Serialize, Default)]
pub struct PluginTriggerLogStatistics {
    pub total_count: i64,
    pub success_count: i64,
    pub failed_count: i64,
    pub avg_duration_ms: f64,
}

#[derive(Debug, Clone, Serialize)]
pub struct PluginTriggerLogDto {
    pub trace_id: String,
    pub plugin_id: String,
    pub listener_id: String,
    pub listener_type: String,
    pub group_id: i64,
    pub user_id: i64,
    pub self_id: i64,
    pub message_id: i64,
    pub message_seq: String,
    pub trigger_data: Value,
    pub success: bool,
    pub duration_ms: i64,
    pub error_message: String,
    pub triggered_at: String,
    pub recorded_at: String,
}

#[derive(Debug, Clone, Default)]
pub struct PluginTriggerLogQuery {
    pub group_id: Option<i64>,
    pub user_id: Option<i64>,
    pub plugin_id: Option<String>,
    pub listener_id: Option<String>,
    pub listener_type: Option<String>,
    pub trace_id: Option<String>,
    pub message_seq: Option<String>,
    pub success: Option<bool>,
    pub start_time: Option<DateTime<Utc>>,
    pub end_time: Option<DateTime<Utc>>,
    pub sort_by: String,
    pub sort_desc: bool,
    pub page: i64,
    pub page_size: i64,
}

impl PluginTriggerLogQuery {
    pub fn normalize(mut self) -> Self {
        if self.page <= 0 {
            self.page = 1;
        }
        if self.page_size <= 0 {
            self.page_size = 20;
        }
        if self.page_size > 200 {
            self.page_size = 200;
        }
        match self.sort_by.as_str() {
            "duration_ms" | "triggered_at" => {}
            _ => self.sort_by = "triggered_at".into(),
        }
        self
    }
}

#[derive(Clone, Debug)]
pub struct TriggerRecord {
    pub trace_id: String,
    pub plugin_id: String,
    pub listener_id: String,
    pub listener_type: String,
    pub self_id: i64,
    pub user_id: i64,
    pub group_id: i64,
    pub message_id: i64,
    pub message_seq: String,
    pub success: bool,
    pub error_message: String,
    pub duration_ms: i64,
    pub trigger_data: Value,
    pub triggered_at: DateTime<Utc>,
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
        // compatibility shim; real boot is start_async
    }

    pub fn set_enabled(&self, enabled: bool) {
        self.enabled.store(enabled, Ordering::Relaxed);
    }

    pub fn is_enabled(&self) -> bool {
        self.enabled.load(Ordering::Relaxed)
    }

    pub async fn start_async(&self) {
        if !self.enabled.load(Ordering::Relaxed) {
            info!("triggerlog disabled");
            return;
        }
        let uri = self.database_uri.read().await.clone();
        if uri.trim().is_empty() {
            info!("triggerlog disabled (empty database_uri)");
            return;
        }
        match self.connect(&uri).await {
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
                info!("triggerlog recorder started");
            }
            Err(err) => error!(error = %err, "triggerlog connect failed"),
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

    pub async fn reconnect(&self, database_uri: String, enabled: bool) -> Result<()> {
        self.stop().await?;
        *self.database_uri.write().await = database_uri;
        self.enabled.store(enabled, Ordering::Relaxed);
        if enabled {
            self.start_async().await;
        }
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

    pub async fn stats(&self) -> PluginTriggerLogStatistics {
        self.query_stats(&PluginTriggerLogQuery::default().normalize())
            .await
    }

    pub async fn query_logs(
        &self,
        query: &PluginTriggerLogQuery,
    ) -> (Vec<PluginTriggerLogDto>, i64) {
        let q = query.clone().normalize();
        let Some(pool) = self.pool.read().await.clone() else {
            return (vec![], 0);
        };

        let total = match count_logs(&pool, &q).await {
            Ok(n) => n,
            Err(err) => {
                warn!(error = %err, "triggerlog count failed");
                return (vec![], 0);
            }
        };

        match fetch_logs(&pool, &q).await {
            Ok(rows) => (rows, total),
            Err(err) => {
                warn!(error = %err, "triggerlog query failed");
                (vec![], total)
            }
        }
    }

    pub async fn query_stats(&self, query: &PluginTriggerLogQuery) -> PluginTriggerLogStatistics {
        let q = query.clone().normalize();
        let Some(pool) = self.pool.read().await.clone() else {
            return PluginTriggerLogStatistics::default();
        };
        match fetch_stats(&pool, &q).await {
            Ok(stats) => stats,
            Err(err) => {
                warn!(error = %err, "triggerlog stats failed");
                PluginTriggerLogStatistics::default()
            }
        }
    }

    async fn connect(&self, uri: &str) -> Result<PgPool> {
        PgPoolOptions::new()
            .max_connections(5)
            .connect(uri)
            .await
            .context("connect postgres for triggerlog")
    }
}

fn parse_duration(s: &str) -> std::time::Duration {
    let s = s.trim();
    if let Some(num) = s.strip_suffix('s')
        && let Ok(v) = num.parse::<u64>()
    {
        return std::time::Duration::from_secs(v.max(1));
    }
    if let Some(num) = s.strip_suffix('m')
        && let Ok(v) = num.parse::<u64>()
    {
        return std::time::Duration::from_secs(v.max(1) * 60);
    }
    std::time::Duration::from_secs(5)
}

async fn ensure_schema(pool: &PgPool) -> Result<()> {
    let stmts = [
        r#"
        CREATE TABLE IF NOT EXISTS plugin_trigger_logs (
            id BIGSERIAL PRIMARY KEY,
            trace_id TEXT NOT NULL UNIQUE,
            plugin_id TEXT NOT NULL,
            listener_id TEXT NOT NULL,
            listener_type TEXT NOT NULL,
            group_id BIGINT NOT NULL DEFAULT 0,
            user_id BIGINT NOT NULL DEFAULT 0,
            self_id BIGINT NOT NULL DEFAULT 0,
            message_id BIGINT NOT NULL DEFAULT 0,
            message_seq TEXT NOT NULL DEFAULT '',
            trigger_data JSONB NOT NULL DEFAULT '{}'::jsonb,
            success BOOLEAN NOT NULL DEFAULT false,
            duration_ms INTEGER NOT NULL DEFAULT 0,
            error_message TEXT NOT NULL DEFAULT '',
            triggered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )
        "#,
        r#"CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_trace_id ON plugin_trigger_logs (trace_id)"#,
        r#"CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_plugin_id ON plugin_trigger_logs (plugin_id)"#,
        r#"CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_listener_id ON plugin_trigger_logs (listener_id)"#,
        r#"CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_listener_type ON plugin_trigger_logs (listener_type)"#,
        r#"CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_group_id ON plugin_trigger_logs (group_id)"#,
        r#"CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_user_id ON plugin_trigger_logs (user_id)"#,
        r#"CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_self_id ON plugin_trigger_logs (self_id)"#,
        r#"CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_triggered_at ON plugin_trigger_logs (triggered_at)"#,
        r#"CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_success ON plugin_trigger_logs (success)"#,
    ];
    for stmt in stmts {
        sqlx::query(stmt)
            .execute(pool)
            .await
            .context("create plugin_trigger_logs")?;
    }
    Ok(())
}

async fn flush_batch(pool: &PgPool, buf: &mut Vec<TriggerRecord>) {
    for rec in buf.drain(..) {
        if let Err(err) = sqlx::query(
            r#"
            INSERT INTO plugin_trigger_logs
                (trace_id, plugin_id, listener_id, listener_type, group_id, user_id,
                 self_id, message_id, message_seq, trigger_data, success, duration_ms,
                 error_message, triggered_at, recorded_at)
            VALUES
                ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NOW())
            ON CONFLICT (trace_id) DO UPDATE SET
                success = EXCLUDED.success,
                duration_ms = EXCLUDED.duration_ms,
                error_message = EXCLUDED.error_message,
                recorded_at = EXCLUDED.recorded_at
            "#,
        )
        .bind(&rec.trace_id)
        .bind(&rec.plugin_id)
        .bind(&rec.listener_id)
        .bind(&rec.listener_type)
        .bind(rec.group_id)
        .bind(rec.user_id)
        .bind(rec.self_id)
        .bind(rec.message_id)
        .bind(&rec.message_seq)
        .bind(&rec.trigger_data)
        .bind(rec.success)
        .bind(rec.duration_ms as i32)
        .bind(&rec.error_message)
        .bind(rec.triggered_at)
        .execute(pool)
        .await
        {
            warn!(error = %err, trace_id = %rec.trace_id, "triggerlog insert failed");
        }
    }
}

fn push_filters<'a>(qb: &mut QueryBuilder<'a, Postgres>, q: &'a PluginTriggerLogQuery) {
    let mut first = true;
    let mut push = |qb: &mut QueryBuilder<'a, Postgres>, sql: &str| {
        if first {
            qb.push(" WHERE ");
            first = false;
        } else {
            qb.push(" AND ");
        }
        qb.push(sql);
    };

    if let Some(v) = q.group_id {
        push(qb, "group_id = ");
        qb.push_bind(v);
    }
    if let Some(v) = q.user_id {
        push(qb, "user_id = ");
        qb.push_bind(v);
    }
    if let Some(ref v) = q.plugin_id {
        push(qb, "plugin_id = ");
        qb.push_bind(v);
    }
    if let Some(ref v) = q.listener_id {
        push(qb, "listener_id = ");
        qb.push_bind(v);
    }
    if let Some(ref v) = q.listener_type {
        push(qb, "listener_type = ");
        qb.push_bind(v);
    }
    if let Some(ref v) = q.trace_id {
        push(qb, "trace_id = ");
        qb.push_bind(v);
    }
    if let Some(ref v) = q.message_seq {
        push(qb, "message_seq = ");
        qb.push_bind(v);
    }
    if let Some(v) = q.success {
        push(qb, "success = ");
        qb.push_bind(v);
    }
    if let Some(v) = q.start_time {
        push(qb, "triggered_at >= ");
        qb.push_bind(v);
    }
    if let Some(v) = q.end_time {
        push(qb, "triggered_at <= ");
        qb.push_bind(v);
    }
}

async fn count_logs(pool: &PgPool, q: &PluginTriggerLogQuery) -> Result<i64> {
    let mut qb = QueryBuilder::<Postgres>::new("SELECT COUNT(*) FROM plugin_trigger_logs");
    push_filters(&mut qb, q);
    let row = qb.build().fetch_one(pool).await?;
    Ok(row.try_get::<i64, _>(0).unwrap_or(0))
}

async fn fetch_logs(pool: &PgPool, q: &PluginTriggerLogQuery) -> Result<Vec<PluginTriggerLogDto>> {
    let mut qb = QueryBuilder::<Postgres>::new(
        r#"
        SELECT trace_id, plugin_id, listener_id, listener_type, group_id, user_id, self_id,
               message_id, message_seq, trigger_data, success, duration_ms, error_message,
               triggered_at, recorded_at
        FROM plugin_trigger_logs
        "#,
    );
    push_filters(&mut qb, q);
    let order_col = if q.sort_by == "duration_ms" {
        "duration_ms"
    } else {
        "triggered_at"
    };
    qb.push(" ORDER BY ");
    qb.push(order_col);
    if q.sort_desc {
        qb.push(" DESC");
    } else {
        qb.push(" ASC");
    }
    let offset = (q.page - 1) * q.page_size;
    qb.push(" LIMIT ");
    qb.push_bind(q.page_size);
    qb.push(" OFFSET ");
    qb.push_bind(offset);

    let rows = qb.build().fetch_all(pool).await?;
    let mut out = Vec::with_capacity(rows.len());
    for row in rows {
        let triggered_at: DateTime<Utc> = row.try_get("triggered_at")?;
        let recorded_at: DateTime<Utc> = row.try_get("recorded_at")?;
        out.push(PluginTriggerLogDto {
            trace_id: row.try_get("trace_id")?,
            plugin_id: row.try_get("plugin_id")?,
            listener_id: row.try_get("listener_id")?,
            listener_type: row.try_get("listener_type")?,
            group_id: row.try_get("group_id")?,
            user_id: row.try_get("user_id")?,
            self_id: row.try_get("self_id")?,
            message_id: row.try_get("message_id")?,
            message_seq: row.try_get("message_seq")?,
            trigger_data: row.try_get("trigger_data")?,
            success: row.try_get("success")?,
            duration_ms: i64::from(row.try_get::<i32, _>("duration_ms").unwrap_or(0)),
            error_message: row.try_get("error_message")?,
            triggered_at: triggered_at.to_rfc3339(),
            recorded_at: recorded_at.to_rfc3339(),
        });
    }
    Ok(out)
}

async fn fetch_stats(
    pool: &PgPool,
    q: &PluginTriggerLogQuery,
) -> Result<PluginTriggerLogStatistics> {
    let mut qb = QueryBuilder::<Postgres>::new(
        r#"
        SELECT
            COUNT(*)::bigint AS total_count,
            COUNT(*) FILTER (WHERE success)::bigint AS success_count,
            COUNT(*) FILTER (WHERE NOT success)::bigint AS failed_count,
            COALESCE(AVG(duration_ms), 0)::float8 AS avg_duration_ms
        FROM plugin_trigger_logs
        "#,
    );
    push_filters(&mut qb, q);
    let row = qb.build().fetch_one(pool).await?;
    Ok(PluginTriggerLogStatistics {
        total_count: row.try_get("total_count").unwrap_or(0),
        success_count: row.try_get("success_count").unwrap_or(0),
        failed_count: row.try_get("failed_count").unwrap_or(0),
        avg_duration_ms: row.try_get("avg_duration_ms").unwrap_or(0.0),
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn query_normalize_defaults() {
        let q = PluginTriggerLogQuery::default().normalize();
        assert_eq!(q.page, 1);
        assert_eq!(q.page_size, 20);
        assert_eq!(q.sort_by, "triggered_at");
    }
}
