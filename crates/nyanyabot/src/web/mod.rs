use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};

use axum::body::Body;
use axum::extract::{Query, State};
use axum::http::{HeaderMap, Request, StatusCode, header};
use axum::middleware::{Next, from_fn_with_state};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use base64::Engine;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use chrono::{DateTime, Utc};
use parking_lot::Mutex;
use rand::RngCore;
use rust_embed::Embed;
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;
use tracing::info;

use crate::chatlog::Recorder as ChatRecorder;
use crate::config::{
    Store, apply_config_patch, normalize_string_map, string_maps_equal, validate_env_key,
};
use crate::onebot::reversews::Server as ReverseWsServer;
use crate::plugin::Manager;
use crate::pluginhost::PluginHost;
use crate::stats::Stats;
use crate::triggerlog::{PluginTriggerLogQuery, Recorder as TriggerRecorder};

mod plugins_api;

const SESSION_COOKIE: &str = "nyanyabot_session";
const SESSION_MAX_AGE: u64 = 30 * 24 * 60 * 60;

#[derive(Embed)]
#[folder = "generated/frontend"]
struct FrontendAssets;

#[derive(Clone)]
pub struct WebServer {
    inner: Arc<WebInner>,
}

pub(super) struct WebInner {
    pub(super) store: Arc<Store>,
    pub(super) pm: Arc<Manager>,
    pub(super) stats: Arc<Stats>,
    pub(super) host: Arc<PluginHost>,
    pub(super) reverse_ws: Arc<ReverseWsServer>,
    pub(super) trigger: Arc<TriggerRecorder>,
    pub(super) chatlog: Option<Arc<ChatRecorder>>,
    pub(super) sessions: Mutex<HashMap<String, Instant>>,
    pub(super) shutdown: tokio::sync::Notify,
}

impl WebServer {
    pub fn new(
        store: Arc<Store>,
        pm: Arc<Manager>,
        stats: Arc<Stats>,
        host: Arc<PluginHost>,
        reverse_ws: Arc<ReverseWsServer>,
        trigger: Arc<TriggerRecorder>,
        chatlog: Option<Arc<ChatRecorder>>,
    ) -> Arc<Self> {
        Arc::new(Self {
            inner: Arc::new(WebInner {
                store,
                pm,
                stats,
                host,
                reverse_ws,
                trigger,
                chatlog,
                sessions: Mutex::new(HashMap::new()),
                shutdown: tokio::sync::Notify::new(),
            }),
        })
    }

    pub fn addr(&self) -> String {
        self.inner.store.get().webui.listen_addr
    }

    pub async fn serve(self: Arc<Self>) -> anyhow::Result<()> {
        let addr = self.addr();
        let app = self.router();
        let listener = tokio::net::TcpListener::bind(&addr).await?;
        info!(%addr, "webui server bound");
        axum::serve(listener, app)
            .with_graceful_shutdown(async move {
                self.inner.shutdown.notified().await;
            })
            .await?;
        Ok(())
    }

    pub async fn shutdown(&self) {
        self.inner.shutdown.notify_waiters();
    }

    fn router(self: &Arc<Self>) -> Router {
        let state = self.inner.clone();
        Router::new()
            .route("/api/auth/login", post(api_login))
            .route("/api/auth/logout", post(api_logout))
            .route("/api/auth/status", get(api_auth_status))
            .route("/api/config", get(api_config).put(api_config_put))
            .route("/api/globals", get(api_globals).put(api_globals_put))
            .route(
                "/api/plugin-env",
                get(api_plugin_env).put(api_plugin_env_put),
            )
            .route("/api/stats", get(api_stats))
            .route("/api/bots", get(api_bots))
            .route("/api/plugins", get(plugins_api::api_plugins))
            .route(
                "/api/plugins/{*rest}",
                get(plugins_api::api_plugin_sub)
                    .put(plugins_api::api_plugin_sub_put)
                    .post(plugins_api::api_plugin_sub_post),
            )
            .route("/api/trigger-logs", get(api_trigger_logs))
            .route("/api/trigger-logs/stats", get(api_trigger_logs_stats))
            .route("/api/info", get(api_info))
            .fallback(static_or_spa)
            .layer(from_fn_with_state(state.clone(), auth_middleware))
            .with_state(state)
    }
}

