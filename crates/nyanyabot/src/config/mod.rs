use std::collections::{BTreeMap, HashMap, HashSet};
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::Arc;

use anyhow::{Context, Result, bail};
use rand::RngCore;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::sync::RwLock;

use crate::util::{OverrideRule, ensure_dir};

pub const DEFAULT_REVERSE_WS_LISTEN: &str = "0.0.0.0:3001";
pub const DEFAULT_MESSAGE_PREFIX: &str = r"^/(?P<content>.+)$";

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct AppConfig {
    #[serde(default)]
    pub onebot: OneBotConfig,
    #[serde(default)]
    pub webui: WebUIConfig,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub message_prefix: String,
    #[serde(default, skip_serializing_if = "HashMap::is_empty")]
    pub globals: HashMap<String, String>,
    #[serde(default, skip_serializing_if = "HashMap::is_empty")]
    pub plugins: HashMap<String, Value>,
    #[serde(default, skip_serializing_if = "HashMap::is_empty")]
    pub plugin_controls: HashMap<String, PluginControl>,
    #[serde(default)]
    pub global_access: AccessControl,
    #[serde(default)]
    pub chat_log: ChatLogConfig,
    #[serde(default)]
    pub trigger_log: TriggerLogConfig,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub message_dedup: Option<bool>,
    #[serde(default)]
    pub dedup: DedupConfig,
    #[serde(default, skip_serializing_if = "is_zero_i32")]
    pub global_sleep_timeout: i32,
    #[serde(default)]
    pub bot_access: BotAccessConfig,
    #[serde(default, skip_serializing_if = "HashMap::is_empty")]
    pub plugin_env: HashMap<String, String>,
}

fn is_zero_i32(v: &i32) -> bool {
    *v == 0
}

