use std::sync::Arc;

use nyanyabot_proto::CommandMatch;
use regex::Regex;
use serde_json::{Value, json};
use tracing::{debug, info, warn};

use crate::config::Store;
use crate::dedup::Deduper;
use crate::onebot::reversews::Server as ReverseWsServer;
use crate::plugin::Manager;
use crate::pluginhost::PluginHost;
use crate::stats::Stats;
use crate::util::apply_overrides;

pub struct Dispatcher {
    pm: Arc<Manager>,
    store: Arc<Store>,
    stats: Arc<Stats>,
    host: Arc<PluginHost>,
    deduper: Option<Arc<dyn Deduper>>,
    reverse_ws: Option<Arc<ReverseWsServer>>,
}

impl Dispatcher {
    pub fn new(
        pm: Arc<Manager>,
        store: Arc<Store>,
        stats: Arc<Stats>,
        host: Arc<PluginHost>,
        deduper: Option<Arc<dyn Deduper>>,
        reverse_ws: Option<Arc<ReverseWsServer>>,
    ) -> Arc<Self> {
        Arc::new(Self {
            pm,
            store,
            stats,
            host,
            deduper,
            reverse_ws,
        })
    }

    pub fn dispatch(self: &Arc<Self>, raw: Value) {
        let this = Arc::clone(self);
        tokio::spawn(async move {
            this.dispatch_inner(raw).await;
        });
    }

