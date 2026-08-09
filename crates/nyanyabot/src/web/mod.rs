use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};

use axum::body::Body;
use axum::extract::{Path, State};
use axum::http::{HeaderMap, Request, StatusCode, header};
use axum::middleware::{Next, from_fn_with_state};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use base64::Engine;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use parking_lot::Mutex;
use rand::RngCore;
use rust_embed::Embed;
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;
use tracing::info;

use crate::config::Store;
use crate::onebot::reversews::Server as ReverseWsServer;
use crate::plugin::Manager;
use crate::pluginhost::PluginHost;
use crate::stats::Stats;
use crate::triggerlog::Recorder as TriggerRecorder;

const SESSION_COOKIE: &str = "nyanyabot_session";
const SESSION_MAX_AGE: u64 = 30 * 24 * 60 * 60;

#[derive(Embed)]
#[folder = "generated/frontend"]
struct FrontendAssets;

#[derive(Clone)]
pub struct WebServer {
    inner: Arc<WebInner>,
}

struct WebInner {
    store: Arc<Store>,
    pm: Arc<Manager>,
    stats: Arc<Stats>,
    host: Arc<PluginHost>,
    reverse_ws: Arc<ReverseWsServer>,
    trigger: Arc<TriggerRecorder>,
    sessions: Mutex<HashMap<String, Instant>>,
    shutdown: tokio::sync::Notify,
}