async fn auth_middleware(
    State(state): State<Arc<WebInner>>,
    req: Request<Body>,
    next: Next,
) -> Response {
    let path = req.uri().path().to_string();
    if is_public_path(&path) || is_authenticated(&state, &req) {
        return next.run(req).await;
    }
    if path.starts_with("/api/") {
        return (
            StatusCode::UNAUTHORIZED,
            Json(json!({"error":"unauthorized"})),
        )
            .into_response();
    }
    let next_q = req
        .uri()
        .path_and_query()
        .map(|p| p.as_str())
        .unwrap_or("/");
    let loc = format!("/login/?next={}", urlencoding_minimal(next_q));
    // Match historical Go behavior (302 Found) for unauthenticated HTML navigations.
    (StatusCode::FOUND, [(header::LOCATION, loc)]).into_response()
}

fn is_public_path(path: &str) -> bool {
    matches!(
        path,
        "/api/auth/login"
            | "/api/auth/logout"
            | "/api/auth/status"
            | "/favicon.ico"
            | "/robots.txt"
    ) || path.starts_with("/api/auth/")
        || path == "/login"
        || path == "/login/"
        || path.starts_with("/login/")
        || path.starts_with("/_next/")
        || path.starts_with("/assets/")
}

fn is_authenticated(state: &WebInner, req: &Request<Body>) -> bool {
    let Some(cookie) = req
        .headers()
        .get(header::COOKIE)
        .and_then(|v| v.to_str().ok())
    else {
        return false;
    };
    for part in cookie.split(';') {
        let part = part.trim();
        let Some(v) = part.strip_prefix(&format!("{SESSION_COOKIE}=")) else {
            continue;
        };
        let mut sessions = state.sessions.lock();
        if let Some(exp) = sessions.get(v).copied() {
            if Instant::now() <= exp {
                return true;
            }
            sessions.remove(v);
        }
    }
    false
}

fn create_session(state: &WebInner) -> String {
    let mut raw = [0u8; 32];
    rand::rng().fill_bytes(&mut raw);
    let id = URL_SAFE_NO_PAD.encode(raw);
    let mut sessions = state.sessions.lock();
    sessions.retain(|_, exp| *exp > Instant::now());
    sessions.insert(
        id.clone(),
        Instant::now() + Duration::from_secs(SESSION_MAX_AGE),
    );
    id
}

#[derive(Deserialize)]
struct LoginBody {
    password: String,
}

async fn api_login(State(state): State<Arc<WebInner>>, Json(body): Json<LoginBody>) -> Response {
    let expected = state.store.get().webui.password;
    if !secure_eq(&body.password, &expected) {
        return (
            StatusCode::UNAUTHORIZED,
            Json(json!({"error":"invalid password"})),
        )
            .into_response();
    }
    let sid = create_session(&state);
    let mut headers = HeaderMap::new();
    headers.insert(
        header::SET_COOKIE,
        format!(
            "{SESSION_COOKIE}={sid}; Path=/; HttpOnly; SameSite=Lax; Max-Age={SESSION_MAX_AGE}"
        )
        .parse()
        .unwrap(),
    );
    (headers, Json(json!({"ok": true}))).into_response()
}

async fn api_logout(State(state): State<Arc<WebInner>>, req: Request<Body>) -> Response {
    if let Some(cookie) = req
        .headers()
        .get(header::COOKIE)
        .and_then(|v| v.to_str().ok())
    {
        for part in cookie.split(';') {
            let part = part.trim();
            if let Some(v) = part.strip_prefix(&format!("{SESSION_COOKIE}=")) {
                state.sessions.lock().remove(v);
            }
        }
    }
    let mut headers = HeaderMap::new();
    headers.insert(
        header::SET_COOKIE,
        format!("{SESSION_COOKIE}=; Path=/; Max-Age=0")
            .parse()
            .unwrap(),
    );
    (headers, Json(json!({"ok": true}))).into_response()
}

async fn api_auth_status(
    State(state): State<Arc<WebInner>>,
    req: Request<Body>,
) -> impl IntoResponse {
    Json(json!({"authenticated": is_authenticated(&state, &req)}))
}

