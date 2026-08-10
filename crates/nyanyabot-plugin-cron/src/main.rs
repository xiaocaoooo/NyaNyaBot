use std::sync::Arc;

use async_trait::async_trait;
use nyanyabot_proto::{
    CommandListener, CommandMatch, ConfigSpec, CronListener, Descriptor, HandleResult, HostClient,
    Plugin, StructuredError, run_plugin_with_host,
};
use parking_lot::RwLock as SyncRwLock;
use serde::Deserialize;
use serde_json::{Value, json};
use tokio::sync::RwLock;
use tracing_subscriber::EnvFilter;

#[derive(Debug, Clone, Deserialize, Default)]
struct CronConfig {
    #[serde(default)]
    group_id: i64,
}

struct CronTimePlugin {
    host: Arc<RwLock<Option<HostClient>>>,
    config: SyncRwLock<CronConfig>,
}

fn plugin_descriptor() -> Descriptor {
    Descriptor {
        name: "CronTime".into(),
        plugin_id: "external.cron_time".into(),
        version: "0.1.0".into(),
        author: "nyanyabot".into(),
        description: "每分钟在特定群聊发送当前时间的测试插件".into(),
        config: Some(ConfigSpec {
            version: Some("1".into()),
            description: Some("CronTime plugin config".into()),
            schema: Some(json!({
                "type":"object",
                "properties":{"group_id":{"type":"integer","description":"要发送时间的群聊ID"}},
                "required":["group_id"],
                "additionalProperties": false
            })),
            default: Some(json!({"group_id": 0})),
        }),
        commands: vec![CommandListener {
            name: "set_group".into(),
            id: "cmd.set_group".into(),
            description: "设置发送时间的群号：/set_group 123456".into(),
            pattern: r"^/?set_group\s+(\d+)$".into(),
            match_raw: false,
            handler: "HandleSetGroup".into(),
        }],
        crons: vec![CronListener {
            name: "send_time".into(),
            id: "cron.send_time".into(),
            description: "每分钟发送当前时间".into(),
            schedule: "0 * * * * *".into(),
            handler: "HandleCronTime".into(),
        }],
        ..Default::default()
    }
}

#[async_trait]
impl Plugin for CronTimePlugin {
    async fn descriptor(&self) -> Result<Descriptor, StructuredError> {
        Ok(plugin_descriptor())
    }

    async fn configure(&self, config: Value) -> Result<(), StructuredError> {
        let parsed: CronConfig = serde_json::from_value(config).unwrap_or_default();
        *self.config.write() = parsed;
        Ok(())
    }

    async fn invoke(
        &self,
        _method: &str,
        _params: Value,
        _caller: &str,
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
        match listener_id {
            "cmd.set_group" => {
                if let Some(g) = match_data.and_then(|m| m.groups.first().cloned())
                    && let Ok(id) = g.parse::<i64>()
                {
                    self.config.write().group_id = id;
                }
                let gid = self.config.read().group_id;
                let reply = format!("已设置发送时间的群号为: {gid}");
                if let Some(mut host) = self.host.read().await.clone() {
                    send_message(&mut host, &event_raw, &reply, trace_id).await;
                }
            }
            "cron.send_time" => {
                let gid = self.config.read().group_id;
                if gid == 0 {
                    return Ok(HandleResult::default());
                }
                let now = chrono::Local::now();
                let message = format!(
                    "当前时间：{}\n星期{}",
                    now.format("%Y-%m-%d %H:%M:%S"),
                    now.format("%A")
                );
                if let Some(mut host) = self.host.read().await.clone() {
                    let _ = host
                        .call_onebot(
                            "send_group_msg",
                            &json!({"group_id": gid, "message": message}),
                            0,
                            trace_id,
                        )
                        .await;
                }
            }
            _ => {}
        }
        Ok(HandleResult::default())
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
        .with_env_filter(EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")))
        .with_writer(std::io::stderr)
        .init();
    let host = Arc::new(RwLock::new(None));
    let plugin = Arc::new(CronTimePlugin {
        host: host.clone(),
        config: SyncRwLock::new(CronConfig::default()),
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