    async fn dispatch_inner(&self, raw: Value) {
        let cfg = self.store.get();
        let post_type = get_string(&raw, "post_type");
        if post_type.is_empty() {
            return;
        }

        let group_id = get_i64(&raw, "group_id");
        let user_id = get_i64(&raw, "user_id");
        let message_seq = get_i64(&raw, "real_seq");
        let message_type = get_string(&raw, "message_type");
        let content = extract_content(&raw);

        if cfg.is_message_dedup_enabled()
            && message_type == "group"
            && group_id > 0
            && message_seq > 0
            && let Some(deduper) = &self.deduper
            && !deduper.try_mark_processed(group_id, message_seq)
        {
            self.stats.inc_dedup();
            debug!(group_id, message_seq, "duplicate message skipped");
            return;
        }

        if post_type == "message" && message_type == "group" {
            let group_name = resolve_group_name(&raw, self.reverse_ws.as_deref(), group_id);
            let user_name = extract_user_display_name(&raw);
            let message_text = resolve_message_text(&raw, &content);
            let real_seq = get_real_seq_string(&raw);
            info!(
                group_name = %group_name,
                group_id,
                user_name = %user_name,
                user_id,
                message = %message_text,
                real_seq = %real_seq,
                "message received"
            );
        }

        let (event_key, event_key_full) = compute_event_keys(&raw);
        let entries = self.pm.entries().await;

        // Event listeners (all post types that match)
        let event_raw = inject_content_field(&raw, &content);
        for (pid, desc, _plugin) in &entries {
            if !cfg.is_plugin_enabled(pid) {
                continue;
            }
            for l in &desc.events {
                if !cfg.is_event_enabled(pid, &l.id) {
                    continue;
                }
                if !cfg.is_allowed(pid, &l.id, false, user_id, group_id) {
                    continue;
                }
                if !match_event(&l.event, &event_key, &event_key_full) {
                    continue;
                }
                // Only inject content for message events (Go: postType == "message")
                let payload = if post_type == "message" {
                    event_raw.clone()
                } else {
                    raw.clone()
                };
                let trace_id = self.host.generate_trace_id();
                let real_seq = get_real_seq_string(&raw);
                let message_id = parse_i64_loose(&real_seq);
                self.host.begin_trace(
                    &trace_id,
                    pid,
                    &l.id,
                    "event",
                    json!({
                        "event_type": event_key,
                        "sub_type": event_key_full,
                        "user_id": user_id,
                        "group_id": group_id,
                        "self_id": get_i64(&raw, "self_id"),
                        "message_id": message_id,
                        "message_seq": real_seq,
                        "raw_message": get_string(&raw, "raw_message"),
                    }),
                );
                info!(plugin_id = %pid, event_id = %l.id, event_type = %event_key, "event dispatched");
                // ensure_awake may restart and re-register a fresh Arc; never reuse the
                // snapshotted `plugin` from entries() after wake.
                let handle_res = match self.host.ensure_awake_plugin(pid).await {
                    Ok(live) => live.handle(&l.id, payload, None, &trace_id).await,
                    Err(err) => {
                        warn!(plugin_id = %pid, error = %err, "ensure_awake failed");
                        Err(nyanyabot_proto::StructuredError::internal(err.to_string()))
                    }
                };
                let (ok, err_msg) = match &handle_res {
                    Ok(_) => (true, String::new()),
                    Err(err) => {
                        warn!(plugin_id = %pid, error = %err, "event handle failed");
                        (false, err.to_string())
                    }
                };
                self.host.end_trace(&trace_id, ok, err_msg);
            }
        }

        // Command listeners: message only (Go does not accept message_sent here)
        if post_type != "message" {
            return;
        }

        self.stats.inc_recv();

        // Group-chat only for commands
        if message_type != "group" {
            self.stats.inc_filtered_non_group();
            debug!(%message_type, user_id, "filtered non-group message for commands");
            return;
        }

        // Filter messages from connected bots (self/other bots)
        if let Some(ws) = &self.reverse_ws {
            let bot_ids = ws.get_bot_ids();
            if bot_ids.contains(&user_id) {
                self.stats.inc_filtered_self();
                debug!(user_id, "filtered bot self/other-bot message for commands");
                return;
            }
        }

        let raw_message = get_string(&raw, "raw_message");
        let command_raw = inject_content_field(&raw, &content);

        for (pid, desc, _plugin) in &entries {
            if !cfg.is_plugin_enabled(pid) {
                continue;
            }
            let prefix_pattern = cfg.effective_command_prefix(pid);
            for l in &desc.commands {
                if !cfg.is_command_enabled(pid, &l.id) {
                    continue;
                }
                if !cfg.is_allowed(pid, &l.id, true, user_id, group_id) {
                    continue;
                }
                let input_src = if l.match_raw {
                    raw_message.clone()
                } else {
                    content.clone()
                };
                if input_src.is_empty() {
                    continue;
                }
                let Some(mut input) = strip_message_prefix(&input_src, &prefix_pattern) else {
                    debug!(
                        plugin_id = %pid,
                        command_id = %l.id,
                        prefix = %prefix_pattern,
                        "prefix not matched, skipping command"
                    );
                    continue;
                };
                let overrides = cfg.command_overrides(pid, &l.id);
                if !overrides.is_empty() {
                    input = apply_overrides(&input, &overrides);
                }
                let Ok(re) = Regex::new(&l.pattern) else {
                    warn!(plugin_id = %pid, command_id = %l.id, "invalid command regex");
                    continue;
                };
                let Some(caps) = re.captures(&input) else {
                    continue;
                };
                let full = caps
                    .get(0)
                    .map(|m| m.as_str().to_string())
                    .unwrap_or_default();
                let mut groups = Vec::new();
                for i in 1..caps.len() {
                    groups.push(
                        caps.get(i)
                            .map(|m| m.as_str().to_string())
                            .unwrap_or_default(),
                    );
                }
                // Named groups preferred when present
                for name in re.capture_names().flatten() {
                    if let Some(m) = caps.name(name) {
                        // Keep positional groups; named also available via same list order in Go
                        let _ = m;
                    }
                }
                let match_data = CommandMatch { full, groups };
                let trace_id = self.host.generate_trace_id();
                let real_seq = get_real_seq_string(&raw);
                let message_id = parse_i64_loose(&real_seq);
                let self_id = get_i64(&raw, "self_id");
                self.host.begin_trace(
                    &trace_id,
                    pid,
                    &l.id,
                    "command",
                    json!({
                        "group_id": group_id,
                        "user_id": user_id,
                        "self_id": self_id,
                        "raw_message": raw_message,
                        "seq": real_seq,
                        "message_id": message_id,
                        "message_seq": real_seq,
                        "content": content,
                        "pattern": &l.pattern,
                        "match_full": match_data.full.clone(),
                        "match_groups": match_data.groups.clone(),
                    }),
                );
                // Register emoji-reaction context (no-op unless command_reactions enabled).
                self.host.command_reactions().begin(
                    &trace_id,
                    crate::reaction::CommandReactionContext {
                        plugin_id: pid.clone(),
                        listener_id: l.id.clone(),
                        self_id,
                        message_id: crate::reaction::extract_message_id(&raw),
                        effective: false,
                        start_sent: false,
                    },
                );
                info!(plugin_id = %pid, command_id = %l.id, "command matched");
                // ensure_awake may restart and re-register a fresh Arc; never reuse the
                // snapshotted `plugin` from entries() after wake.
                let handle_res = match self.host.ensure_awake_plugin(pid).await {
                    Ok(live) => {
                        live.handle(&l.id, command_raw.clone(), Some(match_data), &trace_id)
                            .await
                    }
                    Err(err) => {
                        warn!(plugin_id = %pid, error = %err, "ensure_awake failed");
                        Err(nyanyabot_proto::StructuredError::internal(err.to_string()))
                    }
                };
                let (ok, err_msg, handled_flag) = match &handle_res {
                    Ok(res) => (true, String::new(), res.handled),
                    Err(err) => {
                        warn!(plugin_id = %pid, error = %err, "command handle failed");
                        (false, err.to_string(), false)
                    }
                };
                // Host-side end detection for command reactions.
                crate::reaction::finish_reactions(
                    &self.host.call_onebot_fn(),
                    &self.host.store(),
                    self.host.command_reactions(),
                    &trace_id,
                    handled_flag,
                )
                .await;
                self.host.end_trace(&trace_id, ok, err_msg);
            }
        }
    }
}

