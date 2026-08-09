use std::sync::Arc;
use std::sync::atomic::{AtomicI64, Ordering};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use chrono::{DateTime, Utc};
use parking_lot::Mutex;
use serde::Serialize;

#[derive(Debug, Clone, Serialize)]
pub struct Snapshot {
    pub recv_count: i64,
    pub sent_count: i64,
    pub dedup_count: i64,
    pub start_time: DateTime<Utc>,
    pub uptime: String,
}

#[derive(Debug)]
pub struct Stats {
    recv: AtomicI64,
    sent: AtomicI64,
    dedup: AtomicI64,
    start: Mutex<Instant>,
    start_wall: Mutex<DateTime<Utc>>,
}

impl Stats {
    pub fn new() -> Arc<Self> {
        Arc::new(Self {
            recv: AtomicI64::new(0),
            sent: AtomicI64::new(0),
            dedup: AtomicI64::new(0),
            start: Mutex::new(Instant::now()),
            start_wall: Mutex::new(Utc::now()),
        })
    }

    pub fn inc_recv(&self) {
        self.recv.fetch_add(1, Ordering::Relaxed);
    }

    pub fn inc_sent(&self) {
        self.sent.fetch_add(1, Ordering::Relaxed);
    }

    pub fn inc_dedup(&self) {
        self.dedup.fetch_add(1, Ordering::Relaxed);
    }

    pub fn snapshot(&self) -> Snapshot {
        let start = *self.start.lock();
        let elapsed = start.elapsed();
        Snapshot {
            recv_count: self.recv.load(Ordering::Relaxed),
            sent_count: self.sent.load(Ordering::Relaxed),
            dedup_count: self.dedup.load(Ordering::Relaxed),
            start_time: *self.start_wall.lock(),
            uptime: format_uptime(elapsed),
        }
    }
}

fn format_uptime(d: Duration) -> String {
    let total = d.as_secs();
    let days = total / 86400;
    let hours = (total % 86400) / 3600;
    let mins = (total % 3600) / 60;
    let secs = total % 60;
    if days > 0 {
        format!("{days}d{hours}h{mins}m{secs}s")
    } else if hours > 0 {
        format!("{hours}h{mins}m{secs}s")
    } else if mins > 0 {
        format!("{mins}m{secs}s")
    } else {
        format!("{secs}s")
    }
}

#[allow(dead_code)]
fn system_time_now() -> SystemTime {
    UNIX_EPOCH + Duration::from_secs(Utc::now().timestamp() as u64)
}
