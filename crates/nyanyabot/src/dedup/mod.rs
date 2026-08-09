use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};

use parking_lot::Mutex;

pub trait Deduper: Send + Sync {
    /// Returns true if this is the first time seeing (group_id, seq).
    fn try_mark_processed(&self, group_id: i64, seq: i64) -> bool;
}

#[derive(Debug)]
pub struct MemoryDeduper {
    inner: Mutex<HashMap<(i64, i64), Instant>>,
    ttl: Duration,
}

impl MemoryDeduper {
    pub fn new(ttl_seconds: u64) -> Arc<Self> {
        Arc::new(Self {
            inner: Mutex::new(HashMap::new()),
            ttl: Duration::from_secs(ttl_seconds.max(1)),
        })
    }

    fn cleanup(&self, map: &mut HashMap<(i64, i64), Instant>, now: Instant) {
        map.retain(|_, ts| now.duration_since(*ts) < self.ttl);
    }
}

impl Deduper for MemoryDeduper {
    fn try_mark_processed(&self, group_id: i64, seq: i64) -> bool {
        let mut map = self.inner.lock();
        let now = Instant::now();
        self.cleanup(&mut map, now);
        match map.entry((group_id, seq)) {
            std::collections::hash_map::Entry::Occupied(_) => false,
            std::collections::hash_map::Entry::Vacant(v) => {
                v.insert(now);
                true
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn marks_once() {
        let d = MemoryDeduper::new(60);
        assert!(d.try_mark_processed(1, 2));
        assert!(!d.try_mark_processed(1, 2));
        assert!(d.try_mark_processed(1, 3));
    }
}
