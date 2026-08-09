use std::time::Duration;

use nyanyabot::chatlog::Recorder as ChatRecorder;
use nyanyabot::config::{ChatLogConfig, ChatLogQueueConfig, TriggerLogConfig};
use nyanyabot::triggerlog::{Recorder as TriggerRecorder, TriggerRecord};
use serde_json::json;

fn pg_uri() -> Option<String> {
    std::env::var("NYANYABOT_TEST_DATABASE_URI")
        .ok()
        .or_else(|| Some("postgres://amiabot:amiabot_password@127.0.0.1:5432/amiabot".to_string()))
}

async fn can_connect(uri: &str) -> bool {
    sqlx::postgres::PgPoolOptions::new()
        .max_connections(1)
        .acquire_timeout(Duration::from_secs(2))
        .connect(uri)
        .await
        .is_ok()
}

#[tokio::test]
async fn chat_and_trigger_log_postgres() {
    let Some(uri) = pg_uri() else {
        return;
    };
    if !can_connect(&uri).await {
        eprintln!("skip postgres_logs: cannot connect to {uri}");
        return;
    }

    let chat = ChatRecorder::new(&ChatLogConfig {
        database_uri: uri.clone(),
        queue: ChatLogQueueConfig { size: 100 },
    });
    chat.start().await;
    chat.handle_event(&json!({
        "post_type":"message",
        "message_type":"private",
        "self_id":1,
        "user_id":2,
        "message_id":99,
        "raw_message":"hello-pg-test",
        "message":[{"type":"text","data":{"text":"hello-pg-test"}}]
    }));
    tokio::time::sleep(Duration::from_millis(300)).await;
    let _ = chat.stop().await;

    let trigger = TriggerRecorder::new(&TriggerLogConfig {
        enabled: true,
        database_uri: uri.clone(),
        queue_size: 100,
        batch_size: 10,
        batch_interval: "1s".into(),
    });
    trigger.start();
    trigger.start_async().await;
    trigger
        .record_async(TriggerRecord {
            trace_id: "t-pg".into(),
            plugin_id: "external.echo".into(),
            listener_id: "cmd.echo".into(),
            trace_type: "command".into(),
            self_id: 1,
            user_id: 2,
            group_id: 0,
            success: true,
            error: String::new(),
            duration_ms: 3,
            data: json!({"ok":true}),
        })
        .await;
    tokio::time::sleep(Duration::from_millis(1200)).await;
    let stats = trigger.stats().await;
    assert!(stats.total >= 1, "trigger total={}", stats.total);
    let items = trigger.list_recent(10).await;
    assert!(
        items
            .iter()
            .any(|v| v.get("trace_id").and_then(|x| x.as_str()) == Some("t-pg")),
        "missing trigger row: {items:?}"
    );
    let _ = trigger.stop().await;
}