async fn api_config(State(state): State<Arc<WebInner>>) -> impl IntoResponse {
    Json(state.store.get())
}

async fn api_config_put(
    State(state): State<Arc<WebInner>>,
    Json(body): Json<Value>,
) -> impl IntoResponse {
    let prev = state.store.get();
    let mut next = prev.clone();
    if let Err(err) = apply_config_patch(&mut next, &body) {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": err.to_string()})),
        )
            .into_response();
    }
    match state.store.update(|cfg| {
        *cfg = next.clone();
    }) {
        Ok(cfg) => {
            if let Err(err) = apply_config_patch_side_effects(&state, &prev, &cfg).await {
                return (
                    StatusCode::INTERNAL_SERVER_ERROR,
                    Json(json!({"error": err.to_string()})),
                )
                    .into_response();
            }
            let _ = state.host.reconfigure_all().await;
            Json(cfg).into_response()
        }
        Err(err) => (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": err.to_string()})),
        )
            .into_response(),
    }
}

async fn apply_config_patch_side_effects(
    state: &WebInner,
    prev: &crate::config::AppConfig,
    cfg: &crate::config::AppConfig,
) -> anyhow::Result<()> {
    let chat_changed = prev.chat_log.database_uri != cfg.chat_log.database_uri;
    if chat_changed && let Some(chat) = &state.chatlog {
        chat.reconnect(cfg.chat_log.database_uri.clone()).await?;
    }

    let trigger_changed = prev.trigger_log.enabled != cfg.trigger_log.enabled
        || prev.trigger_log.database_uri != cfg.trigger_log.database_uri
        || prev.trigger_log.queue_size != cfg.trigger_log.queue_size
        || prev.trigger_log.batch_size != cfg.trigger_log.batch_size
        || prev.trigger_log.batch_interval != cfg.trigger_log.batch_interval;
    if trigger_changed {
        state
            .trigger
            .reconnect(
                cfg.trigger_log.database_uri.clone(),
                cfg.trigger_log.enabled,
            )
            .await?;
    }
    Ok(())
}

async fn api_globals(State(state): State<Arc<WebInner>>) -> impl IntoResponse {
    Json(json!({"globals": state.store.get().globals}))
}

#[derive(Deserialize)]
struct GlobalsPutBody {
    #[serde(default)]
    globals: HashMap<String, String>,
}

async fn api_globals_put(
    State(state): State<Arc<WebInner>>,
    Json(body): Json<GlobalsPutBody>,
) -> impl IntoResponse {
    let globals = normalize_string_map(&body.globals);
    match state.store.update(|cfg| cfg.globals = globals) {
        Ok(cfg) => {
            let _ = state.host.reconfigure_all().await;
            Json(json!({"ok": true, "globals": cfg.globals})).into_response()
        }
        Err(err) => (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": err.to_string()})),
        )
            .into_response(),
    }
}

async fn api_plugin_env(State(state): State<Arc<WebInner>>) -> impl IntoResponse {
    Json(json!({"plugin_env": state.store.get().plugin_env}))
}

#[derive(Deserialize)]
struct PluginEnvPutBody {
    #[serde(default)]
    plugin_env: HashMap<String, String>,
}

async fn api_plugin_env_put(
    State(state): State<Arc<WebInner>>,
    Json(body): Json<PluginEnvPutBody>,
) -> impl IntoResponse {
    for key in body.plugin_env.keys() {
        if let Err(err) = validate_env_key(key.trim()) {
            return (
                StatusCode::BAD_REQUEST,
                Json(json!({"error": err.to_string()})),
            )
                .into_response();
        }
    }
    let normalized = normalize_string_map(&body.plugin_env);
    let prev = state.store.get().plugin_env;
    match state.store.update(|cfg| cfg.plugin_env = normalized) {
        Ok(cfg) => {
            if !string_maps_equal(&prev, &cfg.plugin_env) {
                let _ = state.host.restart_plugins(None).await;
            }
            Json(json!({"ok": true, "plugin_env": cfg.plugin_env})).into_response()
        }
        Err(err) => (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": err.to_string()})),
        )
            .into_response(),
    }
}

