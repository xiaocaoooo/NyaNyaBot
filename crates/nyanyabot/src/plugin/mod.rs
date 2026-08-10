use std::collections::HashMap;
use std::sync::Arc;

use anyhow::{Result, bail};
use async_trait::async_trait;
use nyanyabot_proto::{CommandMatch, Descriptor, HandleResult, StructuredError};
use serde_json::Value;
use tokio::sync::RwLock;

/// Host-side view of a loaded plugin (RPC-backed).
#[async_trait]
pub trait Plugin: Send + Sync {
    async fn descriptor(&self) -> Result<Descriptor, StructuredError>;
    async fn configure(&self, config: Value) -> Result<(), StructuredError>;
    async fn invoke(
        &self,
        method: &str,
        params: Value,
        caller_plugin_id: &str,
    ) -> Result<Value, StructuredError>;
    async fn handle(
        &self,
        listener_id: &str,
        event_raw: Value,
        match_data: Option<CommandMatch>,
        trace_id: &str,
    ) -> Result<HandleResult, StructuredError>;
    async fn status(&self) -> Result<String, StructuredError>;
    async fn shutdown(&self) -> Result<(), StructuredError>;
}

#[derive(Clone)]
struct Entry {
    plugin: Arc<dyn Plugin>,
    descriptor: Descriptor,
}

#[derive(Default)]
pub struct Manager {
    inner: RwLock<HashMap<String, Entry>>,
}

impl Manager {
    pub fn new() -> Arc<Self> {
        Arc::new(Self {
            inner: RwLock::new(HashMap::new()),
        })
    }

    pub async fn register(&self, plugin: Arc<dyn Plugin>, descriptor: Descriptor) -> Result<()> {
        if descriptor.plugin_id.trim().is_empty() {
            bail!("plugin_id is empty");
        }
        let mut map = self.inner.write().await;
        if map.contains_key(&descriptor.plugin_id) {
            bail!("plugin already registered: {}", descriptor.plugin_id);
        }
        map.insert(descriptor.plugin_id.clone(), Entry { plugin, descriptor });
        Ok(())
    }

    pub async fn unregister(&self, plugin_id: &str) -> Option<Arc<dyn Plugin>> {
        self.inner.write().await.remove(plugin_id).map(|e| e.plugin)
    }

    pub async fn get(&self, plugin_id: &str) -> Option<(Arc<dyn Plugin>, Descriptor)> {
        self.inner
            .read()
            .await
            .get(plugin_id)
            .map(|e| (e.plugin.clone(), e.descriptor.clone()))
    }

    pub async fn update_descriptor(&self, plugin_id: &str, descriptor: Descriptor) {
        if let Some(entry) = self.inner.write().await.get_mut(plugin_id) {
            entry.descriptor = descriptor;
        }
    }

    pub async fn list(&self) -> Vec<Descriptor> {
        let mut out: Vec<_> = self
            .inner
            .read()
            .await
            .values()
            .map(|e| e.descriptor.clone())
            .collect();
        out.sort_by(|a, b| a.plugin_id.cmp(&b.plugin_id));
        out
    }

    pub async fn entries(&self) -> Vec<(String, Descriptor, Arc<dyn Plugin>)> {
        self.inner
            .read()
            .await
            .iter()
            .map(|(id, e)| (id.clone(), e.descriptor.clone(), e.plugin.clone()))
            .collect()
    }

    pub async fn plugin_ids(&self) -> Vec<String> {
        let mut ids: Vec<_> = self.inner.read().await.keys().cloned().collect();
        ids.sort();
        ids
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use nyanyabot_proto::Descriptor;

    struct Dummy;
    #[async_trait]
    impl Plugin for Dummy {
        async fn descriptor(&self) -> Result<Descriptor, StructuredError> {
            Ok(Descriptor {
                plugin_id: "t".into(),
                name: "t".into(),
                version: "0".into(),
                author: "a".into(),
                description: "d".into(),
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
            Ok("OK".into())
        }
        async fn shutdown(&self) -> Result<(), StructuredError> {
            Ok(())
        }
    }

    #[tokio::test]
    async fn register_list() {
        let m = Manager::new();
        let d = Descriptor {
            plugin_id: "a".into(),
            name: "A".into(),
            version: "1".into(),
            author: "x".into(),
            description: "y".into(),
            ..Default::default()
        };
        m.register(Arc::new(Dummy), d).await.unwrap();
        assert_eq!(m.list().await.len(), 1);
    }
}
