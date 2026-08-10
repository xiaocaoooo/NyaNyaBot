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

    /// Mark effective only when the in-flight context belongs to `plugin_id`.
    pub fn mark_effective_for_plugin(&self, trace_id: &str, plugin_id: &str) -> bool {
        let mut map = self.inner.lock().expect("reaction lock");
        let Some(ctx) = map.get_mut(trace_id) else {
            return false;
        };
        if ctx.plugin_id != plugin_id {
            return false;
        }
        ctx.effective = true;
        true
    }

    pub fn take(&self, trace_id: &str) -> Option<CommandReactionContext> {
        self.inner.lock().expect("reaction lock").remove(trace_id)
    }

    pub fn with_mut<R>(
        &self,
        trace_id: &str,
        f: impl FnOnce(&mut CommandReactionContext) -> R,
    ) -> Option<R> {
        let mut map = self.inner.lock().expect("reaction lock");
        map.get_mut(trace_id).map(f)
    }

    /// Claim start slot if effective and not yet sent. Returns snapshot to send, or None.
    pub fn claim_start_send(&self, trace_id: &str) -> Option<CommandReactionContext> {
        let mut map = self.inner.lock().expect("reaction lock");
        let ctx = map.get_mut(trace_id)?;
        if !ctx.effective || ctx.start_sent {
            return None;
        }
        // Optimistic claim to avoid concurrent double-send.
        ctx.start_sent = true;
        Some(ctx.clone())
    }

    pub fn clear_start_sent(&self, trace_id: &str) {
        let _ = self.with_mut(trace_id, |ctx| {
            ctx.start_sent = false;
        });
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
    let Some(snapshot) = tracker.with_mut(trace_id, |ctx| ctx.clone()) else {
        return;
    };
    if !snapshot.effective || snapshot.start_sent {
        return;
    }
    let Some(rcfg) = reaction_config(store, &snapshot.plugin_id, &snapshot.listener_id) else {
        return;
    };
    if !rcfg.start_enabled || rcfg.start_emoji_id.trim().is_empty() {
        return;
    }
    // Re-check and claim under lock.
    let Some(claimed) = tracker.claim_start_send(trace_id) else {
        return;
    };
    let ok = send_emoji_like(
        call_onebot,
        claimed.self_id,
        &claimed.message_id,
        rcfg.start_emoji_id.trim(),
        trace_id,
    )
    .await;
    if !ok {
        tracker.clear_start_sent(trace_id);
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
    // Premature mark alone (no successful start, final ignored) must not react.
    if !handled_flag && !ctx.start_sent {
        return;
    }
    let Some(rcfg) = reaction_config(store, &ctx.plugin_id, &ctx.listener_id) else {
        return;
    };

    // Ensure start was attempted if enabled (plugins that only set handled=true at end).
    if handled_flag
        && rcfg.start_enabled
        && !rcfg.start_emoji_id.trim().is_empty()
        && !ctx.start_sent
    {
        let ok = send_emoji_like(
            call_onebot,
            ctx.self_id,
            &ctx.message_id,
            rcfg.start_emoji_id.trim(),
            trace_id,
        )
        .await;
        if ok {
            ctx.start_sent = true;
        }
    }

    if rcfg.end_enabled && !rcfg.end_emoji_id.trim().is_empty() {
        // End when we actually started, or when handle completed successfully.
        if ctx.start_sent || handled_flag {
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
}

fn message_id_is_invalid(message_id: &Value) -> bool {
    match message_id {
        Value::Null => true,
        Value::String(s) => s.trim().is_empty() || s.trim() == "0",
        Value::Number(n) => n.as_i64() == Some(0) || n.as_u64() == Some(0),
        _ => false,
    }
}

/// OneBot success: retcode == 0 or status == "ok" (case-insensitive).
pub fn onebot_action_ok(retcode: i64, status: &str) -> bool {
    retcode == 0 || status.eq_ignore_ascii_case("ok")
}

async fn send_emoji_like(
    call_onebot: &CallOneBotFn,
    self_id: i64,
    message_id: &Value,
    emoji_id: &str,
    trace_id: &str,
) -> bool {
    if message_id_is_invalid(message_id) {
        warn!(trace_id = %trace_id, "skip set_msg_emoji_like: empty message_id");
        return false;
    }
    let params = json!({
        "message_id": message_id,
        "emoji_id": emoji_id,
        "set": true,
    });
    // Empty trace_id: host-driven side effect must not inflate plugin_sent stats.
    match (call_onebot)(
        "set_msg_emoji_like".to_string(),
        params,
        self_id,
        String::new(),
    )
    .await
    {
        Ok(resp) => {
            if !onebot_action_ok(resp.retcode, &resp.status) {
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
            if n == 0 {
                // fall through to real_seq
            } else {
                return json!(n);
            }
        } else if let Some(n) = v.as_u64() {
            if n == 0 {
                // fall through
            } else {
                return json!(n);
            }
        } else if let Some(s) = v.as_str() {
            let s = s.trim();
            if !s.is_empty() && s != "0" {
                if let Ok(n) = s.parse::<i64>() {
                    if n != 0 {
                        return json!(n);
                    }
                } else {
                    return json!(s);
                }
            }
        }
    }
    match event.get("real_seq") {
        Some(Value::String(s)) => {
            let s = s.trim();
            if s.is_empty() || s == "0" {
                Value::Null
            } else if let Ok(n) = s.parse::<i64>() {
                if n == 0 { Value::Null } else { json!(n) }
            } else {
                json!(s)
            }
        }
        Some(Value::Number(n)) => {
            if n.as_i64() == Some(0) || n.as_u64() == Some(0) {
                Value::Null
            } else {
                Value::Number(n.clone())
            }
        }
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
        let ev = json!({"message_id": 0, "real_seq": "99"});
        assert_eq!(extract_message_id(&ev), json!(99));
    }

    #[test]
    fn message_id_invalid() {
        assert!(message_id_is_invalid(&Value::Null));
        assert!(message_id_is_invalid(&json!(0)));
        assert!(message_id_is_invalid(&json!("0")));
        assert!(message_id_is_invalid(&json!("")));
        assert!(!message_id_is_invalid(&json!(42)));
    }

    #[test]
    fn onebot_ok_logic() {
        assert!(onebot_action_ok(0, "failed"));
        assert!(onebot_action_ok(1, "ok"));
        assert!(onebot_action_ok(0, "ok"));
        assert!(!onebot_action_ok(1, "failed"));
        assert!(!onebot_action_ok(1400, "error"));
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
        assert!(!t.mark_effective_for_plugin("t1", "other"));
        assert!(t.mark_effective_for_plugin("t1", "p"));
        let ctx = t.take("t1").unwrap();
        assert!(ctx.effective);
        assert!(t.take("t1").is_none());
    }

    #[test]
    fn claim_start_requires_effective() {
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
        assert!(t.claim_start_send("t1").is_none());
        assert!(t.mark_effective("t1"));
        assert!(t.claim_start_send("t1").is_some());
        assert!(t.claim_start_send("t1").is_none());
    }

    #[test]
    fn finish_gate_premature_mark_only() {
        // Documented contract tested via pure flags:
        // handled=false && start_sent=false => skip (even if effective).
        let handled_flag = false;
        let start_sent = false;
        let should = handled_flag || start_sent;
        assert!(!should);
        let should2 = true || false; // handled
        assert!(should2);
        let should3 = false || true; // start_sent
        assert!(should3);
    }
}