/// Align with Go getString: stringify numbers and other JSON values.
fn get_string(v: &Value, key: &str) -> String {
    match v.get(key) {
        Some(Value::String(s)) => s.clone(),
        Some(Value::Number(n)) => n.to_string(),
        Some(Value::Bool(b)) => b.to_string(),
        Some(Value::Null) | None => String::new(),
        Some(other) => other.to_string(),
    }
}

fn get_i64(v: &Value, key: &str) -> i64 {
    v.get(key).map(value_as_i64).unwrap_or(0)
}

fn value_as_i64(x: &Value) -> i64 {
    x.as_i64()
        .or_else(|| x.as_u64().map(|n| n as i64))
        .or_else(|| x.as_f64().map(|n| n as i64))
        .or_else(|| x.as_str().map(parse_i64_loose))
        .unwrap_or(0)
}

fn parse_i64_loose(s: &str) -> i64 {
    s.trim().parse().unwrap_or(0)
}

/// Prefer string form of real_seq (Go getString); numbers stringify.
fn get_real_seq_string(v: &Value) -> String {
    match v.get("real_seq") {
        Some(Value::String(s)) => s.clone(),
        Some(Value::Number(n)) => n.to_string(),
        Some(other) => {
            let n = value_as_i64(other);
            if n == 0 && !other.is_number() {
                String::new()
            } else {
                n.to_string()
            }
        }
        None => String::new(),
    }
}

/// Align with Go deriveContent: use `message` only (string or text segments).
/// Do NOT prefer raw_message; MatchRaw commands use raw_message separately.
fn extract_content(raw: &Value) -> String {
    match raw.get("message") {
        Some(Value::String(s)) => s.clone(),
        Some(Value::Array(arr)) => {
            let mut out = String::new();
            for seg in arr {
                if seg.get("type").and_then(|t| t.as_str()) == Some("text")
                    && let Some(t) = seg.pointer("/data/text").and_then(|v| v.as_str())
                {
                    out.push_str(t);
                }
            }
            out
        }
        _ => String::new(),
    }
}

fn inject_content_field(raw: &Value, content: &str) -> Value {
    let mut obj = raw.clone();
    if let Some(map) = obj.as_object_mut() {
        map.insert("content".into(), Value::String(content.to_string()));
    }
    obj
}

