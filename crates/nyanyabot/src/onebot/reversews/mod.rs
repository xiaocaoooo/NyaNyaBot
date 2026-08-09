use std::collections::HashMap;
use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;

use anyhow::{Result, anyhow, bail};
use axum::Router;
use axum::extract::State;
use axum::extract::ws::{Message, WebSocket, WebSocketUpgrade};
use axum::response::IntoResponse;
use axum::routing::get;
use futures_util::{SinkExt, StreamExt};
use parking_lot::RwLock;
use serde::Serialize;
use serde_json::{Value, json};
use tokio::sync::{Mutex, mpsc, oneshot};
use tracing::{info, warn};
use uuid::Uuid;

use crate::config::Store;
use crate::onebot::ob11::{ApiRequest, ApiResponse};

pub type EventHandler = Arc<dyn Fn(Value) + Send + Sync>;

#[derive(Clone, Debug, Serialize)]
pub struct Group {
    pub group_id: i64,
    pub group_name: String,
    pub member_count: i64,
    pub max_member_count: i64,
}

#[derive(Clone, Debug, Serialize)]
pub struct BotInfo {
    pub self_id: i64,
    pub nickname: String,
    pub remote_addr: String,
    pub connected_at: chrono::DateTime<chrono::Utc>,
    pub group_count: usize,
    pub groups: Vec<Group>,
}

struct Session {
    self_id: i64,
    nickname: String,
    remote: String,
    connected_at: chrono::DateTime<chrono::Utc>,
    groups: Vec<Group>,
    tx: mpsc::UnboundedSender<String>,
    pending: Mutex<HashMap<String, oneshot::Sender<ApiResponse>>>,
}

#[derive(Clone)]
pub struct Server {
    store: Arc<Store>,
    handler: Arc<RwLock<Option<EventHandler>>>,
    sessions: Arc<RwLock<HashMap<i64, Arc<Session>>>>,
    default_bot: Arc<RwLock<Option<i64>>>,
    shutdown: Arc<tokio::sync::Notify>,
}

impl Server {
    pub fn new(store: Arc<Store>) -> Arc<Self> {
        Arc::new(Self {
            store,
            handler: Arc::new(RwLock::new(None)),
            sessions: Arc::new(RwLock::new(HashMap::new())),
            default_bot: Arc::new(RwLock::new(None)),
            shutdown: Arc::new(tokio::sync::Notify::new()),
        })
    }

    pub fn set_handler<F>(&self, f: F)
    where
        F: Fn(Value) + Send + Sync + 'static,
    {
        *self.handler.write() = Some(Arc::new(f));
    }

    pub fn get_bot_ids(&self) -> Vec<i64> {
        let mut ids: Vec<_> = self.sessions.read().keys().copied().collect();
        ids.sort_unstable();
        ids
    }

    pub fn get_bots(&self) -> Vec<BotInfo> {
        self.sessions
            .read()
            .values()
            .map(|s| BotInfo {
                self_id: s.self_id,
                nickname: s.nickname.clone(),
                remote_addr: s.remote.clone(),
                connected_at: s.connected_at,
                group_count: s.groups.len(),
                groups: s.groups.clone(),
            })
            .collect()
    }

    pub async fn call(&self, action: &str, params: Value) -> Result<ApiResponse> {
        let ids = self.get_bot_ids();
        match ids.as_slice() {
            [] => bail!("no onebot connection"),
            [bot] => self.call_with_bot(*bot, action, params).await,
            _ => bail!("multiple bots connected, use CallWithBot to specify self_id"),
        }
    }

    pub async fn call_with_bot(
        &self,
        self_id: i64,
        action: &str,
        params: Value,
    ) -> Result<ApiResponse> {
        let session = self
            .sessions
            .read()
            .get(&self_id)
            .cloned()
            .ok_or_else(|| anyhow!("bot not connected: {self_id}"))?;
        call_raw(&session, action, params).await
    }