async fn api_stats(State(state): State<Arc<WebInner>>) -> impl IntoResponse {
    Json(state.stats.snapshot())
}

async fn api_bots(State(state): State<Arc<WebInner>>) -> impl IntoResponse {
    let bots_raw = state.reverse_ws.get_bots();
    let mut total_groups = 0usize;
    let bots: Vec<Value> = bots_raw
        .into_iter()
        .map(|b| {
            total_groups += b.group_count;
            json!({
                "self_id": b.self_id,
                "nickname": b.nickname,
                "online": true,
                "remote_addr": b.remote_addr,
                "connected_at": b.connected_at.to_rfc3339(),
                "group_count": b.group_count,
                "groups": b.groups,
            })
        })
        .collect();
    let total_bots = bots.len();
    let snap = state.stats.snapshot();
    Json(json!({
        "bots": bots,
        "total_bots": total_bots,
        "online_bots": total_bots,
        "total_groups": total_groups,
        "group_chat_only": true,
        "dedupe_key": "group_id+real_seq",
        "stats": {
            "recv_count": snap.recv_count,
            "sent_count": snap.sent_count,
            "dedup_count": snap.dedup_count,
            "start_time": snap.start_time.to_rfc3339(),
            "uptime": snap.uptime,
        },
        "global_recv_count": snap.recv_count,
        "global_sent_count": snap.sent_count,
        "global_start_time": snap.start_time.to_rfc3339(),
        "global_uptime": snap.uptime,
    }))
}

#[derive(Debug, Deserialize, Default)]
struct TriggerLogsQueryParams {
    group_id: Option<i64>,
    user_id: Option<i64>,
    plugin_id: Option<String>,
    listener_id: Option<String>,
    listener_type: Option<String>,
    start_time: Option<String>,
    end_time: Option<String>,
    message_seq: Option<String>,
    trace_id: Option<String>,
    success: Option<bool>,
    sort_by: Option<String>,
    sort_desc: Option<bool>,
    page: Option<i64>,
    page_size: Option<i64>,
}

fn parse_trigger_query(params: TriggerLogsQueryParams) -> PluginTriggerLogQuery {
    PluginTriggerLogQuery {
        group_id: params.group_id,
        user_id: params.user_id,
        plugin_id: params.plugin_id.filter(|s| !s.is_empty()),
        listener_id: params.listener_id.filter(|s| !s.is_empty()),
        listener_type: params.listener_type.filter(|s| !s.is_empty()),
        trace_id: params.trace_id.filter(|s| !s.is_empty()),
        message_seq: params.message_seq.filter(|s| !s.is_empty()),
        success: params.success,
        start_time: params
            .start_time
            .as_deref()
            .and_then(|s| DateTime::parse_from_rfc3339(s).ok())
            .map(|dt| dt.with_timezone(&Utc)),
        end_time: params
            .end_time
            .as_deref()
            .and_then(|s| DateTime::parse_from_rfc3339(s).ok())
            .map(|dt| dt.with_timezone(&Utc)),
        sort_by: params.sort_by.unwrap_or_else(|| "triggered_at".into()),
        sort_desc: params.sort_desc.unwrap_or(true),
        page: params.page.unwrap_or(1),
        page_size: params.page_size.unwrap_or(20),
    }
    .normalize()
}

async fn api_trigger_logs(
    State(state): State<Arc<WebInner>>,
    Query(params): Query<TriggerLogsQueryParams>,
) -> impl IntoResponse {
    let query = parse_trigger_query(params);
    let (records, total) = state.trigger.query_logs(&query).await;
    let stats = state.trigger.query_stats(&query).await;
    Json(json!({
        "records": records,
        "total": total,
        "page": query.page,
        "page_size": query.page_size,
        "stats": stats,
    }))
}

async fn api_trigger_logs_stats(
    State(state): State<Arc<WebInner>>,
    Query(params): Query<TriggerLogsQueryParams>,
) -> impl IntoResponse {
    let query = parse_trigger_query(params);
    Json(state.trigger.query_stats(&query).await)
}

#[derive(Debug, Deserialize, Default)]
struct InfoQuery {
    id: Option<String>,
    #[serde(rename = "type")]
    type_: Option<String>,
    self_id: Option<i64>,
}