/// Strip message prefix. Returns None if pattern invalid or does not match (Go skip behavior).
/// Align with Go stripMessagePrefix:
/// - match must start at index 0
/// - prefer non-empty named `content` group
/// - otherwise return remainder after full match
fn strip_message_prefix(input: &str, pattern: &str) -> Option<String> {
    let pattern = pattern.trim();
    if pattern.is_empty() {
        return Some(input.to_string());
    }
    let re = Regex::new(pattern).ok()?;
    let caps = re.captures(input)?;
    let full = caps.get(0)?;
    // Go: loc[0] != 0 => not matched for prefix purposes
    if full.start() != 0 {
        return None;
    }
    if let Some(c) = caps.name("content") {
        let s = c.as_str();
        if !s.is_empty() {
            return Some(s.to_string());
        }
    }
    // Remainder after the full match (Go TrimPrefix / msg[loc[1]:]).
    Some(input[full.end()..].to_string())
}

fn extract_user_display_name(raw: &Value) -> String {
    let Some(sender) = raw.get("sender").and_then(|v| v.as_object()) else {
        return String::new();
    };
    if let Some(card) = sender.get("card").and_then(|v| v.as_str()) {
        let card = card.trim();
        if !card.is_empty() {
            return card.to_string();
        }
    }
    sender
        .get("nickname")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string())
        .unwrap_or_default()
}

fn resolve_message_text(raw: &Value, content: &str) -> String {
    let raw_message = get_string(raw, "raw_message");
    if !raw_message.is_empty() {
        raw_message
    } else {
        content.to_string()
    }
}

fn resolve_group_name(raw: &Value, reverse_ws: Option<&ReverseWsServer>, group_id: i64) -> String {
    let from_event = get_string(raw, "group_name");
    if !from_event.trim().is_empty() {
        return from_event;
    }
    reverse_ws
        .and_then(|ws| ws.lookup_group_name(group_id))
        .unwrap_or_default()
}

/// Align with Go computeEventKeys / designs §5:
/// - key = post_type
/// - full = post_type + detail type only (no sub_type)
/// - message_sent is not given a message_type suffix in Go
fn compute_event_keys(raw: &Value) -> (String, String) {
    let post = get_string(raw, "post_type");
    let detail = match post.as_str() {
        "message" => get_string(raw, "message_type"),
        "notice" => get_string(raw, "notice_type"),
        "request" => get_string(raw, "request_type"),
        "meta_event" => get_string(raw, "meta_event_type"),
        _ => String::new(),
    };
    let full = if detail.is_empty() {
        String::new()
    } else {
        format!("{post}.{detail}")
    };
    (post, full)
}