    pub async fn start(self: Arc<Self>) -> Result<()> {
        let addr: SocketAddr = self
            .store
            .get()
            .onebot
            .reverse_ws
            .listen_addr
            .parse()
            .map_err(|e| anyhow!("invalid reverse ws addr: {e}"))?;
        let app = Router::new()
            .route("/", get(ws_upgrade))
            .route("/onebot/v11/ws", get(ws_upgrade))
            .with_state(self.clone());
        let listener = tokio::net::TcpListener::bind(addr).await?;
        info!(%addr, "onebot reverse ws listening");
        let this = self.clone();
        axum::serve(
            listener,
            app.into_make_service_with_connect_info::<SocketAddr>(),
        )
        .with_graceful_shutdown(async move {
            this.shutdown.notified().await;
        })
        .await?;
        Ok(())
    }

    pub async fn shutdown(&self) {
        self.shutdown.notify_waiters();
    }
}

async fn ws_upgrade(
    ws: WebSocketUpgrade,
    State(server): State<Arc<Server>>,
    axum::extract::ConnectInfo(addr): axum::extract::ConnectInfo<SocketAddr>,
) -> impl IntoResponse {
    let remote = addr.to_string();
    ws.on_upgrade(move |socket| handle_socket(server, socket, remote))
}

async fn handle_socket(server: Arc<Server>, socket: WebSocket, remote: String) {
    let (mut sink, mut stream) = socket.split();
    let (tx, mut rx) = mpsc::unbounded_channel::<String>();

    let write_task = tokio::spawn(async move {
        while let Some(msg) = rx.recv().await {
            if sink.send(Message::Text(msg.into())).await.is_err() {
                break;
            }
        }
    });

    let session = Arc::new(Session {
        self_id: 0,
        nickname: String::new(),
        remote: remote.clone(),
        connected_at: chrono::Utc::now(),
        groups: Vec::new(),
        tx: tx.clone(),
        pending: Mutex::new(HashMap::new()),
    });

    // Bootstrap APIs first via temporary reader task using shared pending map.
    let bootstrap_session = session.clone();
    let server_bg = server.clone();
    let reader = tokio::spawn(async move {
        let mut identified = 0i64;
        while let Some(Ok(msg)) = stream.next().await {
            let data = match msg {
                Message::Text(t) => t.as_bytes().to_vec(),
                Message::Binary(b) => b.to_vec(),
                Message::Close(_) => break,
                _ => continue,
            };
            let probe: Value = match serde_json::from_slice(&data) {
                Ok(v) => v,
                Err(_) => continue,
            };
            let echo = probe.get("echo").and_then(|v| v.as_str()).unwrap_or("");
            let status = probe.get("status").and_then(|v| v.as_str()).unwrap_or("");
            if !echo.is_empty()
                && !status.is_empty()
                && let Ok(resp) = serde_json::from_value::<ApiResponse>(probe.clone())
            {
                if let Some(tx) = bootstrap_session.pending.lock().await.remove(echo) {
                    let _ = tx.send(resp);
                } else if identified != 0 {
                    let sess = server_bg.sessions.read().get(&identified).cloned();
                    if let Some(sess) = sess
                        && let Some(tx) = sess.pending.lock().await.remove(echo)
                    {
                        let _ = tx.send(resp);
                    }
                }
                continue;
            }

            let mut event = probe;
            if identified != 0 {
                event = ensure_self_id(event, identified);
            } else if let Some(id) = event_self_id(&event) {
                identified = id;
            }
            if let Some(handler) = server_bg.handler.read().clone() {
                handler(event);
            }
        }
    });

    let login = match call_raw(&session, "get_login_info", Value::Null).await {
        Ok(r) => r,
        Err(err) => {
            warn!(error = %err, "get_login_info failed");
            write_task.abort();
            reader.abort();
            return;
        }
    };
    let user_id = json_i64(&login.data, "user_id");
    let nickname = login
        .data
        .get("nickname")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();
    if user_id == 0 {
        warn!("invalid login info");
        write_task.abort();
        reader.abort();
        return;
    }

    let cfg = server.store.get();
    if !cfg.bot_access.allowed(user_id) {
        warn!(self_id = user_id, "bot rejected by access control");
        if cfg.bot_access.reject_behavior == "ignore" {
            let _ = reader.await;
            write_task.abort();
            return;
        }
        write_task.abort();
        reader.abort();
        return;
    }

    let mut groups = Vec::new();
    if let Ok(resp) = call_raw(&session, "get_group_list", Value::Null).await
        && let Some(arr) = resp.data.as_array()
    {
        for g in arr {
            groups.push(Group {
                group_id: json_i64(g, "group_id"),
                group_name: g
                    .get("group_name")
                    .and_then(|v| v.as_str())
                    .unwrap_or("")
                    .to_string(),
                member_count: json_i64(g, "member_count"),
                max_member_count: json_i64(g, "max_member_count"),
            });
        }
    }

    let live = Arc::new(Session {
        self_id: user_id,
        nickname: nickname.clone(),
        remote,
        connected_at: chrono::Utc::now(),
        groups,
        tx,
        pending: Mutex::new(HashMap::new()),
    });
    // Move any remaining pending waiters are on bootstrap session; fine for handshake completion.
    server.sessions.write().insert(user_id, live);
    {
        let mut def = server.default_bot.write();
        if def.is_none() {
            *def = Some(user_id);
        }
    }
    info!(self_id = user_id, %nickname, "bot identified");

    let _ = reader.await;
    write_task.abort();
    server.sessions.write().remove(&user_id);
    {
        let mut def = server.default_bot.write();
        if *def == Some(user_id) {
            *def = server.sessions.read().keys().next().copied();
        }
    }
    info!(self_id = user_id, "bot disconnected");
}

