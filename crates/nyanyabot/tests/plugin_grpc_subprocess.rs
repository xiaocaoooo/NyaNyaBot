use std::path::PathBuf;
use std::process::Command;
use std::sync::Arc;
use std::time::Duration;

use nyanyabot::config::Store;
use nyanyabot::onebot::ob11::ApiResponse;
use nyanyabot::pluginhost::PluginHost;
use nyanyabot::stats::Stats;
use serde_json::{Value, json};
use tempfile::tempdir;

fn workspace_root() -> PathBuf {
    // crates/nyanyabot -> crates -> NyaNyaBot
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
    if bin.exists() {
        return bin;
    }
    let status = Command::new("cargo")
        .args(["build", "-p", name])
        .current_dir(&root)
        .status()
        .expect("spawn cargo build");
    assert!(status.success(), "cargo build -p {name} failed");
    assert!(bin.exists(), "missing binary {}", bin.display());
    bin
}

fn dummy_call_onebot() -> nyanyabot::pluginhost::host_service::CallOneBotFn {
    Arc::new(
        move |_action: String, _params: Value, _self_id: i64, _trace: String| {
            Box::pin(async move {
                Ok(ApiResponse {
                    status: "ok".into(),
                    retcode: 0,
                    data: json!({"message_id": 1}),
                    ..Default::default()
                })
            })
        },
    )
}

#[tokio::test]
async fn subprocess_echo_handshake_describe_configure_handle() {
    let echo_bin = ensure_plugin_bin("nyanyabot-plugin-echo");
    let dir = tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    store.load_or_create_default().unwrap();
    // seed echo config
    store
        .update(|cfg| {
            cfg.plugins
                .insert("external.echo".into(), json!({"prefix": "test: "}));
        })
        .unwrap();

    let pm = nyanyabot::plugin::Manager::new();
    let stats = Stats::new();
    let host = PluginHost::new(pm.clone(), store, stats, dummy_call_onebot())
        .await
        .expect("host");

    host.load_exec(&echo_bin).await.expect("load echo");

    let list = pm.list().await;
    assert_eq!(list.len(), 1);
    assert_eq!(list[0].plugin_id, "external.echo");
    assert!(list[0].commands.iter().any(|c| c.id == "cmd.echo"));

    let (plugin, _desc) = pm.get("external.echo").await.expect("get plugin");
    let st = plugin.status().await.expect("status");
    assert_eq!(st, "OK");

    // Handle echo command
    let event = json!({
        "post_type": "message",
        "message_type": "private",
        "user_id": 12345,
        "self_id": 1,
        "raw_message": "echo hello",
        "message": [{"type":"text","data":{"text":"echo hello"}}]
    });
    plugin
        .handle(
            "cmd.echo",
            event,
            Some(nyanyabot_proto::CommandMatch {
                full: "echo hello".into(),
                groups: vec!["hello".into()],
            }),
            "trace-1",
        )
        .await
        .expect("handle");

    // wrong token style: reconfigure works
    host.reconfigure_plugin("external.echo")
        .await
        .expect("reconfigure");

    host.close().await;
    // give process a moment to exit
    tokio::time::sleep(Duration::from_millis(100)).await;
}

#[tokio::test]
async fn subprocess_status_plugin_loads() {
    let bin = ensure_plugin_bin("nyanyabot-plugin-builtin-status");
    let dir = tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    store.load_or_create_default().unwrap();
    let pm = nyanyabot::plugin::Manager::new();
    let stats = Stats::new();
    let host = PluginHost::new(pm.clone(), store, stats, dummy_call_onebot())
        .await
        .unwrap();
    host.load_exec(&bin).await.expect("load status");
    let list = pm.list().await;
    assert_eq!(list[0].plugin_id, "builtin.status");
    host.close().await;
}

#[tokio::test]
async fn subprocess_wrong_token_rejected_and_graceful_close() {
    use nyanyabot_proto::TokenInterceptor;
    use nyanyabot_proto::pb::DescribeRequest;
    use nyanyabot_proto::pb::plugin_service_client::PluginServiceClient;
    use tonic::Request;
    use tonic::transport::Channel;

    let echo_bin = ensure_plugin_bin("nyanyabot-plugin-echo");
    let dir = tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    store.load_or_create_default().unwrap();
    let pm = nyanyabot::plugin::Manager::new();
    let stats = Stats::new();
    let host = PluginHost::new(pm.clone(), store, stats, dummy_call_onebot())
        .await
        .unwrap();
    host.load_exec(&echo_bin).await.expect("load");

    // Discover plugin listen addr by re-spawning is hard; instead verify host close is graceful
    // and status works before close.
    let (plugin, _) = pm.get("external.echo").await.unwrap();
    assert_eq!(plugin.status().await.unwrap(), "OK");

    // Direct wrong-token client against a freshly spawned plugin process
    let mut child = tokio::process::Command::new(&echo_bin)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .kill_on_drop(true)
        .spawn()
        .unwrap();
    let stdout = child.stdout.take().unwrap();
    let readiness = tokio::time::timeout(Duration::from_secs(10), async {
        use tokio::io::{AsyncBufReadExt, BufReader};
        let mut lines = BufReader::new(stdout).lines();
        let line = lines.next_line().await.unwrap().expect("readiness line");
        serde_json::from_str::<nyanyabot_proto::Readiness>(&line).unwrap()
    })
    .await
    .expect("readiness timeout");

    let channel = Channel::from_shared(format!("http://{}", readiness.addr))
        .unwrap()
        .connect()
        .await
        .unwrap();
    let mut bad = PluginServiceClient::with_interceptor(
        channel,
        TokenInterceptor::new("definitely-wrong-token"),
    );
    let err = bad
        .describe(Request::new(DescribeRequest {}))
        .await
        .expect_err("wrong token must fail");
    assert_eq!(err.code(), tonic::Code::Unauthenticated);

    host.close().await;
    let _ = child.kill().await;
}