async fn api_info(State(state): State<Arc<WebInner>>, Query(params): Query<InfoQuery>) -> Response {
    let Some(id_str) = params.id.filter(|s| !s.is_empty()) else {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": "missing id or type"})),
        )
            .into_response();
    };
    let Some(type_str) = params.type_.filter(|s| !s.is_empty()) else {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": "missing id or type"})),
        )
            .into_response();
    };
    let Ok(id) = id_str.parse::<i64>() else {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": "invalid id"})),
        )
            .into_response();
    };

    let (action, call_params) = match type_str.as_str() {
        "user" => ("get_stranger_info", json!({"user_id": id})),
        "group" => ("get_group_info", json!({"group_id": id})),
        _ => {
            return (
                StatusCode::BAD_REQUEST,
                Json(json!({"error": "invalid type"})),
            )
                .into_response();
        }
    };

    let bot_ids = state.reverse_ws.get_bot_ids();
    if bot_ids.is_empty() {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(json!({"error": "onebot not connected"})),
        )
            .into_response();
    }

    let mut selected = bot_ids[0];
    if let Some(self_id) = params.self_id {
        selected = self_id;
    } else if type_str == "group" {
        for bot in state.reverse_ws.get_bots() {
            if bot.groups.iter().any(|g| g.group_id == id) {
                selected = bot.self_id;
                break;
            }
        }
    }

    let resp = match state
        .reverse_ws
        .call_with_bot(selected, action, call_params)
        .await
    {
        Ok(r) => r,
        Err(err) => {
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(json!({"error": err.to_string()})),
            )
                .into_response();
        }
    };
    if resp.status != "ok" {
        return (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(json!({"error": if resp.msg.is_empty() { "onebot call failed".into() } else { resp.msg.clone() }})),
        )
            .into_response();
    }

    let mut name = String::new();
    if type_str == "user" {
        if let Some(n) = resp.data.get("nickname").and_then(|v| v.as_str()) {
            name = n.to_string();
        }
    } else if let Some(n) = resp.data.get("group_name").and_then(|v| v.as_str()) {
        name = n.to_string();
    }
    if name.is_empty() {
        name = id_str;
    }
    Json(json!({"name": name})).into_response()
}

async fn static_or_spa(req: Request<Body>) -> Response {
    let path = req.uri().path();
    for candidate in frontend_asset_candidates(path) {
        if let Some(file) = FrontendAssets::get(&candidate) {
            return embedded_file_response(&candidate, file.data.into_owned(), StatusCode::OK);
        }
    }

    // Static export is multi-page; unknown routes should be 404, not the dashboard shell.
    if let Some(file) = FrontendAssets::get("404.html") {
        return embedded_file_response("404.html", file.data.into_owned(), StatusCode::NOT_FOUND);
    }
    (StatusCode::NOT_FOUND, "not found").into_response()
}

fn frontend_asset_candidates(uri_path: &str) -> Vec<String> {
    let path = uri_path.trim_start_matches('/');
    if path.is_empty() {
        return vec!["index.html".into()];
    }

    let mut candidates = vec![path.to_string()];
    if path.ends_with('/') {
        candidates.push(format!("{path}index.html"));
    } else {
        candidates.push(format!("{path}/index.html"));
        if !path.ends_with(".html") {
            candidates.push(format!("{path}.html"));
        }
    }
    candidates
}

fn embedded_file_response(path: &str, bytes: Vec<u8>, status: StatusCode) -> Response {
    let mime = mime_guess::from_path(path).first_or_octet_stream();
    let mut content_type = mime.essence_str().to_string();
    if content_type == "text/html" {
        content_type = "text/html; charset=utf-8".into();
    }
    Response::builder()
        .status(status)
        .header(header::CONTENT_TYPE, content_type)
        .body(Body::from(bytes))
        .unwrap()
}

fn secure_eq(a: &str, b: &str) -> bool {
    let ha = Sha256::digest(a.as_bytes());
    let hb = Sha256::digest(b.as_bytes());
    ha.ct_eq(&hb).into()
}

fn urlencoding_minimal(s: &str) -> String {
    url::form_urlencoded::byte_serialize(s.as_bytes()).collect()
}