async fn call_raw(session: &Session, action: &str, params: Value) -> Result<ApiResponse> {
    let echo = Uuid::new_v4().to_string();
    let (tx, rx) = oneshot::channel();
    session.pending.lock().await.insert(echo.clone(), tx);
    let req = ApiRequest {
        action: action.to_string(),
        params: if params.is_null() { None } else { Some(params) },
        echo: Some(echo.clone()),
    };
    session
        .tx
        .send(serde_json::to_string(&req)?)
        .map_err(|_| anyhow!("ws send failed"))?;
    match tokio::time::timeout(Duration::from_secs(30), rx).await {
        Ok(Ok(resp)) => Ok(resp),
        Ok(Err(_)) => bail!("canceled"),
        Err(_) => {
            session.pending.lock().await.remove(&echo);
            bail!("timeout")
        }
    }
}

fn json_i64(v: &Value, key: &str) -> i64 {
    v.get(key).map(value_as_i64).unwrap_or(0)
}

fn value_as_i64(v: &Value) -> i64 {
    v.as_i64()
        .or_else(|| v.as_u64().map(|n| n as i64))
        .or_else(|| v.as_f64().map(|n| n as i64))
        .or_else(|| v.as_str().and_then(|s| s.trim().parse().ok()))
        .unwrap_or(0)
}

fn event_self_id(event: &Value) -> Option<i64> {
    let n = json_i64(event, "self_id");
    if n == 0 { None } else { Some(n) }
}

/// Apply Go ensureSelfID: when connection is identified, always overwrite self_id.
fn ensure_self_id(mut event: Value, self_id: i64) -> Value {
    if self_id != 0
        && let Some(obj) = event.as_object_mut()
    {
        obj.insert("self_id".into(), json!(self_id));
    }
    event
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn parses_string_ids() {
        assert_eq!(json_i64(&json!({"user_id": "12345"}), "user_id"), 12345);
        assert_eq!(json_i64(&json!({"group_id": 9}), "group_id"), 9);
        assert_eq!(event_self_id(&json!({"self_id": "42"})), Some(42));
        assert_eq!(event_self_id(&json!({"self_id": 0})), None);
    }

    #[test]
    fn ensure_self_id_overwrites_nonzero() {
        let ev = ensure_self_id(json!({"self_id": 1, "post_type": "message"}), 99);
        assert_eq!(ev["self_id"], 99);
        assert_eq!(ev["post_type"], "message");
    }
}
