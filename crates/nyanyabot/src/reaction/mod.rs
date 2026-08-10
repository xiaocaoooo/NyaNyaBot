//! Per-command message emoji reactions via NapCat `set_msg_emoji_like`.

use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use serde_json::{Value, json};
use tracing::warn;

use crate::config::{CommandReactionConfig, Store};
use crate::pluginhost::host_service::CallOneBotFn;

#[derive(Debug, Clone)]
pub struct CommandReactionContext {
    pub plugin_id: String,
    pub listener_id: String,
    pub self_id: i64,
    pub message_id: Value,
    pub effective: bool,
    pub start_sent: bool,
}

#[derive(Clone, Default)]
pub struct CommandReactionTracker {
    inner: Arc<Mutex<HashMap<String, CommandReactionContext>>>,
}

impl CommandReactionTracker {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn begin(&self, trace_id: &str, ctx: CommandReactionContext) {
        if trace_id.trim().is_empty() {
            return;
        }
        self.inner
            .lock()
            .expect("reaction lock")
            .insert(trace_id.to_string(), ctx);
    }

    pub fn mark_effective(&self, trace_id: &str) -> bool {
        let mut map = self.inner.lock().expect("reaction lock");
        if let Some(ctx) = map.get_mut(trace_id) {
            ctx.effective = true;
            return true;
        }
        false
    }

    pub fn take(&self, trace_id: &str) -> Option<CommandReactionContext> {
        self.inner.lock().expect("reaction lock").remove(trace_id)
    }

    pub fn with_mut<R>(&self, trace_id: &str, f: impl FnOnce(&mut CommandReactionContext) -> R) -> Option<R> {
        let mut map = self.inner.lock().expect("reaction lock");
        map.get_mut(trace_id).map(f)
    }
}

pub fn reaction_config(
    store: &Store,
    plugin_id: &str,
    listener_id: &str,
) -> Option<CommandReactionConfig> {
    let cfg = store.get();
    cfg.plugin_controls
        .get(plugin_id)
        .and_then(|c| c.command_reactions.get(listener_id))
        .cloned()
        .filter(|c| c.enabled)
}

pub async fn maybe_send_start(
    call_onebot: &CallOneBotFn,
    store: &Store,
    tracker: &CommandReactionTracker,
    trace_id: &str,
) {
    let Some(mut snapshot) = tracker.with_mut(trace_id, |ctx| ctx.clone()) else {
        return;
    };
    if snapshot.start_sent {
        return;
    }
    let Some(rcfg) = reaction_config(store, &snapshot.plugin_id, &snapshot.listener_id) else {
        return;
    };
    if !rcfg.start_enabled || rcfg.start_emoji_id.trim().is_empty() {
        return;
    }
    if send_emoji_like(
        call_onebot,
        snapshot.self_id,
        &snapshot.message_id,
        rcfg.start_emoji_id.trim(),
        trace_id,
    )
    .await
    {
        let _ = tracker.with_mut(trace_id, |ctx| {
            ctx.start_sent = true;
            snapshot.start_sent = true;
        });
    }
}

pub async fn finish_reactions(
    call_onebot: &CallOneBotFn,
    store: &Store,
    tracker: &CommandReactionTracker,
    trace_id: &str,
    handled_flag: bool,
) {
    let Some(mut ctx) = tracker.take(trace_id) else {
        return;
    };
    if handled_flag {
        ctx.effective = true;
    }
    if !ctx.effective {
        return;
    }
    let Some(rcfg) = reaction_config(store, &ctx.plugin_id, &ctx.listener_id) else {
        return;
    };

    // Ensure start was attempted if enabled (plugins that only set handled=true at end).
    if rcfg.start_enabled && !rcfg.start_emoji_id.trim().is_empty() && !ctx.start_sent {
        let _ = send_emoji_like(
            call_onebot,
            ctx.self_id,
            &ctx.message_id,
            rcfg.start_emoji_id.trim(),
            trace_id,
        )
        .await;
        ctx.start_sent = true;
    }

    if rcfg.end_enabled && !rcfg.end_emoji_id.trim().is_empty() {
        let _ = send_emoji_like(
            call_onebot,
            ctx.self_id,
            &ctx.message_id,
            rcfg.end_emoji_id.trim(),
            trace_id,
        )
        .await;
    }
}

async fn send_emoji_like(
    call_onebot: &CallOneBotFn,
    self_id: i64,
    message_id: &Value,
    emoji_id: &str,
    trace_id: &str,
) -> bool {
    if matches!(message_id, Value::Null)
        || message_id.as_str().map(|s| s.trim().is_empty()).unwrap_or(false)
    {
        warn!(trace_id = %trace_id, "skip set_msg_emoji_like: empty message_id");
        return false;
    }
    let params = json!({
        "message_id": message_id,
        "emoji_id": emoji_id,
        "set": true,
    });
    match (call_onebot)(
        "set_msg_emoji_like".to_string(),
        params,
        self_id,
        trace_id.to_string(),
    )
    .await
    {
        Ok(resp) => {
            if resp.retcode != 0 && resp.status != "ok" {
                warn!(
                    trace_id = %trace_id,
                    retcode = resp.retcode,
                    status = %resp.status,
                    msg = %resp.msg,
                    "set_msg_emoji_like failed"
                );
                return false;
            }
            true
        }
        Err(err) => {
            warn!(trace_id = %trace_id, error = %err, "set_msg_emoji_like error");
            false
        }
    }
}

/// Extract OneBot message id for emoji like (prefer message_id, else real_seq).
pub fn extract_message_id(event: &Value) -> Value {
    if let Some(v) = event.get("message_id") {
        if let Some(n) = v.as_i64() {
            return json!(n);
        }
        if let Some(n) = v.as_u64() {
            return json!(n);
        }
        if let Some(s) = v.as_str() {
            let s = s.trim();
            if !s.is_empty() {
                if let Ok(n) = s.parse::<i64>() {
                    return json!(n);
                }
                return json!(s);
            }
        }
    }
    match event.get("real_seq") {
        Some(Value::String(s)) => {
            let s = s.trim();
            if s.is_empty() {
                Value::Null
            } else if let Ok(n) = s.parse::<i64>() {
                json!(n)
            } else {
                json!(s)
            }
        }
        Some(Value::Number(n)) => Value::Number(n.clone()),
        _ => Value::Null,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn extract_prefers_message_id() {
        let ev = json!({"message_id": 42, "real_seq": "99"});
        assert_eq!(extract_message_id(&ev), json!(42));
        let ev = json!({"real_seq": "99"});
        assert_eq!(extract_message_id(&ev), json!(99));
    }

    #[test]
    fn tracker_effective_and_take() {
        let t = CommandReactionTracker::new();
        t.begin(
            "t1",
            CommandReactionContext {
                plugin_id: "p".into(),
                listener_id: "c".into(),
                self_id: 1,
                message_id: json!(1),
                effective: false,
                start_sent: false,
            },
        );
        assert!(t.mark_effective("t1"));
        let ctx = t.take("t1").unwrap();
        assert!(ctx.effective);
        assert!(t.take("t1").is_none());
    }
}