/// Align with Go matchEvent / designs §5:
/// - empty selector never matches
/// - selector with '.' must equal full key exactly
/// - selector without '.' must equal post_type key exactly
fn match_event(pattern: &str, key: &str, full: &str) -> bool {
    let pattern = pattern.trim();
    if pattern.is_empty() {
        return false;
    }
    if pattern.contains('.') {
        return pattern == full;
    }
    pattern == key
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn event_keys() {
        // Go ignores sub_type when building full key.
        let (k, f) = compute_event_keys(&json!({
            "post_type":"message","message_type":"group","sub_type":"normal"
        }));
        assert_eq!(k, "message");
        assert_eq!(f, "message.group");

        let (k2, f2) = compute_event_keys(&json!({
            "post_type":"message_sent","message_type":"group"
        }));
        assert_eq!(k2, "message_sent");
        assert_eq!(f2, "");
    }

    #[test]
    fn content_from_segments() {
        let c = extract_content(&json!({
            "message":[
                {"type":"text","data":{"text":"hello"}},
                {"type":"face","data":{"id":"1"}},
                {"type":"text","data":{"text":" world"}}
            ]
        }));
        assert_eq!(c, "hello world");
    }

    #[test]
    fn prefix_must_match_or_skip() {
        let pat = r"^/(?P<content>.+)$";
        assert_eq!(
            strip_message_prefix("/echo hi", pat).as_deref(),
            Some("echo hi")
        );
        assert!(strip_message_prefix("echo hi", pat).is_none());
    }

    #[test]
    fn prefix_must_start_at_beginning() {
        // Even without ^, Go requires match at index 0.
        let pat = r"/(?P<content>.+)$";
        assert_eq!(
            strip_message_prefix("/echo hi", pat).as_deref(),
            Some("echo hi")
        );
        assert!(strip_message_prefix("x/echo hi", pat).is_none());
    }

    #[test]
    fn prefix_without_content_group_returns_remainder() {
        let pat = r"^/";
        assert_eq!(
            strip_message_prefix("/echo hi", pat).as_deref(),
            Some("echo hi")
        );
    }

    #[test]
    fn real_seq_string_and_parse() {
        assert_eq!(get_real_seq_string(&json!({"real_seq": "42"})), "42");
        assert_eq!(get_real_seq_string(&json!({"real_seq": 99})), "99");
        assert_eq!(parse_i64_loose("100"), 100);
        assert_eq!(parse_i64_loose(""), 0);
    }

    #[test]
    fn injects_content_field() {
        let raw = json!({"post_type":"message","raw_message":"x"});
        let out = inject_content_field(&raw, "hello");
        assert_eq!(out["content"], "hello");
        assert_eq!(out["raw_message"], "x");
    }

    #[test]
    fn match_event_go_parity() {
        // Empty never matches.
        assert!(!match_event("", "message", "message.group"));
        // No dot: post_type only.
        assert!(match_event("message", "message", "message.group"));
        assert!(match_event("notice", "notice", "notice.group_increase"));
        assert!(!match_event("notice", "message", "message.group"));
        // With dot: exact full key (no sub_type in full).
        assert!(match_event("message.group", "message", "message.group"));
        assert!(!match_event(
            "message.group.normal",
            "message",
            "message.group"
        ));
        // No wildcards in Go.
        assert!(!match_event("*", "notice", "notice.group_increase"));
        assert!(!match_event("notice*", "notice", "notice.group_increase"));
    }

    #[test]
    fn get_string_stringifies_numbers() {
        assert_eq!(get_string(&json!({"group_id": 12345}), "group_id"), "12345");
        assert_eq!(
            get_string(&json!({"raw_message": "hi"}), "raw_message"),
            "hi"
        );
        assert_eq!(get_string(&json!({}), "missing"), "");
    }

    #[test]
    fn content_from_message_string_not_raw_message() {
        // Go deriveContent ignores raw_message; only message field.
        let c = extract_content(&json!({
            "raw_message": "[CQ:at,qq=1] raw",
            "message":[{"type":"text","data":{"text":"from-seg"}}]
        }));
        assert_eq!(c, "from-seg");
        let c2 = extract_content(&json!({
            "raw_message": "raw-only",
            "message": "plain-message"
        }));
        assert_eq!(c2, "plain-message");
    }

    #[test]
    fn user_display_name_prefers_card() {
        assert_eq!(
            extract_user_display_name(&json!({
                "sender": {"card": "Card", "nickname": "Nick"}
            })),
            "Card"
        );
        assert_eq!(
            extract_user_display_name(&json!({
                "sender": {"card": "  ", "nickname": "Nick"}
            })),
            "Nick"
        );
        assert_eq!(
            extract_user_display_name(&json!({
                "sender": {"nickname": "Nick"}
            })),
            "Nick"
        );
        assert_eq!(extract_user_display_name(&json!({})), "");
    }

    #[test]
    fn message_text_prefers_raw_message() {
        assert_eq!(
            resolve_message_text(
                &json!({"raw_message": "raw", "message": "plain"}),
                "from-content"
            ),
            "raw"
        );
        assert_eq!(
            resolve_message_text(&json!({"raw_message": ""}), "from-content"),
            "from-content"
        );
    }

    #[test]
    fn group_name_from_event_without_ws() {
        assert_eq!(
            resolve_group_name(&json!({"group_name": "G1"}), None, 1),
            "G1"
        );
        assert_eq!(resolve_group_name(&json!({}), None, 1), "");
    }
}
