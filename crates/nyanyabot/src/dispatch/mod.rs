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

        let (event_key, event_key_full) = compute_event_keys(&raw);
        let entries = self.pm.entries().await;

        // Event listeners (all post types that match)
        let event_raw = inject_content_field(&raw, &content);
        for (pid, desc, plugin) in &entries {
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
                // Only inject content for message-like events (Go behavior)
                let payload = if post_type == "message" || post_type == "message_sent" {
                    event_raw.clone()
                } else {
                    raw.clone()
                };
                let trace_id = self.host.generate_trace_id();
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
                    }),
                );
                info!(plugin_id = %pid, event_id = %l.id, event_type = %event_key, "event dispatched");
                if let Err(err) = self.host.ensure_awake(pid).await {
                    warn!(plugin_id = %pid, error = %err, "ensure_awake failed");
                }
                if let Err(err) = plugin.handle(&l.id, payload, None, &trace_id).await {
                    warn!(plugin_id = %pid, error = %err, "event handle failed");
                }
                self.host.end_trace(&trace_id);
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

        for (pid, desc, plugin) in &entries {
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
                self.host.begin_trace(
                    &trace_id,
                    pid,
                    &l.id,
                    "message",
                    json!({
                        "group_id": group_id,
                        "user_id": user_id,
                        "self_id": get_i64(&raw, "self_id"),
                        "raw_message": raw_message,
                        "seq": message_seq,
                        "content": content,
                    }),
                );
                info!(plugin_id = %pid, command_id = %l.id, "command matched");
                if let Err(err) = self.host.ensure_awake(pid).await {
                    warn!(plugin_id = %pid, error = %err, "ensure_awake failed");
                }
                if let Err(err) = plugin
                    .handle(&l.id, command_raw.clone(), Some(match_data), &trace_id)
                    .await
                {
                    warn!(plugin_id = %pid, error = %err, "command handle failed");
                }
                self.host.end_trace(&trace_id);
            }
        }
    }
}

fn get_string(v: &Value, key: &str) -> String {
    v.get(key)
        .and_then(|x| x.as_str())
        .unwrap_or("")
        .to_string()
}

fn get_i64(v: &Value, key: &str) -> i64 {
    v.get(key)
        .and_then(|x| {
            x.as_i64()
                .or_else(|| x.as_u64().map(|n| n as i64))
                .or_else(|| x.as_f64().map(|n| n as i64))
                .or_else(|| x.as_str().and_then(|s| s.parse().ok()))
        })
        .unwrap_or(0)
}

fn extract_content(raw: &Value) -> String {
    if let Some(s) = raw.get("raw_message").and_then(|v| v.as_str())
        && !s.is_empty()
    {
        return s.to_string();
    }
    if let Some(arr) = raw.get("message").and_then(|v| v.as_array()) {
        let mut out = String::new();
        for seg in arr {
            if seg.get("type").and_then(|t| t.as_str()) == Some("text")
                && let Some(t) = seg.pointer("/data/text").and_then(|v| v.as_str())
            {
                out.push_str(t);
            }
        }
        return out;
    }
    String::new()
}

fn inject_content_field(raw: &Value, content: &str) -> Value {
    let mut obj = raw.clone();
    if let Some(map) = obj.as_object_mut() {
        map.insert("content".into(), Value::String(content.to_string()));
    }
    obj
}

/// Strip message prefix. Returns None if pattern invalid or does not match (Go skip behavior).
fn strip_message_prefix(input: &str, pattern: &str) -> Option<String> {
    let pattern = pattern.trim();
    if pattern.is_empty() {
        return Some(input.to_string());
    }
    let re = Regex::new(pattern).ok()?;
    let caps = re.captures(input)?;
    if let Some(c) = caps.name("content") {
        return Some(c.as_str().to_string());
    }
    if let Some(c) = caps.get(1) {
        return Some(c.as_str().to_string());
    }
    // matched but no capture group: treat full match strip as empty remainder after match
    if let Some(m) = caps.get(0) {
        return Some(input[m.end()..].to_string());
    }
    None
}

fn compute_event_keys(raw: &Value) -> (String, String) {
    let post = get_string(raw, "post_type");
    let mut full = post.clone();
    let detail = match post.as_str() {
        "message" | "message_sent" => get_string(raw, "message_type"),
        "notice" => get_string(raw, "notice_type"),
        "request" => get_string(raw, "request_type"),
        "meta_event" => get_string(raw, "meta_event_type"),
        _ => String::new(),
    };
    if !detail.is_empty() {
        full = format!("{post}.{detail}");
    }
    let sub = get_string(raw, "sub_type");
    if !sub.is_empty() {
        full = format!("{full}.{sub}");
    }
    (post, full)
}

fn match_event(pattern: &str, key: &str, full: &str) -> bool {
    let pattern = pattern.trim();
    if pattern.is_empty() || pattern == "*" {
        return true;
    }
    if pattern == key || pattern == full {
        return true;
    }
    if let Some(prefix) = pattern.strip_suffix('*') {
        return full.starts_with(prefix) || key.starts_with(prefix);
    }
    false
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn event_keys() {
        let (k, f) = compute_event_keys(&json!({
            "post_type":"message","message_type":"group","sub_type":"normal"
        }));
        assert_eq!(k, "message");
        assert_eq!(f, "message.group.normal");
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
    fn injects_content_field() {
        let raw = json!({"post_type":"message","raw_message":"x"});
        let out = inject_content_field(&raw, "hello");
        assert_eq!(out["content"], "hello");
        assert_eq!(out["raw_message"], "x");
    }
}