impl WebServer {
    pub fn new(
        store: Arc<Store>,
        pm: Arc<Manager>,
        stats: Arc<Stats>,
        host: Arc<PluginHost>,
        reverse_ws: Arc<ReverseWsServer>,
        trigger: Arc<TriggerRecorder>,
    ) -> Arc<Self> {
        Arc::new(Self {
            inner: Arc::new(WebInner {
                store,
                pm,
                stats,
                host,
                reverse_ws,
                trigger,
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
            .route("/api/plugins", get(api_plugins))
            .route(
                "/api/plugins/{*rest}",
                get(api_plugin_sub).put(api_plugin_sub_put),
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
    (
        StatusCode::FOUND,
        [(header::LOCATION, loc)],
    )
        .into_response()
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
        if let Some(v) = part.strip_prefix(&(SESSION_COOKIE.to_string() + "=")) {
            let mut sessions = state.sessions.lock();
            cleanup_sessions(&mut sessions);
            if let Some(exp) = sessions.get(v) {
                return Instant::now() < *exp;
            }
        }
    }
    false
}

fn cleanup_sessions(sessions: &mut HashMap<String, Instant>) {
    let now = Instant::now();
    sessions.retain(|_, exp| now < *exp);
}

fn create_session(state: &WebInner) -> String {
    let mut bytes = [0u8; 32];
    rand::rng().fill_bytes(&mut bytes);
    let id = URL_SAFE_NO_PAD.encode(bytes);
    let mut sessions = state.sessions.lock();
    cleanup_sessions(&mut sessions);
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
    match state.store.update(|cfg| {
        if let Ok(new_cfg) = serde_json::from_value::<crate::config::AppConfig>(body.clone()) {
            *cfg = new_cfg;
        }
    }) {
        Ok(cfg) => {
            let _ = state.host.reconfigure_all().await;
            Json(json!(cfg)).into_response()
        }
        Err(err) => (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": err.to_string()})),
        )
            .into_response(),
    }
}

async fn api_globals(State(state): State<Arc<WebInner>>) -> impl IntoResponse {
    Json(state.store.get().globals)
}

async fn api_globals_put(
    State(state): State<Arc<WebInner>>,
    Json(body): Json<HashMap<String, String>>,
) -> impl IntoResponse {
    match state.store.update(|cfg| cfg.globals = body) {
        Ok(cfg) => {
            let _ = state.host.reconfigure_all().await;
            Json(cfg.globals).into_response()
        }
        Err(err) => (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": err.to_string()})),
        )
            .into_response(),
    }
}

async fn api_plugin_env(State(state): State<Arc<WebInner>>) -> impl IntoResponse {
    Json(state.store.get().plugin_env)
}

async fn api_plugin_env_put(
    State(state): State<Arc<WebInner>>,
    Json(body): Json<HashMap<String, String>>,
) -> impl IntoResponse {
    match state.store.update(|cfg| cfg.plugin_env = body) {
        Ok(cfg) => {
            let _ = state.host.restart_plugins(None).await;
            Json(cfg.plugin_env).into_response()
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
    Json(json!({"bots": state.reverse_ws.get_bots()}))
}

async fn api_plugins(State(state): State<Arc<WebInner>>) -> impl IntoResponse {
    let list = state.pm.list().await;
    Json(json!({"plugins": list}))
}

async fn api_plugin_sub(State(state): State<Arc<WebInner>>, Path(rest): Path<String>) -> Response {
    // rest like "{pluginID}/config" or "{pluginID}"
    let parts: Vec<&str> = rest.trim_matches('/').split('/').collect();
    if parts.is_empty() {
        return (StatusCode::NOT_FOUND, Json(json!({"error":"not found"}))).into_response();
    }
    let plugin_id = parts[0];
    if parts.len() == 1 {
        return match state.pm.get(plugin_id).await {
            Some((_, d)) => Json(d).into_response(),
            None => (
                StatusCode::NOT_FOUND,
                Json(json!({"error":"plugin not found"})),
            )
                .into_response(),
        };
    }
    if parts.get(1) == Some(&"config") {
        let cfg = state.store.get();
        let val = cfg.plugins.get(plugin_id).cloned().unwrap_or(json!({}));
        return Json(val).into_response();
    }
    if parts.get(1) == Some(&"control") {
        let cfg = state.store.get();
        let val = cfg
            .plugin_controls
            .get(plugin_id)
            .cloned()
            .unwrap_or_default();
        return Json(val).into_response();
    }
    (StatusCode::NOT_FOUND, Json(json!({"error":"not found"}))).into_response()
}

async fn api_plugin_sub_put(
    State(state): State<Arc<WebInner>>,
    Path(rest): Path<String>,
    Json(body): Json<Value>,
) -> Response {
    let parts: Vec<&str> = rest.trim_matches('/').split('/').collect();
    if parts.len() < 2 {
        return (StatusCode::NOT_FOUND, Json(json!({"error":"not found"}))).into_response();
    }
    let plugin_id = parts[0].to_string();
    match parts[1] {
        "config" => {
            match state.store.update(|cfg| {
                cfg.plugins.insert(plugin_id.clone(), body.clone());
            }) {
                Ok(_) => {
                    let _ = state.host.reconfigure_plugin(&plugin_id).await;
                    Json(body).into_response()
                }
                Err(err) => (
                    StatusCode::BAD_REQUEST,
                    Json(json!({"error": err.to_string()})),
                )
                    .into_response(),
            }
        }
        "control" => match serde_json::from_value::<crate::config::PluginControl>(body.clone()) {
            Ok(ctrl) => match state.store.update(|cfg| {
                cfg.plugin_controls.insert(plugin_id.clone(), ctrl);
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
        _ => (StatusCode::NOT_FOUND, Json(json!({"error":"not found"}))).into_response(),
    }
}

async fn api_trigger_logs(State(state): State<Arc<WebInner>>) -> impl IntoResponse {
    Json(
        json!({"items": state.trigger.list_recent(100).await, "stats": state.trigger.stats().await}),
    )
}

async fn api_trigger_logs_stats(State(state): State<Arc<WebInner>>) -> impl IntoResponse {
    Json(state.trigger.stats().await)
}

async fn api_info(State(state): State<Arc<WebInner>>) -> impl IntoResponse {
    let cfg = state.store.get();
    Json(json!({
        "name": "NyaNyaBot",
        "version": env!("CARGO_PKG_VERSION"),
        "bots": state.reverse_ws.get_bot_ids(),
        "plugins": state.pm.plugin_ids().await,
        "webui": {
            "auto_refresh": cfg.webui.auto_refresh,
            "refresh_interval": cfg.webui.refresh_interval,
        }
    }))
}

async fn static_or_spa(req: Request<Body>) -> Response {
    let path = req.uri().path();
    for candidate in frontend_asset_candidates(path) {
        if let Some(file) = FrontendAssets::get(&candidate) {
            return embedded_file_response(&candidate, file.data.into_owned(), StatusCode::OK);
        }
    }

    // Static export is multi-page; unknown routes should be 404, not the dashboard shell.
    // Falling back to index.html previously served the home page for "/login/" and broke auth UX.
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
