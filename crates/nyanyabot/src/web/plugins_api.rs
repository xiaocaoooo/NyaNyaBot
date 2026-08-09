use std::collections::{BTreeSet, HashMap, HashSet};
use std::sync::Arc;

use axum::extract::{Path, State};
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use axum::Json;
use nyanyabot_proto::{Descriptor, ensure_descriptor_arrays};
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};

use crate::config::{AccessControl, AppConfig, PluginControl, validate_env_key};
use crate::plugin::Manager;
use crate::util::OverrideRule;

use super::WebInner;

#[derive(Debug, Clone, Serialize)]
pub struct PluginStateView {
    pub enabled: bool,
    pub commands: Map<String, Value>,
    pub events: Map<String, Value>,
    pub crons: Map<String, Value>,
    pub command_prefix: String,
    pub enable_sleep: bool,
    pub sleep_timeout: i32,
    pub status: String,
    pub access: AccessControl,
    pub command_access: Map<String, Value>,
    pub event_access: Map<String, Value>,
    pub command_overrides: Map<String, Value>,
    pub env: Map<String, Value>,
}

#[derive(Debug, Clone, Serialize)]
struct PluginListItem {
    #[serde(flatten)]
    descriptor: Descriptor,
    state: PluginStateView,
}

#[derive(Debug, Default, Deserialize)]
struct PluginConfigPatch {
    #[serde(default)]
    config: Value,
}

#[derive(Debug, Default, Deserialize)]
struct PluginSwitchPatch {
    enabled: Option<bool>,
    commands: Option<HashMap<String, bool>>,
    events: Option<HashMap<String, bool>>,
    crons: Option<HashMap<String, bool>>,
    prefix: Option<String>,
    enable_sleep: Option<bool>,
    sleep_timeout: Option<i32>,
    access: Option<AccessControl>,
    command_access: Option<HashMap<String, AccessControl>>,
    event_access: Option<HashMap<String, AccessControl>>,
    command_overrides: Option<HashMap<String, Vec<OverrideRule>>>,
    env: Option<HashMap<String, String>>,
}

fn bool_map_to_value_map(m: &HashMap<String, bool>) -> Map<String, Value> {
    m.iter()
        .map(|(k, v)| (k.clone(), Value::Bool(*v)))
        .collect()
}

fn string_map_to_value_map(m: &HashMap<String, String>) -> Map<String, Value> {
    m.iter()
        .map(|(k, v)| (k.clone(), Value::String(v.clone())))
        .collect()
}

fn access_map_to_value_map(m: &HashMap<String, AccessControl>) -> Map<String, Value> {
    m.iter()
        .map(|(k, v)| (k.clone(), serde_json::to_value(v).unwrap_or(Value::Null)))
        .collect()
}

fn overrides_map_to_value_map(m: &HashMap<String, Vec<OverrideRule>>) -> Map<String, Value> {
    m.iter()
        .map(|(k, v)| (k.clone(), serde_json::to_value(v).unwrap_or(Value::Null)))
        .collect()
}

