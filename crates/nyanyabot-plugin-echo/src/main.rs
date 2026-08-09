use std::sync::Arc;

use async_trait::async_trait;
use nyanyabot_proto::{
    CommandListener, CommandMatch, ConfigSpec, Descriptor, HandleResult, HostClient, Plugin,
    StructuredError, run_plugin_with_host,
};
use parking_lot::RwLock as SyncRwLock;
use serde::Deserialize;
use serde_json::{Value, json};
use tokio::sync::RwLock;
use tracing_subscriber::EnvFilter;

#[derive(Debug, Clone, Deserialize)]
struct EchoConfig {
    #[serde(default = "default_prefix")]
    prefix: String,
}

fn default_prefix() -> String {
    "echo: ".into()
}

impl Default for EchoConfig {
    fn default() -> Self {
        Self {
            prefix: default_prefix(),
        }
    }
}

struct EchoPlugin {
    host: Arc<RwLock<Option<HostClient>>>,
    config: SyncRwLock<EchoConfig>,
}

fn plugin_descriptor() -> Descriptor {
    Descriptor {
        name: "Echo".into(),
        plugin_id: "external.echo".into(),
        version: "0.2.0".into(),
        author: "nyanyabot".into(),
        description: "回声测试插件".into(),
        config: Some(ConfigSpec {
            version: Some("1".into()),
            description: Some("Echo plugin config".into()),
            schema: Some(json!({
                "type": "object",
                "properties": {
                    "prefix": {"type": "string", "default": "echo: "}
                }
            })),
            default: Some(json!({"prefix": "echo: "})),
        }),
        commands: vec![CommandListener {
            name: "echo".into(),
            id: "cmd.echo".into(),
            description: "匹配 /echo xxx 并回声".into(),
            pattern: r"^echo\s+(.+)$".into(),
            match_raw: false,
            handler: "HandleEcho".into(),
        }],
        ..Default::default()
    }
}

#[async_trait]
impl Plugin for EchoPlugin {
    async fn descriptor(&self) -> Result<Descriptor, StructuredError> {
        Ok(plugin_descriptor())
    }

    async fn configure(&self, config: Value) -> Result<(), StructuredError> {
        let parsed: EchoConfig = serde_json::from_value(config).unwrap_or_default();
        *self.config.write() = parsed;
        Ok(())
    }

    async fn invoke(
        &self,
        _method: &str,
        _params: Value,
        _caller_plugin_id: &str,
    ) -> Result<Value, StructuredError> {
        Err(StructuredError::not_found("method is not exported"))
    }

    async fn handle(
        &self,
        listener_id: &str,
        event_raw: Value,
        match_data: Option<CommandMatch>,
        trace_id: &str,
    ) -> Result<HandleResult, StructuredError> {
        if listener_id != "cmd.echo" {
            return Ok(HandleResult {});
        }
        let text = match_data
            .and_then(|m| m.groups.first().cloned())
            .unwrap_or_default();
        let prefix = self.config.read().prefix.clone();
        let reply = format!("{prefix}{text}");
        let host = self.host.read().await.clone();
        if let Some(mut host) = host {
            send_message(&mut host, &event_raw, &reply, trace_id).await;
        }
        Ok(HandleResult {})
    }

    async fn status(&self) -> Result<String, StructuredError> {
        Ok("OK".into())
    }

    async fn shutdown(&self) -> Result<(), StructuredError> {
        Ok(())
    }
}

async fn send_message(host: &mut HostClient, event_raw: &Value, text: &str, trace_id: &str) {
    let msg_type = event_raw
        .get("message_type")
        .and_then(|v| v.as_str())
        .unwrap_or("");
    let self_id = event_raw
        .get("self_id")
        .and_then(|v| v.as_i64())
        .unwrap_or(0);
    if msg_type == "group" {
        let group_id = event_raw.get("group_id").cloned().unwrap_or(Value::Null);
        let _ = host
            .call_onebot(
                "send_group_msg",
                &json!({"group_id": group_id, "message": text}),
                self_id,
                trace_id,
            )
            .await;
    } else {
        let user_id = event_raw.get("user_id").cloned().unwrap_or(Value::Null);
        let _ = host
            .call_onebot(
                "send_private_msg",
                &json!({"user_id": user_id, "message": text}),
                self_id,
                trace_id,
            )
            .await;
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    tracing_subscriber::fmt()
        .with_env_filter(EnvFilter::from_default_env().add_directive("info".parse()?))
        .with_writer(std::io::stderr)
        .init();

    let host = Arc::new(RwLock::new(None));
    let plugin = Arc::new(EchoPlugin {
        host: host.clone(),
        config: SyncRwLock::new(EchoConfig::default()),
    });
    run_plugin_with_host(plugin, host).await
}

#[cfg(test)]
mod descriptor_snapshot_tests {
    use super::plugin_descriptor;

    #[test]
    fn descriptor_matches_snapshot() {
        let mut d = plugin_descriptor();
        nyanyabot_proto::ensure_descriptor_arrays(&mut d);
        let actual = serde_json::to_string_pretty(&d).expect("serialize descriptor");
        let path =
            std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("snapshots/descriptor.json");
        if std::env::var("UPDATE_SNAPSHOTS").is_ok() {
            std::fs::create_dir_all(path.parent().unwrap()).unwrap();
            std::fs::write(&path, format!("{actual}\n")).unwrap();
        }
        let expected = std::fs::read_to_string(&path)
            .unwrap_or_else(|e| panic!("missing descriptor snapshot at {}: {e}", path.display()));
        assert_eq!(
            actual.trim(),
            expected.trim(),
            "descriptor snapshot mismatch; run with UPDATE_SNAPSHOTS=1"
        );
    }
}
