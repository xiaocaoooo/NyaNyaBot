use std::sync::Arc;
use std::sync::atomic::{AtomicI64, AtomicU64, Ordering};
use std::time::{Duration, Instant};

use async_trait::async_trait;
use nyanyabot_proto::{CommandMatch, Descriptor, HandleResult, StructuredError};
use serde_json::Value;
use std::sync::Mutex;
use tracing::{info, warn};

use crate::plugin::Plugin;

/// Wraps a plugin so it can be put to sleep after idle timeout and woken on demand.
/// Status strings follow Go LazyPlugin: Sleeping / Idle / Running / Crashed.
pub struct LazyPlugin {
    inner: Arc<dyn Plugin>,
    last_used: Mutex<Instant>,
    sleep_timeout_secs: AtomicI64,
    sleeping: Mutex<bool>,
    /// In-flight handle/invoke/configure calls (Go activeCalls).
    active_calls: AtomicU64,
    /// Unexpected process death observed by host watcher.
    crashed: Mutex<bool>,
    plugin_id: String,
}

impl LazyPlugin {
    pub fn new(plugin_id: String, inner: Arc<dyn Plugin>, sleep_timeout_secs: i64) -> Arc<Self> {
        Arc::new(Self {
            inner,
            last_used: Mutex::new(Instant::now()),
            sleep_timeout_secs: AtomicI64::new(sleep_timeout_secs),
            sleeping: Mutex::new(false),
            active_calls: AtomicU64::new(0),
            crashed: Mutex::new(false),
            plugin_id,
        })
    }

    pub fn touch(&self) {
        *self.last_used.lock().expect("lock") = Instant::now();
        *self.sleeping.lock().expect("lock") = false;
        *self.crashed.lock().expect("lock") = false;
    }

    pub fn set_sleep_timeout(&self, secs: i64) {
        self.sleep_timeout_secs.store(secs, Ordering::Relaxed);
    }

    pub fn mark_crashed(&self) {
        *self.crashed.lock().expect("lock") = true;
    }

    pub fn clear_crashed(&self) {
        *self.crashed.lock().expect("lock") = false;
    }

    pub fn maybe_sleep(&self) -> bool {
        let timeout = self.sleep_timeout_secs.load(Ordering::Relaxed);
        if timeout <= 0 {
            return false;
        }
        if self.active_calls.load(Ordering::Relaxed) > 0 {
            return false;
        }
        let idle = self.last_used.lock().expect("lock").elapsed();
        if idle >= Duration::from_secs(timeout as u64) {
            let mut sleeping = self.sleeping.lock().expect("lock");
            if !*sleeping {
                *sleeping = true;
                info!(
                    plugin_id = %self.plugin_id,
                    idle_secs = idle.as_secs(),
                    "plugin idle sleep marked"
                );
                return true;
            }
        }
        false
    }

    pub fn is_sleeping(&self) -> bool {
        *self.sleeping.lock().expect("lock")
    }

    pub fn inner(&self) -> Arc<dyn Plugin> {
        self.inner.clone()
    }

    fn begin_call(&self) {
        self.touch();
        self.active_calls.fetch_add(1, Ordering::Relaxed);
    }

    fn end_call(&self) {
        let prev = self.active_calls.fetch_sub(1, Ordering::Relaxed);
        if prev == 0 {
            // underflow guard
            self.active_calls.store(0, Ordering::Relaxed);
        }
    }
}

#[async_trait]
impl Plugin for LazyPlugin {
    async fn descriptor(&self) -> Result<Descriptor, StructuredError> {
        // Listing descriptors should not wake a sleeping plugin.
        self.inner.descriptor().await
    }

    async fn configure(&self, config: Value) -> Result<(), StructuredError> {
        self.begin_call();
        let res = self.inner.configure(config).await;
        self.end_call();
        res
    }