pub async fn build_plugin_state(
    pm: &Manager,
    cfg: &AppConfig,
    desc: &Descriptor,
) -> PluginStateView {
    let control = cfg.plugin_controls.get(&desc.plugin_id);

    let mut commands = HashMap::new();
    for cmd in &desc.commands {
        commands.insert(
            cmd.id.clone(),
            cfg.is_command_enabled(&desc.plugin_id, &cmd.id),
        );
    }
    let mut events = HashMap::new();
    for evt in &desc.events {
        events.insert(
            evt.id.clone(),
            cfg.is_event_enabled(&desc.plugin_id, &evt.id),
        );
    }
    let mut crons = HashMap::new();
    for cron in &desc.crons {
        crons.insert(
            cron.id.clone(),
            cfg.is_cron_enabled(&desc.plugin_id, &cron.id),
        );
    }

    let mut command_prefix = String::new();
    let mut enable_sleep = true;
    let mut sleep_timeout = cfg.global_sleep_timeout;
    let mut access = AccessControl::default();
    let mut command_access = HashMap::new();
    let mut event_access = HashMap::new();
    let mut command_overrides = HashMap::new();
    let mut env_map = HashMap::new();

    if let Some(control) = control {
        command_prefix = control.command_prefix.clone();
        access = control.access.clone();
        command_access = control.command_access.clone();
        event_access = control.event_access.clone();
        command_overrides = control.command_overrides.clone();
        env_map = control.env.clone();
        enable_sleep = control.enable_sleep.unwrap_or(true);
        if let Some(st) = control.sleep_timeout {
            if st > 0 {
                sleep_timeout = st;
            }
        }
    }

    let mut status = "Unknown".to_string();
    if let Some((plugin, _)) = pm.get(&desc.plugin_id).await {
        match plugin.status().await {
            Ok(s) if !s.trim().is_empty() => status = s,
            Err(_) => status = "Crashed".into(),
            Ok(_) => {}
        }
    }

    PluginStateView {
        enabled: cfg.is_plugin_enabled(&desc.plugin_id),
        commands: bool_map_to_value_map(&commands),
        events: bool_map_to_value_map(&events),
        crons: bool_map_to_value_map(&crons),
        command_prefix,
        enable_sleep,
        sleep_timeout,
        status,
        access,
        command_access: access_map_to_value_map(&command_access),
        event_access: access_map_to_value_map(&event_access),
        command_overrides: overrides_map_to_value_map(&command_overrides),
        env: string_map_to_value_map(&env_map),
    }
}

fn apply_listener_switches(disabled: Vec<String>, patch: &HashMap<String, bool>) -> Vec<String> {
    let mut set: BTreeSet<String> = disabled
        .into_iter()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .collect();
    for (id, enabled) in patch {
        let id = id.trim();
        if id.is_empty() {
            continue;
        }
        if *enabled {
            set.remove(id);
        } else {
            set.insert(id.to_string());
        }
    }
    set.into_iter().collect()
}

fn validate_listener_switch_ids(
    desc: &Descriptor,
    commands: Option<&HashMap<String, bool>>,
    events: Option<&HashMap<String, bool>>,
    crons: Option<&HashMap<String, bool>>,
) -> Result<(), String> {
    if let Some(commands) = commands {
        let allowed: HashSet<&str> = desc.commands.iter().map(|c| c.id.as_str()).collect();
        for id in commands.keys() {
            let id = id.trim();
            if id.is_empty() {
                return Err("command listener id is empty".into());
            }
            if !allowed.contains(id) {
                return Err(format!("unknown command listener \"{id}\""));
            }
        }
    }
    if let Some(events) = events {
        let allowed: HashSet<&str> = desc.events.iter().map(|e| e.id.as_str()).collect();
        for id in events.keys() {
            let id = id.trim();
            if id.is_empty() {
                return Err("event listener id is empty".into());
            }
            if !allowed.contains(id) {
                return Err(format!("unknown event listener \"{id}\""));
            }
        }
    }
    if let Some(crons) = crons {
        let allowed: HashSet<&str> = desc.crons.iter().map(|c| c.id.as_str()).collect();
        for id in crons.keys() {
            let id = id.trim();
            if id.is_empty() {
                return Err("cron listener id is empty".into());
            }
            if !allowed.contains(id) {
                return Err(format!("unknown cron listener \"{id}\""));
            }
        }
    }
    Ok(())
}

fn validate_env_map(env: Option<&HashMap<String, String>>) -> Result<(), String> {
    let Some(env) = env else {
        return Ok(());
    };
    for k in env.keys() {
        validate_env_key(k).map_err(|e| e.to_string())?;
    }
    Ok(())
}

