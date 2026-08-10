use std::sync::Arc;

use async_trait::async_trait;
use nyanyabot_proto::{
    CommandListener, CommandMatch, ConfigSpec, Descriptor, ExportSpec, HandleResult, HostClient,
    Plugin, StructuredError, run_plugin_with_host,
};
use parking_lot::RwLock as SyncRwLock;
use serde_json::{Value, json};
use tokio::sync::RwLock;
use tracing_subscriber::EnvFilter;

#[derive(Default)]
struct State {
    raw_config: Value,
    prefix: String,
}

struct ConfigDumpPlugin {
    host: Arc<RwLock<Option<HostClient>>>,
    state: SyncRwLock<State>,
}

fn plugin_descriptor() -> Descriptor {
    Descriptor {
        name: "ConfigDump".into(),
        plugin_id: "external.configdump".into(),
        version: "0.1.0".into(),
        author: "nyanyabot".into(),
        description:
            "Test plugin: reply with the current runtime config (hot updated via Configure)".into(),
        exports: vec![ExportSpec {
            name: "configdump.snapshot".into(),
            description: "返回当前生效配置快照（供其他插件调用）".into(),
            params_schema: json!({"type":"object","properties":{"pretty":{"type":"boolean"}},"additionalProperties":false}),
            result_schema: json!({"type":"object","properties":{"caller_plugin_id":{"type":"string"},"prefix":{"type":"string"},"config":{"type":"object"}},"required":["caller_plugin_id","prefix","config"],"additionalProperties":true}),
        }],
        config: Some(ConfigSpec {
            version: Some("1".into()),
            description: Some("ConfigDump plugin config".into()),
            schema: Some(
                json!({"type":"object","properties":{"prefix":{"type":"string","description":"回复前缀（用于验证热更新是否生效）"}},"additionalProperties":true}),
            ),
            default: Some(json!({"prefix":"CFG: "})),
        }),
        commands: vec![CommandListener {
            name: "cfg".into(),
            id: "cmd.cfg".into(),
            description: "输入 /cfg 或 /cfg pretty，返回插件当前配置（用于测试热更新）".into(),
            pattern: r"^/?cfg(?:\s+(pretty))?$".into(),
            match_raw: false,
            handler: "HandleCfg".into(),
        }],
        ..Default::default()
    }
}

#[async_trait]
impl Plugin for ConfigDumpPlugin {
    async fn descriptor(&self) -> Result<Descriptor, StructuredError> {
        Ok(plugin_descriptor())
    }

    async fn configure(&self, config: Value) -> Result<(), StructuredError> {
        let prefix = config
            .get("prefix")
            .and_then(|v| v.as_str())
            .filter(|s| !s.is_empty())
            .unwrap_or("CFG: ")
            .to_string();
        let mut st = self.state.write();
        st.raw_config = if config.is_null() { json!({}) } else { config };
        st.prefix = prefix;
        Ok(())
    }

    async fn invoke(
        &self,
        method: &str,
        params: Value,
        caller_plugin_id: &str,
    ) -> Result<Value, StructuredError> {
        if method != "configdump.snapshot" {
            return Err(StructuredError::not_found("method is not exported"));
        }
        let pretty = params
            .get("pretty")
            .and_then(|v| v.as_bool())
            .unwrap_or(false);
        let st = self.state.read();
        let mut payload = json!({
            "caller_plugin_id": caller_plugin_id,
            "prefix": st.prefix,
            "config": st.raw_config.clone(),
        });
        if pretty && let Ok(s) = serde_json::to_string_pretty(&st.raw_config) {
            payload
                .as_object_mut()
                .unwrap()
                .insert("config_pretty".into(), json!(s));
        }
        Ok(payload)
    }

    async fn handle(
        &self,
        listener_id: &str,
        event_raw: Value,
        match_data: Option<CommandMatch>,
        trace_id: &str,
    ) -> Result<HandleResult, StructuredError> {
        if listener_id != "cmd.cfg" {
            return Ok(HandleResult::ignored());
        }
        let pretty = match_data
            .as_ref()
            .and_then(|m| m.groups.first())
            .map(|s| s == "pretty")
            .unwrap_or(false);
        let reply = {
            let st = self.state.read();
            let body = if pretty {
                serde_json::to_string_pretty(&st.raw_config).unwrap_or_else(|_| "{}".into())
            } else {
                serde_json::to_string(&st.raw_config).unwrap_or_else(|_| "{}".into())
            };
            format!("{}{}", st.prefix, body)
        };
        if let Some(mut host) = self.host.read().await.clone() {
            let _ = host.report_command_effective(trace_id).await;
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
                        &json!({"group_id": group_id, "message": reply}),
                        self_id,
                        trace_id,
                    )
                    .await;
            } else {
                let user_id = event_raw.get("user_id").cloned().unwrap_or(Value::Null);
                let _ = host
                    .call_onebot(
                        "send_private_msg",
                        &json!({"user_id": user_id, "message": reply}),
                        self_id,
                        trace_id,
                    )
                    .await;
            }
        }
        Ok(HandleResult::handled())
    }

    async fn status(&self) -> Result<String, StructuredError> {
        Ok("OK".into())
    }

    async fn shutdown(&self) -> Result<(), StructuredError> {
        Ok(())
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    tracing_subscriber::fmt()
        .with_env_filter(EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")))
        .with_writer(std::io::stderr)
        .init();
    let host = Arc::new(RwLock::new(None));
    let plugin = Arc::new(ConfigDumpPlugin {
        host: host.clone(),
        state: SyncRwLock::new(State {
            raw_config: json!({"prefix":"CFG: "}),
            prefix: "CFG: ".into(),
        }),
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
        assert_eq!(actual.trim(), expected.trim());
    }
}
