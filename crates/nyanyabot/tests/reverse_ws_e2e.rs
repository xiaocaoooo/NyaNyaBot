use std::path::PathBuf;
use std::process::Command;
use std::sync::Arc;
use std::time::Duration;

use futures_util::{SinkExt, StreamExt};
use nyanyabot::config::Store;
use nyanyabot::dispatch::Dispatcher;
use nyanyabot::onebot::reversews::Server as ReverseWsServer;
use nyanyabot::pluginhost::PluginHost;
use nyanyabot::stats::Stats;
use serde_json::{Value, json};
use tempfile::tempdir;
use tokio_tungstenite::tungstenite::Message;

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
        assert!(
            Command::new("cargo")
                .args(["build", "-p", name])
                .current_dir(&root)
                .status()
                .unwrap()
                .success()
        );
    }
    bin
}

async fn fake_onebot_session(addr: std::net::SocketAddr, self_id: i64, push_event: Option<Value>) {
    let url = format!("ws://{addr}/");
    let (ws, _) = tokio_tungstenite::connect_async(&url)
        .await
        .expect("connect reverse ws");
    let (mut sink, mut stream) = ws.split();
    let mut pushed = false;
    while let Some(Ok(msg)) = stream.next().await {
        let text = match msg {
            Message::Text(t) => t.to_string(),
            Message::Binary(b) => String::from_utf8_lossy(&b).to_string(),
            Message::Close(_) => break,
            _ => continue,
        };
        let req: Value = match serde_json::from_str(&text) {
            Ok(v) => v,
            Err(_) => continue,
        };
        let action = req.get("action").and_then(|v| v.as_str()).unwrap_or("");
        let echo = req.get("echo").cloned().unwrap_or(Value::Null);
        let data = match action {
            "get_login_info" => json!({"user_id": self_id, "nickname": "FakeBot"}),
            "get_group_list" => json!([]),
            "send_private_msg" | "send_group_msg" => json!({"message_id": 1}),
            _ => json!({}),
        };
        let resp = json!({"status":"ok","retcode":0,"data":data,"echo":echo});
        if sink
            .send(Message::Text(resp.to_string().into()))
            .await
            .is_err()
        {
            break;
        }
        if action == "get_group_list"
            && !pushed
            && let Some(event) = push_event.clone()
        {
            pushed = true;
            let _ = sink.send(Message::Text(event.to_string().into())).await;
        }
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn reverse_ws_login_event_dispatch_and_hot_reload() {
    let echo_bin = ensure_plugin_bin("nyanyabot-plugin-echo");
    let dir = tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    store.load_or_create_default().unwrap();

    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    drop(listener);
    store
        .update(|cfg| {
            cfg.onebot.reverse_ws.listen_addr = addr.to_string();
            cfg.plugins
                .insert("external.echo".into(), json!({"prefix": "ws: "}));
        })
        .unwrap();

    let pm = nyanyabot::plugin::Manager::new();
    let stats = Stats::new();
    let recorded = Arc::new(tokio::sync::Mutex::new(Vec::<(String, Value)>::new()));
    let recorded2 = recorded.clone();
    let call_onebot: nyanyabot::pluginhost::host_service::CallOneBotFn = Arc::new(
        move |action: String, params: Value, _self_id: i64, _trace: String| {
            let recorded2 = recorded2.clone();
            Box::pin(async move {
                recorded2.lock().await.push((action, params));
                Ok(nyanyabot::onebot::ob11::ApiResponse {
                    status: "ok".into(),
                    retcode: 0,
                    data: json!({"message_id": 7}),
                    ..Default::default()
                })
            })
        },
    );

    let host = PluginHost::new(pm.clone(), store.clone(), stats.clone(), call_onebot)
        .await
        .unwrap();
    host.load_exec(&echo_bin).await.unwrap();

    let onebot = ReverseWsServer::new(store.clone());
    let dispatcher = Dispatcher::new(pm.clone(), store.clone(), stats.clone(), host.clone(), None);
    let disp = dispatcher.clone();
    onebot.set_handler(move |event: Value| {
        disp.dispatch(event);
    });

    let ob = onebot.clone();
    let ob_task = tokio::spawn(async move {
        let _ = ob.start().await;
    });
    tokio::time::sleep(Duration::from_millis(150)).await;

    let event = json!({
        "post_type":"message",
        "message_type":"private",
        "user_id": 7,
        "self_id": 4242,
        "raw_message": "echo hello-ws",
        "message": [{"type":"text","data":{"text":"echo hello-ws"}}],
        "message_id": 99
    });
    let client = tokio::spawn(fake_onebot_session(addr, 4242, Some(event)));

    let mut bot_ok = false;
    for _ in 0..50 {
        if onebot.get_bot_ids().contains(&4242) {
            bot_ok = true;
            break;
        }
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    assert!(bot_ok, "fake bot should register via get_login_info");

    let mut got = false;
    for _ in 0..80 {
        let calls = recorded.lock().await;
        if calls.iter().any(|(a, p)| {
            a == "send_private_msg" && p.get("user_id").and_then(|v| v.as_i64()) == Some(7)
        }) {
            got = true;
            break;
        }
        drop(calls);
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    assert!(
        got,
        "echo plugin should call send_private_msg after reverse-ws event; got {:?}",
        recorded.lock().await
    );

    store
        .update(|cfg| {
            cfg.plugins
                .insert("external.echo".into(), json!({"prefix": "hotws: "}));
        })
        .unwrap();
    host.reconfigure_plugin("external.echo").await.unwrap();

    host.close().await;
    onebot.shutdown().await;
    let _ = tokio::time::timeout(Duration::from_secs(1), ob_task).await;
    client.abort();
}
