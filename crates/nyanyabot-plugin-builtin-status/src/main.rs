use std::sync::Arc;

use async_trait::async_trait;
use nyanyabot_proto::{
    CommandListener, CommandMatch, Descriptor, HandleResult, HostClient, Plugin, StructuredError,
    run_plugin_with_host,
};
use serde_json::{Value, json};
use tokio::sync::RwLock;
use tracing_subscriber::EnvFilter;

struct StatusPlugin {
    host: Arc<RwLock<Option<HostClient>>>,
}

fn plugin_descriptor() -> Descriptor {
    Descriptor {
        name: "Builtin Status".into(),
        plugin_id: "builtin.status".into(),
        version: "0.1.0".into(),
        author: "nyanyabot".into(),
        description: "显示 NyaNyaBot 运行状态".into(),
        commands: vec![CommandListener {
            name: "status".into(),
            id: "cmd.status".into(),
            description: "显示机器人状态（收发消息数、运行时间）".into(),
            pattern: r"(?i)^nyanya(bot)?$".into(),
            match_raw: false,
            handler: "HandleStatus".into(),
        }],
        ..Default::default()
    }
}

#[async_trait]
impl Plugin for StatusPlugin {
    async fn descriptor(&self) -> Result<Descriptor, StructuredError> {
        Ok(plugin_descriptor())
    }

    async fn configure(&self, _config: Value) -> Result<(), StructuredError> {
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
        _match_data: Option<CommandMatch>,
        trace_id: &str,
    ) -> Result<HandleResult, StructuredError> {
        if listener_id != "cmd.status" {
            return Ok(HandleResult {});
        }
        let host = {
            let guard = self.host.read().await;
            guard.clone()
        };
        let Some(mut host) = host else {
            return Ok(HandleResult {});
        };
        let stats = host.get_stats().await?;
        let reply = format!(
            "NyaNyaBot\n收/发: {}/{}\n运行时间: {}",
            stats.recv_count, stats.sent_count, stats.uptime
        );
        send_message(&mut host, &event_raw, &reply, trace_id).await;
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
    let plugin = Arc::new(StatusPlugin { host: host.clone() });
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