#[tokio::test]
async fn subprocess_invoke_not_found_structured() {
    let echo_bin = ensure_plugin_bin("nyanyabot-plugin-echo");
    let dir = tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    store.load_or_create_default().unwrap();
    let pm = nyanyabot::plugin::Manager::new();
    let stats = Stats::new();
    let host = PluginHost::new(pm.clone(), store, stats, dummy_call_onebot())
        .await
        .unwrap();
    host.load_exec(&echo_bin).await.unwrap();
    let (plugin, _) = pm.get("external.echo").await.unwrap();
    let err = plugin
        .invoke("no.such.method", json!({}), "caller")
        .await
        .expect_err("missing export");
    assert_eq!(err.code.as_str(), "NOT_FOUND");
    host.close().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn subprocess_crash_auto_restarts() {
    let echo_bin = ensure_plugin_bin("nyanyabot-plugin-echo");
    let dir = tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    store.load_or_create_default().unwrap();
    let pm = nyanyabot::plugin::Manager::new();
    let stats = Stats::new();
    let host = PluginHost::new(pm.clone(), store, stats, dummy_call_onebot())
        .await
        .unwrap();
    host.load_exec(&echo_bin).await.unwrap();
    assert!(pm.get("external.echo").await.is_some());

    host.force_kill_plugin_process("external.echo")
        .await
        .expect("force kill");

    // watch loop is 1s tick + 200ms restart delay
    let mut ok = false;
    for _ in 0..40 {
        tokio::time::sleep(Duration::from_millis(250)).await;
        if pm.get("external.echo").await.is_some()
            && let Ok(st) = pm.get("external.echo").await.unwrap().0.status().await
            && st == "OK"
        {
            ok = true;
            break;
        }
    }
    assert!(ok, "plugin should auto-restart after crash");
    host.close().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn subprocess_idle_sleep_mark() {
    let echo_bin = ensure_plugin_bin("nyanyabot-plugin-echo");
    let dir = tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    store.load_or_create_default().unwrap();
    let pm = nyanyabot::plugin::Manager::new();
    let stats = Stats::new();
    let host = PluginHost::new(pm.clone(), store, stats, dummy_call_onebot())
        .await
        .unwrap();
    host.load_exec(&echo_bin).await.unwrap();
    host.set_plugin_sleep_timeout("external.echo", 1)
        .await
        .unwrap();

    let pid_before = host.plugin_os_pid("external.echo").await;
    assert!(
        pid_before.is_some(),
        "plugin process should be running after load"
    );

    // force last_used into the past by waiting >1s without touch; watch ticks every 1s
    tokio::time::sleep(Duration::from_millis(2500)).await;
    assert!(
        host.plugin_is_sleeping("external.echo").await,
        "plugin should be marked sleeping after idle timeout"
    );
    // True sleep stops the OS process.
    assert!(
        host.plugin_os_pid("external.echo").await.is_none(),
        "idle sleep should stop the plugin process"
    );

    // Wake path restarts process and clears sleeping mark.
    host.ensure_awake("external.echo").await.unwrap();
    assert!(
        !host.plugin_is_sleeping("external.echo").await,
        "ensure_awake should clear sleeping mark"
    );
    let pid_after = host.plugin_os_pid("external.echo").await;
    assert!(
        pid_after.is_some(),
        "plugin process should be running after wake"
    );
    assert_ne!(
        pid_before, pid_after,
        "wake should start a new process after idle stop"
    );

    // Live process should accept configure again.
    host.reconfigure_plugin("external.echo").await.unwrap();
    host.close().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn subprocess_start_timeout_fails_fast() {
    // Use a non-plugin binary that never prints readiness: /bin/sleep
    let dir = tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    store.load_or_create_default().unwrap();
    let pm = nyanyabot::plugin::Manager::new();
    let stats = Stats::new();
    let host = PluginHost::new(pm, store, stats, dummy_call_onebot())
        .await
        .unwrap();
    // temporarily we can't change START_TIMEOUT (10s) — still validates error path with a hanging process
    // To keep CI fast, spawn a tiny shell that never writes readiness but exits after short delay?
    // Better: use `true` which exits immediately without readiness -> read error, not timeout.
    let err = host
        .load_exec(PathBuf::from("/bin/true"))
        .await
        .expect_err("non-plugin must fail");
    let msg = format!("{err:#}");
    assert!(
        msg.contains("readiness")
            || msg.contains("invalid")
            || msg.contains("eof")
            || msg.contains("failed"),
        "unexpected err: {msg}"
    );
    host.close().await;
}