fn apply_plugin_switch_patch(mut control: PluginControl, patch: PluginSwitchPatch) -> PluginControl {
    if let Some(enabled) = patch.enabled {
        control.enabled = Some(enabled);
    }
    if let Some(prefix) = patch.prefix {
        control.command_prefix = prefix.trim().to_string();
    }
    if let Some(enable_sleep) = patch.enable_sleep {
        control.enable_sleep = Some(enable_sleep);
    }
    if let Some(sleep_timeout) = patch.sleep_timeout {
        control.sleep_timeout = Some(sleep_timeout);
    }
    if let Some(commands) = patch.commands.as_ref() {
        control.disabled_commands = apply_listener_switches(control.disabled_commands, commands);
    }
    if let Some(events) = patch.events.as_ref() {
        control.disabled_events = apply_listener_switches(control.disabled_events, events);
    }
    if let Some(crons) = patch.crons.as_ref() {
        control.disabled_crons = apply_listener_switches(control.disabled_crons, crons);
    }
    if let Some(access) = patch.access {
        control.access = access;
    }
    if let Some(command_access) = patch.command_access {
        control.command_access = command_access;
    }
    if let Some(event_access) = patch.event_access {
        control.event_access = event_access;
    }
    if let Some(command_overrides) = patch.command_overrides {
        control.command_overrides = command_overrides;
    }
    if let Some(env) = patch.env {
        control.env = env;
    }
    control
}

pub async fn api_plugins(State(state): State<Arc<WebInner>>) -> Response {
    let cfg = state.store.get();
    let mut descs = state.pm.list().await;
    descs.sort_by(|a, b| match a.name.cmp(&b.name) {
        std::cmp::Ordering::Equal => a.plugin_id.cmp(&b.plugin_id),
        other => other,
    });
    // NOTE: fix Equal below via sed after write
    let mut items = Vec::with_capacity(descs.len());
    for mut desc in descs {
        ensure_descriptor_arrays(&mut desc);
        let state_view = build_plugin_state(&state.pm, &cfg, &desc).await;
        items.push(PluginListItem {
            descriptor: desc,
            state: state_view,
        });
    }
    Json(items).into_response()
}

async fn handle_plugin_config_get(state: &WebInner, plugin_id: &str) -> Response {
    let plugin_id = plugin_id.trim();
    if plugin_id.is_empty() {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": "plugin_id is empty"})),
        )
            .into_response();
    }
    if state.pm.get(plugin_id).await.is_none() {
        return (
            StatusCode::NOT_FOUND,
            Json(json!({"error": "plugin not found"})),
        )
            .into_response();
    }
    let cfg = state.store.get();
    let config = cfg
        .plugins
        .get(plugin_id)
        .cloned()
        .unwrap_or_else(|| json!({}));
    Json(json!({"plugin_id": plugin_id, "config": config})).into_response()
}

async fn handle_plugin_config_put(state: &WebInner, plugin_id: &str, body: Value) -> Response {
    let plugin_id = plugin_id.trim();
    if plugin_id.is_empty() {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": "plugin_id is empty"})),
        )
            .into_response();
    }
    if state.pm.get(plugin_id).await.is_none() {
        return (
            StatusCode::NOT_FOUND,
            Json(json!({"error": "plugin not found"})),
        )
            .into_response();
    }
    let patch: PluginConfigPatch = match serde_json::from_value(body) {
        Ok(p) => p,
        Err(err) => {
            return (
                StatusCode::BAD_REQUEST,
                Json(json!({"error": err.to_string()})),
            )
                .into_response();
        }
    };
    let config = match patch.config {
        Value::Null => json!({}),
        Value::Object(map) => Value::Object(map),
        _ => {
            return (
                StatusCode::BAD_REQUEST,
                Json(json!({"error": "config must be a JSON object"})),
            )
                .into_response();
        }
    };
    match state.store.update(|cfg| {
        cfg.plugins.insert(plugin_id.to_string(), config);
    }) {
        Ok(_) => {
            let _ = state.host.reconfigure_plugin(plugin_id).await;
            Json(json!({"ok": true})).into_response()
        }
        Err(err) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(json!({"error": err.to_string()})),
        )
            .into_response(),
    }
}