impl Default for AppConfig {
    fn default() -> Self {
        Self {
            onebot: OneBotConfig {
                reverse_ws: ReverseWSConfig {
                    listen_addr: DEFAULT_REVERSE_WS_LISTEN.into(),
                },
            },
            webui: WebUIConfig {
                listen_addr: "0.0.0.0:3000".into(),
                password: String::new(),
                auto_refresh: Some(true),
                refresh_interval: 1,
            },
            message_prefix: DEFAULT_MESSAGE_PREFIX.into(),
            globals: HashMap::new(),
            plugins: HashMap::new(),
            plugin_controls: HashMap::new(),
            global_access: AccessControl::default(),
            chat_log: ChatLogConfig::default(),
            trigger_log: TriggerLogConfig {
                enabled: false,
                database_uri: String::new(),
                queue_size: 1000,
                batch_size: 100,
                batch_interval: "5s".into(),
            },
            message_dedup: None,
            dedup: DedupConfig {
                enabled: true,
                backend: "memory".into(),
                ttl_seconds: 3600,
            },
            global_sleep_timeout: 0,
            bot_access: BotAccessConfig::default(),
            plugin_env: HashMap::new(),
        }
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
pub struct OneBotConfig {
    #[serde(default)]
    pub reverse_ws: ReverseWSConfig,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ReverseWSConfig {
    #[serde(default = "default_reverse_ws")]
    pub listen_addr: String,
}

impl Default for ReverseWSConfig {
    fn default() -> Self {
        Self {
            listen_addr: default_reverse_ws(),
        }
    }
}

fn default_reverse_ws() -> String {
    DEFAULT_REVERSE_WS_LISTEN.into()
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct WebUIConfig {
    #[serde(default = "default_webui_addr")]
    pub listen_addr: String,
    #[serde(default)]
    pub password: String,
    #[serde(default)]
    pub auto_refresh: Option<bool>,
    #[serde(default)]
    pub refresh_interval: i64,
}

impl Default for WebUIConfig {
    fn default() -> Self {
        Self {
            listen_addr: default_webui_addr(),
            password: String::new(),
            auto_refresh: Some(true),
            refresh_interval: 1,
        }
    }
}

fn default_webui_addr() -> String {
    "0.0.0.0:3000".into()
}

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq, Eq)]
pub struct ChatLogConfig {
    #[serde(default)]
    pub database_uri: String,
    #[serde(default)]
    pub queue: ChatLogQueueConfig,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ChatLogQueueConfig {
    #[serde(default = "default_queue_size")]
    pub size: i64,
}

impl Default for ChatLogQueueConfig {
    fn default() -> Self {
        Self {
            size: default_queue_size(),
        }
    }
}

fn default_queue_size() -> i64 {
    1000
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct TriggerLogConfig {
    #[serde(default)]
    pub enabled: bool,
    #[serde(default)]
    pub database_uri: String,
    #[serde(default = "default_queue_size")]
    pub queue_size: i64,
    #[serde(default = "default_batch_size")]
    pub batch_size: i64,
    #[serde(default = "default_batch_interval")]
    pub batch_interval: String,
}

impl Default for TriggerLogConfig {
    fn default() -> Self {
        Self {
            enabled: false,
            database_uri: String::new(),
            queue_size: 1000,
            batch_size: 100,
            batch_interval: "5s".into(),
        }
    }
}

fn default_batch_size() -> i64 {
    100
}
fn default_batch_interval() -> String {
    "5s".into()
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct DedupConfig {
    #[serde(default = "default_true")]
    pub enabled: bool,
    #[serde(default = "default_memory")]
    pub backend: String,
    #[serde(default = "default_ttl")]
    pub ttl_seconds: u64,
}

impl Default for DedupConfig {
    fn default() -> Self {
        Self {
            enabled: true,
            backend: "memory".into(),
            ttl_seconds: 3600,
        }
    }
}

fn default_true() -> bool {
    true
}
fn default_memory() -> String {
    "memory".into()
}
fn default_ttl() -> u64 {
    3600
}

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq, Eq)]
pub struct BotAccessConfig {
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub whitelist_bots: Vec<i64>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub blacklist_bots: Vec<i64>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub default_policy: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub reject_behavior: String,
}

impl BotAccessConfig {
    pub fn allowed(&self, bot_id: i64) -> bool {
        if self.whitelist_bots.contains(&bot_id) {
            return true;
        }
        if self.blacklist_bots.contains(&bot_id) {
            return false;
        }
        self.default_policy != "deny"
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq, Eq)]
pub struct AccessControl {
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub whitelist_users: Vec<i64>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub blacklist_users: Vec<i64>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub whitelist_groups: Vec<i64>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub blacklist_groups: Vec<i64>,
}

impl AccessControl {
    pub fn is_empty(&self) -> bool {
        self.whitelist_users.is_empty()
            && self.blacklist_users.is_empty()
            && self.whitelist_groups.is_empty()
            && self.blacklist_groups.is_empty()
    }

    pub fn allowed(&self, user_id: i64, group_id: i64) -> bool {
        if !self.whitelist_users.is_empty() && self.whitelist_users.contains(&user_id) {
            return true;
        }
        if self.blacklist_users.contains(&user_id) {
            return false;
        }
        if group_id > 0
            && !self.whitelist_groups.is_empty()
            && self.whitelist_groups.contains(&group_id)
        {
            return true;
        }
        if group_id > 0 && self.blacklist_groups.contains(&group_id) {
            return false;
        }
        if !self.whitelist_users.is_empty() || !self.whitelist_groups.is_empty() {
            return false;
        }
        true
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
pub struct PluginControl {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub enabled: Option<bool>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub disabled_commands: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub disabled_events: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub disabled_crons: Vec<String>,
    #[serde(default)]
    pub access: AccessControl,
    #[serde(default, skip_serializing_if = "HashMap::is_empty")]
    pub command_access: HashMap<String, AccessControl>,
    #[serde(default, skip_serializing_if = "HashMap::is_empty")]
    pub event_access: HashMap<String, AccessControl>,
    #[serde(default, skip_serializing_if = "HashMap::is_empty")]
    pub command_overrides: HashMap<String, Vec<OverrideRule>>,
    /// Overrides AppConfig.message_prefix matching for this plugin when non-empty.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub command_prefix: String,
    /// nil = use host default (enabled); Some overrides auto-sleep.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub enable_sleep: Option<bool>,
    /// None or 0 = use AppConfig.global_sleep_timeout.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub sleep_timeout: Option<i32>,
    #[serde(default, skip_serializing_if = "HashMap::is_empty")]
    pub env: HashMap<String, String>,
}

impl PluginControl {
    pub fn is_empty(&self) -> bool {
        self.enabled.is_none()
            && self.disabled_commands.is_empty()
            && self.disabled_events.is_empty()
            && self.disabled_crons.is_empty()
            && self.access.is_empty()
            && self.command_access.is_empty()
            && self.event_access.is_empty()
            && self.command_overrides.is_empty()
            && self.command_prefix.is_empty()
            && self.enable_sleep.is_none()
            && matches!(self.sleep_timeout, None | Some(0))
            && self.env.is_empty()
    }
}

impl AppConfig {
    pub fn is_plugin_enabled(&self, plugin_id: &str) -> bool {
        self.plugin_controls
            .get(plugin_id)
            .and_then(|c| c.enabled)
            .unwrap_or(true)
    }

    pub fn is_command_enabled(&self, plugin_id: &str, listener_id: &str) -> bool {
        self.plugin_controls
            .get(plugin_id)
            .map(|c| !c.disabled_commands.iter().any(|id| id == listener_id))
            .unwrap_or(true)
    }

    pub fn is_event_enabled(&self, plugin_id: &str, listener_id: &str) -> bool {
        self.plugin_controls
            .get(plugin_id)
            .map(|c| !c.disabled_events.iter().any(|id| id == listener_id))
            .unwrap_or(true)
    }

    pub fn is_cron_enabled(&self, plugin_id: &str, listener_id: &str) -> bool {
        self.plugin_controls
            .get(plugin_id)
            .map(|c| !c.disabled_crons.iter().any(|id| id == listener_id))
            .unwrap_or(true)
    }

    pub fn is_message_dedup_enabled(&self) -> bool {
        self.message_dedup.unwrap_or(self.dedup.enabled)
    }

    pub fn is_allowed(
        &self,
        plugin_id: &str,
        listener_id: &str,
        is_command: bool,
        user_id: i64,
        group_id: i64,
    ) -> bool {
        if !self.global_access.allowed(user_id, group_id) {
            return false;
        }
        let Some(ctrl) = self.plugin_controls.get(plugin_id) else {
            return true;
        };
        if !ctrl.access.allowed(user_id, group_id) {
            return false;
        }
        let map = if is_command {
            &ctrl.command_access
        } else {
            &ctrl.event_access
        };
        if let Some(ac) = map.get(listener_id) {
            return ac.allowed(user_id, group_id);
        }
        true
    }

    pub fn command_overrides(&self, plugin_id: &str, listener_id: &str) -> Vec<OverrideRule> {
        self.plugin_controls
            .get(plugin_id)
            .and_then(|c| c.command_overrides.get(listener_id))
            .cloned()
            .unwrap_or_default()
    }
}

#[derive(Debug)]
pub struct Store {
    path: PathBuf,
    cfg: RwLock<AppConfig>,
}

impl Store {
    pub fn new(data_dir: impl AsRef<Path>) -> Result<Arc<Self>> {
        let data_dir = data_dir.as_ref();
        if data_dir.as_os_str().is_empty() {
            bail!("dataDir is empty");
        }
        ensure_dir(data_dir)?;
        Ok(Arc::new(Self {
            path: data_dir.join("config.json"),
            cfg: RwLock::new(AppConfig::default()),
        }))
    }

    pub fn load_or_create_default(&self) -> Result<AppConfig> {
        match fs::read(&self.path) {
            Ok(bytes) => {
                let mut cfg: AppConfig = serde_json::from_slice(&bytes)
                    .with_context(|| format!("parse {}", self.path.display()))?;
                let changed = self.ensure_defaults(&mut cfg)?;
                *self.cfg.write().expect("cfg") = cfg.clone();
                if changed {
                    self.save_locked(&cfg)?;
                }
                Ok(cfg)
            }
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => {
                let mut cfg = AppConfig::default();
                self.ensure_defaults(&mut cfg)?;
                *self.cfg.write().expect("cfg") = cfg.clone();
                self.save_locked(&cfg)?;
                Ok(cfg)
            }
            Err(err) => Err(err).with_context(|| format!("read {}", self.path.display())),
        }
    }

    pub fn get(&self) -> AppConfig {
        self.cfg.read().expect("cfg").clone()
    }

    pub fn update<F>(&self, f: F) -> Result<AppConfig>
    where
        F: FnOnce(&mut AppConfig),
    {
        let mut cfg = self.cfg.write().expect("cfg").clone();
        f(&mut cfg);
        self.ensure_defaults(&mut cfg)?;
        self.save_locked(&cfg)?;
        *self.cfg.write().expect("cfg") = cfg.clone();
        Ok(cfg)
    }

    fn save_locked(&self, cfg: &AppConfig) -> Result<()> {
        let data = serde_json::to_vec_pretty(cfg)?;
        let tmp = self.path.with_extension("json.tmp");
        fs::write(&tmp, &data).with_context(|| format!("write {}", tmp.display()))?;
        fs::rename(&tmp, &self.path)
            .with_context(|| format!("rename {} -> {}", tmp.display(), self.path.display()))?;
        Ok(())
    }

    fn ensure_defaults(&self, cfg: &mut AppConfig) -> Result<bool> {
        let mut changed = false;
        if cfg.onebot.reverse_ws.listen_addr.trim().is_empty() {
            cfg.onebot.reverse_ws.listen_addr = DEFAULT_REVERSE_WS_LISTEN.into();
            changed = true;
        }
        if cfg.webui.listen_addr.trim().is_empty() {
            cfg.webui.listen_addr = "0.0.0.0:3000".into();
            changed = true;
        }
        if cfg.webui.password.trim().is_empty() {
            cfg.webui.password = generate_webui_password(16)?;
            changed = true;
        }
        if cfg.webui.refresh_interval <= 0 {
            cfg.webui.refresh_interval = 1;
            changed = true;
        }
        if cfg.message_prefix.trim().is_empty() {
            cfg.message_prefix = DEFAULT_MESSAGE_PREFIX.into();
            changed = true;
        }
        if cfg.dedup.ttl_seconds == 0 {
            cfg.dedup.ttl_seconds = 3600;
            changed = true;
        }
        if cfg.dedup.backend.trim().is_empty() {
            cfg.dedup.backend = "memory".into();
            changed = true;
        }
        if cfg.trigger_log.queue_size <= 0 {
            cfg.trigger_log.queue_size = 1000;
            changed = true;
        }
        if cfg.trigger_log.batch_size <= 0 {
            cfg.trigger_log.batch_size = 100;
            changed = true;
        }
        if cfg.trigger_log.batch_interval.trim().is_empty() {
            cfg.trigger_log.batch_interval = "5s".into();
            changed = true;
        }
        // Normalize maps to keep stable serialization keys optionally later.
        let _ = BTreeMap::<String, Value>::new();
        let _ = HashSet::<String>::new();
        Ok(changed)
    }
}

pub fn generate_webui_password(length: usize) -> Result<String> {
    const ALPHABET: &[u8] = b"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
    let mut rng = rand::rng();
    let mut out = String::with_capacity(length);
    for _ in 0..length.max(8) {
        let idx = (rng.next_u32() as usize) % ALPHABET.len();
        out.push(ALPHABET[idx] as char);
    }
    Ok(out)
}

pub fn merge_process_env(
    host_environ: impl IntoIterator<Item = impl AsRef<str>>,
    global_env: &HashMap<String, String>,
    plugin_env: &HashMap<String, String>,
) -> Vec<String> {
    let mut map: HashMap<String, String> = HashMap::new();
    for item in host_environ {
        let item = item.as_ref();
        if let Some((k, v)) = item.split_once('=') {
            map.insert(k.to_string(), v.to_string());
        }
    }
    for (k, v) in global_env {
        map.insert(k.clone(), v.clone());
    }
    for (k, v) in plugin_env {
        map.insert(k.clone(), v.clone());
    }
    let mut out: Vec<String> = map.into_iter().map(|(k, v)| format!("{k}={v}")).collect();
    out.sort();
    out
}

pub fn normalize_string_map(input: &HashMap<String, String>) -> HashMap<String, String> {
    let mut out = HashMap::new();
    let mut keys: Vec<_> = input.keys().cloned().collect();
    keys.sort();
    for k in keys {
        let key = k.trim();
        if key.is_empty() {
            continue;
        }
        let value = input
            .get(&k)
            .map(|v| v.trim().to_string())
            .unwrap_or_default();
        out.insert(key.to_string(), value);
    }
    out
}

pub fn string_maps_equal(a: &HashMap<String, String>, b: &HashMap<String, String>) -> bool {
    if a.len() != b.len() {
        return false;
    }
    a.iter()
        .all(|(k, v)| b.get(k).map(|x| x == v).unwrap_or(false))
}

/// Apply a partial WebUI config patch without wiping unspecified fields.
pub fn apply_config_patch(cfg: &mut AppConfig, patch: &serde_json::Value) -> Result<()> {
    if !patch.is_object() {
        bail!("config patch must be a JSON object");
    }
    if let Some(addr) = patch
        .pointer("/onebot/reverse_ws/listen_addr")
        .and_then(|v| v.as_str())
    {
        cfg.onebot.reverse_ws.listen_addr = addr.trim().to_string();
    }
    if let Some(webui) = patch.get("webui") {
        if let Some(addr) = webui.get("listen_addr").and_then(|v| v.as_str()) {
            cfg.webui.listen_addr = addr.trim().to_string();
        }
        if let Some(password) = webui.get("password").and_then(|v| v.as_str()) {
            cfg.webui.password = password.to_string();
        }
        if let Some(auto) = webui.get("auto_refresh").and_then(|v| v.as_bool()) {
            cfg.webui.auto_refresh = Some(auto);
        }
        if let Some(interval) = webui.get("refresh_interval").and_then(|v| v.as_i64()) {
            cfg.webui.refresh_interval = interval;
        }
    }
    if let Some(chat) = patch.get("chat_log") {
        if let Some(uri) = chat.get("database_uri").and_then(|v| v.as_str()) {
            cfg.chat_log.database_uri = uri.trim().to_string();
        }
        if let Some(size) = chat.pointer("/queue/size").and_then(|v| v.as_i64()) {
            cfg.chat_log.queue.size = size;
        }
    }
    if let Some(trigger) = patch.get("trigger_log") {
        if let Some(enabled) = trigger.get("enabled").and_then(|v| v.as_bool()) {
            cfg.trigger_log.enabled = enabled;
        }
        if let Some(uri) = trigger.get("database_uri").and_then(|v| v.as_str()) {
            cfg.trigger_log.database_uri = uri.trim().to_string();
        }
        if let Some(size) = trigger.get("queue_size").and_then(|v| v.as_i64()) {
            cfg.trigger_log.queue_size = size;
        }
        if let Some(size) = trigger.get("batch_size").and_then(|v| v.as_i64()) {
            cfg.trigger_log.batch_size = size;
        }
        if let Some(interval) = trigger.get("batch_interval").and_then(|v| v.as_str()) {
            cfg.trigger_log.batch_interval = interval.trim().to_string();
        }
    }
    if let Some(prefix) = patch.get("message_prefix").and_then(|v| v.as_str()) {
        cfg.message_prefix = prefix.to_string();
    }
    if let Some(timeout) = patch.get("global_sleep_timeout").and_then(|v| v.as_i64()) {
        cfg.global_sleep_timeout = timeout as i32;
    }
    if let Some(access) = patch.get("global_access") {
        cfg.global_access = serde_json::from_value(access.clone())
            .map_err(|e| anyhow::anyhow!("invalid global_access: {e}"))?;
    }
    if let Some(bot_access) = patch.get("bot_access") {
        if let Some(v) = bot_access.get("whitelist_bots") {
            cfg.bot_access.whitelist_bots = serde_json::from_value(v.clone())
                .map_err(|e| anyhow::anyhow!("invalid whitelist_bots: {e}"))?;
        }
        if let Some(v) = bot_access.get("blacklist_bots") {
            cfg.bot_access.blacklist_bots = serde_json::from_value(v.clone())
                .map_err(|e| anyhow::anyhow!("invalid blacklist_bots: {e}"))?;
        }
        if let Some(v) = bot_access.get("default_policy").and_then(|v| v.as_str()) {
            cfg.bot_access.default_policy = v.trim().to_string();
        }
        if let Some(v) = bot_access.get("reject_behavior").and_then(|v| v.as_str()) {
            cfg.bot_access.reject_behavior = v.trim().to_string();
        }
    }
    Ok(())
}

pub fn validate_env_key(key: &str) -> Result<()> {
    if key.is_empty() {
        bail!("env key is empty");
    }
    let mut chars = key.chars();
    match chars.next() {
        Some(c) if c == '_' || c.is_ascii_alphabetic() => {}
        _ => bail!("invalid env key: {key}"),
    }
    if !chars.all(|c| c == '_' || c.is_ascii_alphanumeric()) {
        bail!("invalid env key: {key}");
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn access_control_whitelist() {
        let ac = AccessControl {
            whitelist_users: vec![1],
            ..Default::default()
        };
        assert!(ac.allowed(1, 0));
        assert!(!ac.allowed(2, 0));
    }

    #[test]
    fn store_creates_default() {
        let dir = tempdir().unwrap();
        let store = Store::new(dir.path()).unwrap();
        let cfg = store.load_or_create_default().unwrap();
        assert!(!cfg.webui.password.is_empty());
        assert!(dir.path().join("config.json").exists());
        let again = store.load_or_create_default().unwrap();
        assert_eq!(cfg.webui.password, again.webui.password);
    }

    #[test]
    fn atomic_update() {
        let dir = tempdir().unwrap();
        let store = Store::new(dir.path()).unwrap();
        store.load_or_create_default().unwrap();
        store
            .update(|cfg| {
                cfg.globals.insert("a".into(), "1".into());
            })
            .unwrap();
        assert_eq!(store.get().globals.get("a").unwrap(), "1");
    }

    #[test]
    fn config_patch_preserves_unrelated_fields() {
        let mut cfg = AppConfig::default();
        cfg.webui.password = "secret".into();
        cfg.plugins
            .insert("external.echo".into(), serde_json::json!({"prefix":"x"}));
        apply_config_patch(
            &mut cfg,
            &serde_json::json!({
                "webui": {"listen_addr": "127.0.0.1:3000", "auto_refresh": false, "refresh_interval": 3},
                "global_sleep_timeout": 42
            }),
        )
        .unwrap();
        assert_eq!(cfg.webui.password, "secret");
        assert_eq!(cfg.webui.refresh_interval, 3);
        assert_eq!(cfg.global_sleep_timeout, 42);
        assert!(cfg.plugins.contains_key("external.echo"));
    }
}
