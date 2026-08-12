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
        "message_type":"group",
        "self_id":1,
        "user_id":2,
        "group_id":99,
        "real_seq":"pg-test-seq-1",
        "raw_message":"hello-pg-test",
        "sender":{"nickname":"n"},
        "message":[{"type":"text","data":{"text":"hello-pg-test"}}]
    }));
    tokio::time::sleep(Duration::from_millis(800)).await;
    let _ = chat.stop().await;

    let pool = sqlx::postgres::PgPoolOptions::new()
        .max_connections(1)
        .connect(&uri)
        .await
        .unwrap();
    let chat_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM group_message_logs WHERE real_seq = $1 AND group_id = 99",
    )
    .bind("pg-test-seq-1")
    .fetch_one(&pool)
    .await
    .unwrap_or(0);
    assert!(chat_count >= 1, "missing group_message_logs row");

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
            listener_type: "command".into(),
            self_id: 1,
            user_id: 2,
            group_id: 0,
            message_id: 0,
            message_seq: String::new(),
            success: true,
            error_message: String::new(),
            duration_ms: 3,
            trigger_data: json!({"ok":true}),
            triggered_at: chrono::Utc::now(),
        })
        .await;
    tokio::time::sleep(Duration::from_millis(1200)).await;
    let stats = trigger.stats().await;
    assert!(
        stats.total_count >= 1,
        "trigger total={}",
        stats.total_count
    );
    let (items, total) = trigger
        .query_logs(&nyanyabot::triggerlog::PluginTriggerLogQuery {
            trace_id: Some("t-pg".into()),
            ..Default::default()
        })
        .await;
    assert!(total >= 1, "missing plugin_trigger_logs total");
    assert!(
        items.iter().any(|v| v.trace_id == "t-pg"),
        "missing trigger row: {items:?}"
    );
    let table_count: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM plugin_trigger_logs WHERE trace_id = 't-pg'")
            .fetch_one(&pool)
            .await
            .unwrap_or(0);
    assert!(table_count >= 1, "plugin_trigger_logs missing t-pg");
    let _ = trigger.stop().await;
}