    async fn invoke(
        &self,
        method: &str,
        params: Value,
        caller_plugin_id: &str,
    ) -> Result<Value, StructuredError> {
        if self.is_sleeping() {
            warn!(plugin_id = %self.plugin_id, "invoke while sleeping; caller should ensure_awake");
        }
        self.begin_call();
        let res = self.inner.invoke(method, params, caller_plugin_id).await;
        self.end_call();
        res
    }

    async fn handle(
        &self,
        listener_id: &str,
        event_raw: Value,
        match_data: Option<CommandMatch>,
        trace_id: &str,
    ) -> Result<HandleResult, StructuredError> {
        if self.is_sleeping() {
            warn!(plugin_id = %self.plugin_id, "handle while sleeping; caller should ensure_awake");
        }
        self.begin_call();
        let res = self
            .inner
            .handle(listener_id, event_raw, match_data, trace_id)
            .await;
        self.end_call();
        res
    }

    async fn status(&self) -> Result<String, StructuredError> {
        // Do not touch/wake on status checks (Go parity).
        if self.is_sleeping() {
            return Ok("Sleeping".into());
        }
        if *self.crashed.lock().expect("lock") {
            return Ok("Crashed".into());
        }
        if self.active_calls.load(Ordering::Relaxed) == 0 {
            // Process may still be up; Go reports Idle when no active calls.
            return Ok("Idle".into());
        }
        match self.inner.status().await {
            Ok(s) if s.trim().is_empty() => Ok("Running".into()),
            Ok(s) => Ok(s),
            Err(_) => Ok("Crashed".into()),
        }
    }

    async fn shutdown(&self) -> Result<(), StructuredError> {
        self.inner.shutdown().await
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use async_trait::async_trait;
    use nyanyabot_proto::{CommandMatch, Descriptor, HandleResult, StructuredError};
    use serde_json::Value;
    use std::sync::Arc;

    struct Dummy;
    #[async_trait]
    impl Plugin for Dummy {
        async fn descriptor(&self) -> Result<Descriptor, StructuredError> {
            Ok(Descriptor {
                plugin_id: "dummy".into(),
                ..Default::default()
            })
        }
        async fn configure(&self, _: Value) -> Result<(), StructuredError> {
            Ok(())
        }
        async fn invoke(&self, _: &str, _: Value, _: &str) -> Result<Value, StructuredError> {
            Ok(Value::Null)
        }
        async fn handle(
            &self,
            _: &str,
            _: Value,
            _: Option<CommandMatch>,
            _: &str,
        ) -> Result<HandleResult, StructuredError> {
            Ok(HandleResult::default())
        }
        async fn status(&self) -> Result<String, StructuredError> {
            Ok("Running".into())
        }
        async fn shutdown(&self) -> Result<(), StructuredError> {
            Ok(())
        }
    }

    #[tokio::test]
    async fn marks_sleep_after_timeout() {
        let lazy = LazyPlugin::new("dummy".into(), Arc::new(Dummy), 1);
        assert!(!lazy.maybe_sleep());
        *lazy.last_used.lock().unwrap() = Instant::now() - Duration::from_secs(5);
        assert!(lazy.maybe_sleep());
        assert!(lazy.is_sleeping());
        assert_eq!(lazy.status().await.unwrap(), "Sleeping");
        lazy.touch();
        assert!(!lazy.is_sleeping());
        assert_eq!(lazy.status().await.unwrap(), "Idle");
    }

    #[tokio::test]
    async fn status_idle_vs_crashed() {
        let lazy = LazyPlugin::new("dummy".into(), Arc::new(Dummy), 60);
        assert_eq!(lazy.status().await.unwrap(), "Idle");
        lazy.mark_crashed();
        assert_eq!(lazy.status().await.unwrap(), "Crashed");
        lazy.clear_crashed();
        lazy.begin_call();
        assert_eq!(lazy.status().await.unwrap(), "Running");
        lazy.end_call();
        assert_eq!(lazy.status().await.unwrap(), "Idle");
    }
}