async fn handle_plugin_switches_put(state: &WebInner, plugin_id: &str, body: Value) -> Response {
    let plugin_id = plugin_id.trim();
    if plugin_id.is_empty() {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": "plugin_id is empty"})),
        )
            .into_response();
    }
    let Some((_plugin, mut desc)) = state.pm.get(plugin_id).await else {
        return (
            StatusCode::NOT_FOUND,
            Json(json!({"error": "plugin not found"})),
        )
            .into_response();
    };
    ensure_descriptor_arrays(&mut desc);
    let patch: PluginSwitchPatch = match serde_json::from_value(body) {
        Ok(p) => p,
        Err(err) => {
            return (
                StatusCode::BAD_REQUEST,
                Json(json!({"error": err.to_string()})),
            )
                .into_response();
        }
    };
    if let Err(err) = validate_listener_switch_ids(
        &desc,
        patch.commands.as_ref(),
        patch.events.as_ref(),
        patch.crons.as_ref(),
    ) {
        return (StatusCode::BAD_REQUEST, Json(json!({"error": err}))).into_response();
    }
    if let Err(err) = validate_env_map(patch.env.as_ref()) {
        return (StatusCode::BAD_REQUEST, Json(json!({"error": err}))).into_response();
    }

    let prev_env = state
        .store
        .get()
        .plugin_controls
        .get(plugin_id)
        .map(|c| c.env.clone())
        .unwrap_or_default();

    let cfg = match state.store.update(|cfg| {
        let control = cfg
            .plugin_controls
            .get(plugin_id)
            .cloned()
            .unwrap_or_default();
        cfg.plugin_controls.insert(
            plugin_id.to_string(),
            apply_plugin_switch_patch(control, patch),
        );
    }) {
        Ok(cfg) => cfg,
        Err(err) => {
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(json!({"error": err.to_string()})),
            )
                .into_response();
        }
    };

    let new_env = cfg
        .plugin_controls
        .get(plugin_id)
        .map(|c| c.env.clone())
        .unwrap_or_default();
    if prev_env != new_env {
        let _ = state
            .host
            .restart_plugins(Some(vec![plugin_id.to_string()]))
            .await;
    }

    let state_view = build_plugin_state(&state.pm, &cfg, &desc).await;
    Json(json!({"ok": true, "state": state_view})).into_response()
}

pub async fn api_plugin_sub(State(state): State<Arc<WebInner>>, Path(rest): Path<String>) -> Response {
    let parts: Vec<&str> = rest.trim_matches('/').split('/').filter(|p| !p.is_empty()).collect();
    if parts.is_empty() {
        return (StatusCode::NOT_FOUND, Json(json!({"error": "not found"}))).into_response();
    }
    let plugin_id = parts[0];
    if parts.len() == 1 {
        return match state.pm.get(plugin_id).await {
            Some((_, mut d)) => {
                ensure_descriptor_arrays(&mut d);
                Json(d).into_response()
            }
            None => (
                StatusCode::NOT_FOUND,
                Json(json!({"error": "plugin not found"})),
            )
                .into_response(),
        };
    }
    match parts.get(1).copied() {
        Some("config") => handle_plugin_config_get(&state, plugin_id).await,
        Some("control") => {
            let cfg = state.store.get();
            let val = cfg
                .plugin_controls
                .get(plugin_id)
                .cloned()
                .unwrap_or_default();
            Json(val).into_response()
        }
        _ => (StatusCode::NOT_FOUND, Json(json!({"error": "not found"}))).into_response(),
    }
}

pub async fn api_plugin_sub_put(
    State(state): State<Arc<WebInner>>,
    Path(rest): Path<String>,
    Json(body): Json<Value>,
) -> Response {
    let parts: Vec<&str> = rest.trim_matches('/').split('/').filter(|p| !p.is_empty()).collect();
    if parts.len() < 2 {
        return (StatusCode::NOT_FOUND, Json(json!({"error": "not found"}))).into_response();
    }
    let plugin_id = parts[0];
    match parts[1] {
        "config" => handle_plugin_config_put(&state, plugin_id, body).await,
        "switches" => handle_plugin_switches_put(&state, plugin_id, body).await,
        "control" => match serde_json::from_value::<PluginControl>(body.clone()) {
            Ok(ctrl) => match state.store.update(|cfg| {
                cfg.plugin_controls.insert(plugin_id.to_string(), ctrl);
            }) {
                Ok(_) => Json(body).into_response(),
                Err(err) => (
                    StatusCode::BAD_REQUEST,
                    Json(json!({"error": err.to_string()})),
                )
                    .into_response(),
            },
            Err(err) => (
                StatusCode::BAD_REQUEST,
                Json(json!({"error": err.to_string()})),
            )
                .into_response(),
        },
        _ => (StatusCode::NOT_FOUND, Json(json!({"error": "not found"}))).into_response(),
    }
}

