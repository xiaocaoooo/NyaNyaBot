use std::collections::HashMap;
use std::sync::Arc;
use std::sync::atomic::{AtomicI64, Ordering};
use std::time::Duration;

use chrono::{DateTime, Utc};
use dashmap::DashMap;
use parking_lot::Mutex;
use serde::Serialize;

#[derive(Debug, Clone, Serialize)]
pub struct Snapshot {
    pub recv_count: i64,
    pub sent_count: i64,
    pub filtered_self_count: i64,
    pub filtered_non_group_count: i64,
    pub dedup_count: i64,
    #[serde(skip_serializing_if = "HashMap::is_empty")]
    pub plugin_sent_stats: HashMap<String, i64>,
    pub start_time: DateTime<Utc>,
    pub uptime: String,
}

#[derive(Debug)]
pub struct Stats {
    recv: AtomicI64,
    sent: AtomicI64,
    filtered_self: AtomicI64,
    filtered_non_group: AtomicI64,
    dedup: AtomicI64,
    plugin_sent: DashMap<String, AtomicI64>,
    start: Mutex<std::time::Instant>,
    start_wall: Mutex<DateTime<Utc>>,
}

impl Stats {
    pub fn new() -> Arc<Self> {
        Arc::new(Self {
            recv: AtomicI64::new(0),
            sent: AtomicI64::new(0),
            filtered_self: AtomicI64::new(0),
            filtered_non_group: AtomicI64::new(0),
            dedup: AtomicI64::new(0),
            plugin_sent: DashMap::new(),
            start: Mutex::new(std::time::Instant::now()),
            start_wall: Mutex::new(Utc::now()),
        })
    }

    pub fn inc_recv(&self) {
        self.recv.fetch_add(1, Ordering::Relaxed);
    }

    pub fn inc_sent(&self) {
        self.sent.fetch_add(1, Ordering::Relaxed);
    }

    pub fn inc_sent_by_plugin(&self, plugin_id: &str) {
        if plugin_id.is_empty() {
            return;
        }
        self.plugin_sent
            .entry(plugin_id.to_string())
            .or_insert_with(|| AtomicI64::new(0))
            .fetch_add(1, Ordering::Relaxed);
        self.inc_sent();
    }

    pub fn inc_filtered_self(&self) {
        self.filtered_self.fetch_add(1, Ordering::Relaxed);
    }

    pub fn inc_filtered_non_group(&self) {
        self.filtered_non_group.fetch_add(1, Ordering::Relaxed);
    }

    pub fn inc_dedup(&self) {
        self.dedup.fetch_add(1, Ordering::Relaxed);
    }

    pub fn snapshot(&self) -> Snapshot {
        let start = *self.start.lock();
        let elapsed = start.elapsed();
        let mut plugin_sent_stats = HashMap::new();
        for entry in self.plugin_sent.iter() {
            plugin_sent_stats.insert(entry.key().clone(), entry.value().load(Ordering::Relaxed));
        }
        Snapshot {
            recv_count: self.recv.load(Ordering::Relaxed),
            sent_count: self.sent.load(Ordering::Relaxed),
            filtered_self_count: self.filtered_self.load(Ordering::Relaxed),
            filtered_non_group_count: self.filtered_non_group.load(Ordering::Relaxed),
            dedup_count: self.dedup.load(Ordering::Relaxed),
            plugin_sent_stats,
            start_time: *self.start_wall.lock(),
            uptime: format_uptime(elapsed),
        }
    }
}

fn format_uptime(d: Duration) -> String {
    // Align with Go stats.FormatDuration Chinese output.
    let total = d.as_secs();
    let days = total / 86400;
    let hours = (total % 86400) / 3600;
    let mins = (total % 3600) / 60;
    let secs = total % 60;
    let mut parts = Vec::new();
    if days > 0 {
        parts.push(format!("{days}天"));
    }
    if hours > 0 {
        parts.push(format!("{hours}小时"));
    }
    if mins > 0 {
        parts.push(format!("{mins}分"));
    }
    if secs > 0 || parts.is_empty() {
        parts.push(format!("{secs}秒"));
    }
    parts.concat()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn uptime_chinese() {
        assert_eq!(format_uptime(Duration::from_secs(12)), "12秒");
        assert_eq!(format_uptime(Duration::from_secs(60 + 34)), "1分34秒");
        assert_eq!(
            format_uptime(Duration::from_secs(12 * 3600 + 45 * 60 + 23)),
            "12小时45分23秒"
        );
        assert_eq!(
            format_uptime(Duration::from_secs(23 * 86400 + 23 * 3600 + 23 * 60 + 23)),
            "23天23小时23分23秒"
        );
    }
}
