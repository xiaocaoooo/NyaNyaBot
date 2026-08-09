use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use nyanyabot::config::Store;
use nyanyabot::pluginhost::PluginHost;
use nyanyabot::stats::Stats;
use serde_json::{Value, json};
use tempfile::tempdir;

fn workspace_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .parent()
        .unwrap()
        .parent()
        .unwrap()
        .to_path_buf()
}

fn ensure_plugin_bin(name: &str) -> PathBuf {
    let root = workspace_root();
    let bin = root.join("target/debug").join(name);
    if !bin.exists() {
        let status = std::process::Command::new("cargo")
            .args(["build", "-p", name])
            .current_dir(&root)
            .status()
            .unwrap();
        assert!(status.success());
    }
    bin
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn fake_onebot_and_plugin_event_path() {
    let echo_bin = ensure_plugin_bin("nyanyabot-plugin-echo");
    let dir = tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    store.load_or_create_default().unwrap();
    store
        .update(|cfg| {
            cfg.plugins
                .insert("external.echo".into(), json!({"prefix": "e2e: "}));
        })
        .unwrap();

    let pm = nyanyabot::plugin::Manager::new();
    let stats = Stats::new();
    let onebot_calls = Arc::new(tokio::sync::Mutex::new(Vec::<(String, Value)>::new()));
    let onebot_calls2 = onebot_calls.clone();
    let call_onebot: nyanyabot::pluginhost::host_service::CallOneBotFn = Arc::new(
        move |action: String, params: Value, _self_id: i64, _trace: String| {
            let onebot_calls2 = onebot_calls2.clone();
            Box::pin(async move {
                onebot_calls2.lock().await.push((action, params));
                Ok(nyanyabot::onebot::ob11::ApiResponse {
                    status: "ok".into(),
                    retcode: 0,
                    data: json!({"message_id": 42}),
                    ..Default::default()
                })
            })
        },
    );

    let host = PluginHost::new(pm.clone(), store, stats, call_onebot)
        .await
        .unwrap();
    tokio::time::timeout(Duration::from_secs(15), host.load_exec(&echo_bin))
        .await
        .expect("load timeout")
        .unwrap();

    let (plugin, desc) = pm.get("external.echo").await.unwrap();
    assert_eq!(desc.plugin_id, "external.echo");

    tokio::time::timeout(
        Duration::from_secs(10),
        plugin.handle(
            "cmd.echo",
            json!({
                "post_type":"message",
                "message_type":"private",
                "user_id":7,
                "self_id":1,
                "raw_message":"echo e2e",
                "message":[{"type":"text","data":{"text":"echo e2e"}}]
            }),
            Some(nyanyabot_proto::CommandMatch {
                full: "echo e2e".into(),
                groups: vec!["e2e".into()],
            }),
            "e2e-trace",
        ),
    )
    .await
    .expect("handle timeout")
    .unwrap();

    let mut ok = false;
    for _ in 0..50 {
        {
            let calls = onebot_calls.lock().await;
            if calls.iter().any(|(a, _)| a == "send_private_msg") {
                ok = true;
            }
        }
        if ok {
            break;
        }
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    assert!(ok, "expected send_private_msg from echo plugin");

    host.reconfigure_plugin("external.echo").await.unwrap();
    assert_eq!(plugin.status().await.unwrap(), "OK");
    host.close().await;
}
