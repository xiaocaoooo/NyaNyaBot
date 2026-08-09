use std::sync::Arc;
use std::sync::atomic::{AtomicI64, Ordering};
use std::time::{Duration, Instant};

use async_trait::async_trait;
use nyanyabot_proto::{CommandMatch, Descriptor, StructuredError};
use serde_json::Value;
use std::sync::Mutex;
use tracing::{info, warn};

use crate::plugin::Plugin;

/// Wraps a plugin so it can be put to sleep after idle timeout and woken on demand.
pub struct LazyPlugin {
    inner: Arc<dyn Plugin>,
    last_used: Mutex<Instant>,
    sleep_timeout_secs: AtomicI64,
    sleeping: Mutex<bool>,
    plugin_id: String,
}

impl LazyPlugin {
    pub fn new(plugin_id: String, inner: Arc<dyn Plugin>, sleep_timeout_secs: i64) -> Arc<Self> {
        Arc::new(Self {
            inner,
            last_used: Mutex::new(Instant::now()),
            sleep_timeout_secs: AtomicI64::new(sleep_timeout_secs),
            sleeping: Mutex::new(false),
            plugin_id,
        })
    }

    pub fn touch(&self) {
        *self.last_used.lock().expect("lock") = Instant::now();
        *self.sleeping.lock().expect("lock") = false;
    }

    pub fn set_sleep_timeout(&self, secs: i64) {
        self.sleep_timeout_secs.store(secs, Ordering::Relaxed);
    }

    pub fn maybe_sleep(&self) -> bool {
        let timeout = self.sleep_timeout_secs.load(Ordering::Relaxed);
        if timeout <= 0 {
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
}

#[async_trait]
impl Plugin for LazyPlugin {
    async fn descriptor(&self) -> Result<Descriptor, StructuredError> {
        // Listing descriptors should not wake a sleeping plugin.
        self.inner.descriptor().await
    }

    async fn configure(&self, config: Value) -> Result<(), StructuredError> {
        self.touch();
        self.inner.configure(config).await
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
        self.touch();
        self.inner.invoke(method, params, caller_plugin_id).await
    }

    async fn handle(
        &self,
        listener_id: &str,
        event_raw: Value,
        match_data: Option<CommandMatch>,
        trace_id: &str,
    ) -> Result<(), StructuredError> {
        if self.is_sleeping() {
            warn!(plugin_id = %self.plugin_id, "handle while sleeping; caller should ensure_awake");
        }
        self.touch();
        self.inner
            .handle(listener_id, event_raw, match_data, trace_id)
            .await
    }

    async fn status(&self) -> Result<String, StructuredError> {
        if self.is_sleeping() {
            return Ok("Sleeping".into());
        }
        // Do not touch/wake on status checks.
        match self.inner.status().await {
            Ok(s) if s.trim().is_empty() => Ok("Idle".into()),
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
    use nyanyabot_proto::{CommandMatch, Descriptor, StructuredError};
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
        ) -> Result<(), StructuredError> {
            Ok(())
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
    }
}
