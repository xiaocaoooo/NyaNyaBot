use std::sync::Arc;

use nyanyabot_proto::CommandMatch;
use regex::Regex;
use serde_json::{Value, json};
use tracing::{debug, info, warn};

use crate::config::Store;
use crate::dedup::Deduper;
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
}

impl Dispatcher {
    pub fn new(
        pm: Arc<Manager>,
        store: Arc<Store>,
        stats: Arc<Stats>,
        host: Arc<PluginHost>,
        deduper: Option<Arc<dyn Deduper>>,
    ) -> Arc<Self> {
        Arc::new(Self {
            pm,
            store,
            stats,
            host,
            deduper,
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

        self.stats.inc_recv();

        let (event_key, event_key_full) = compute_event_keys(&raw);
        let entries = self.pm.entries().await;

        // Event listeners
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
                    }),
                );
                info!(plugin_id = %pid, event_id = %l.id, event_type = %event_key, "event dispatched");
                if let Err(err) = plugin.handle(&l.id, raw.clone(), None, &trace_id).await {
                    warn!(plugin_id = %pid, error = %err, "event handle failed");
                }
                self.host.end_trace(&trace_id);
            }
        }

        // Command listeners (message only)
        if post_type != "message" && post_type != "message_sent" {
            return;
        }
        let raw_message = get_string(&raw, "raw_message");
        let content = extract_content(&raw);
        let prefix_re = Regex::new(&cfg.message_prefix).ok();

        for (pid, desc, plugin) in &entries {
            if !cfg.is_plugin_enabled(pid) {
                continue;
            }
            for l in &desc.commands {
                if !cfg.is_command_enabled(pid, &l.id) {
                    continue;
                }
                if !cfg.is_allowed(pid, &l.id, true, user_id, group_id) {
                    continue;
                }
                let mut input = if l.match_raw {
                    raw_message.clone()
                } else {
                    content.clone()
                };
                // message prefix strip for non-raw by default path: apply prefix on content-like input
                if let Some(re) = &prefix_re
                    && let Some(caps) = re.captures(&input)
                {
                    if let Some(c) = caps.name("content") {
                        input = c.as_str().to_string();
                    } else if let Some(c) = caps.get(1) {
                        input = c.as_str().to_string();
                    }
                }
                let overrides = cfg.command_overrides(pid, &l.id);
                if !overrides.is_empty() {
                    input = apply_overrides(&input, &overrides);
                }
                let Ok(re) = Regex::new(&l.pattern) else {
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
                // named groups also included via numbered in Rust regex
                let match_data = CommandMatch { full, groups };
                let trace_id = self.host.generate_trace_id();
                self.host.begin_trace(
                    &trace_id,
                    pid,
                    &l.id,
                    "command",
                    json!({
                        "pattern": l.pattern,
                        "user_id": user_id,
                        "group_id": group_id,
                        "raw_message": raw_message,
                    }),
                );
                info!(plugin_id = %pid, command_id = %l.id, "command dispatched");
                if let Err(err) = plugin
                    .handle(&l.id, raw.clone(), Some(match_data), &trace_id)
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
    match v.get(key) {
        Some(Value::String(s)) => s.clone(),
        Some(Value::Number(n)) => n.to_string(),
        _ => String::new(),
    }
}

fn get_i64(v: &Value, key: &str) -> i64 {
    match v.get(key) {
        Some(Value::Number(n)) => n.as_i64().unwrap_or(0),
        Some(Value::String(s)) => s.parse().unwrap_or(0),
        _ => 0,
    }
}

fn extract_content(raw: &Value) -> String {
    if let Some(arr) = raw.get("message").and_then(|m| m.as_array()) {
        let mut out = String::new();
        for seg in arr {
            if seg.get("type").and_then(|t| t.as_str()) == Some("text")
                && let Some(t) = seg.pointer("/data/text").and_then(|x| x.as_str())
            {
                out.push_str(t);
            }
        }
        return out;
    }
    get_string(raw, "raw_message")
}

fn compute_event_keys(raw: &Value) -> (String, String) {
    let post_type = get_string(raw, "post_type");
    let detail = match post_type.as_str() {
        "message" | "message_sent" => get_string(raw, "message_type"),
        "notice" => get_string(raw, "notice_type"),
        "request" => get_string(raw, "request_type"),
        "meta_event" => get_string(raw, "meta_event_type"),
        _ => String::new(),
    };
    let sub = get_string(raw, "sub_type");
    let key = if detail.is_empty() {
        post_type.clone()
    } else {
        format!("{post_type}.{detail}")
    };
    let full = if sub.is_empty() {
        key.clone()
    } else {
        format!("{key}.{sub}")
    };
    (key, full)
}

fn match_event(pattern: &str, key: &str, full: &str) -> bool {
    let pattern = pattern.trim();
    if pattern.is_empty() || pattern == "*" || pattern == "all" {
        return true;
    }
    pattern == key || pattern == full || full.starts_with(&(pattern.to_string() + "."))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn event_keys() {
        let raw = json!({"post_type":"message","message_type":"group","sub_type":"normal"});
        let (k, f) = compute_event_keys(&raw);
        assert_eq!(k, "message.group");
        assert_eq!(f, "message.group.normal");
    }

    #[test]
    fn content_from_segments() {
        let raw = json!({
            "message": [
                {"type":"at","data":{"qq":"1"}},
                {"type":"text","data":{"text":"hello"}},
                {"type":"text","data":{"text":" world"}}
            ]
        });
        assert_eq!(extract_content(&raw), "hello world");
    }
}
