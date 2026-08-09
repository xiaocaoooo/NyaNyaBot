use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::sync::Arc;
use std::time::Duration;

use anyhow::{Context, Result, anyhow, bail};
use async_trait::async_trait;
use nyanyabot_proto::pb::host_service_server::HostServiceServer;
use nyanyabot_proto::pb::plugin_service_client::PluginServiceClient;
use nyanyabot_proto::pb::{
    AttachHostRequest, ConfigureRequest, DescribeRequest, HandleRequest, InvokeRequest,
    ShutdownRequest, StatusRequest,
};
use nyanyabot_proto::{
    CommandMatch, Descriptor, PROTOCOL_VERSION, Readiness, StructuredError, TokenInterceptor,
    ensure_descriptor_arrays, generate_token,
};
use serde_json::{Value, json};
use std::sync::RwLock;
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::process::{Child, Command};
use tokio::sync::{Mutex, mpsc};
use tonic::Request;
use tonic::transport::{Channel, Server};
use tracing::{error, info, warn};

use super::host_service::{HostServiceImpl, SharedHostState};
use super::lazy::LazyPlugin;
use crate::config::{AppConfig, Store, merge_process_env};
use crate::plugin::{Manager, Plugin};
use crate::stats::Stats;
use crate::triggerlog::Recorder as TriggerRecorder;

const START_TIMEOUT: Duration = Duration::from_secs(10);

type AuthedPluginClient =
    PluginServiceClient<tonic::service::interceptor::InterceptedService<Channel, TokenInterceptor>>;

#[allow(dead_code)]
struct RunningPlugin {
    plugin_id: String,
    exe_path: PathBuf,
    child: Child,
    client: AuthedPluginClient,
    plugin_token: String,
    host_token: String,
    descriptor: Descriptor,
    lazy: Arc<LazyPlugin>,
}

#[allow(dead_code)]
pub struct PluginHost {
    pm: Arc<Manager>,
    store: Arc<Store>,
    stats: Arc<Stats>,
    host_state: SharedHostState,
    host_addr: Arc<RwLock<String>>,
    running: Mutex<HashMap<String, RunningPlugin>>,
    by_path: Mutex<HashMap<PathBuf, String>>,
    trigger_recorder: RwLock<Option<Arc<TriggerRecorder>>>,
    traces: RwLock<HashMap<String, TraceRecord>>,
    restart_tx: mpsc::UnboundedSender<PathBuf>,
    _host_server: Mutex<Option<tokio::task::JoinHandle<()>>>,
    _restart_worker: Mutex<Option<tokio::task::JoinHandle<()>>>,
}

#[derive(Clone, Debug)]
#[allow(dead_code)]
pub struct TraceRecord {
    pub trace_id: String,
    pub plugin_id: String,
    pub listener_id: String,
    pub trace_type: String,
    pub data: Value,
    pub start: chrono::DateTime<chrono::Utc>,
}

impl PluginHost {
    pub async fn new(
        pm: Arc<Manager>,
        store: Arc<Store>,
        stats: Arc<Stats>,
        call_onebot: super::host_service::CallOneBotFn,
    ) -> Result<Arc<Self>> {
        let host_state = SharedHostState {
            plugin_manager: pm.clone(),
            stats: stats.clone(),
            tokens: Arc::new(RwLock::new(HashMap::new())),
            call_onebot,
            plugin_sent: Arc::new(RwLock::new(HashMap::new())),
        };

        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await?;
        let addr = listener.local_addr()?;
        let host_addr = format!("127.0.0.1:{}", addr.port());
        let incoming = tokio_stream::wrappers::TcpListenerStream::new(listener);
        let svc = HostServiceServer::new(HostServiceImpl {
            state: host_state.clone(),
        });
        let handle = tokio::spawn(async move {
            if let Err(err) = Server::builder()
                .add_service(svc)
                .serve_with_incoming(incoming)
                .await
            {
                error!(error = %err, "host gRPC server error");
            }
        });

        let (restart_tx, mut restart_rx) = mpsc::unbounded_channel::<PathBuf>();
        let host = Arc::new(Self {
            pm,
            store,
            stats,
            host_state,
            host_addr: Arc::new(RwLock::new(host_addr)),
            running: Mutex::new(HashMap::new()),
            by_path: Mutex::new(HashMap::new()),
            trigger_recorder: RwLock::new(None),
            traces: RwLock::new(HashMap::new()),
            restart_tx,
            _host_server: Mutex::new(Some(handle)),
            _restart_worker: Mutex::new(None),
        });
        let worker_host = Arc::clone(&host);
        let worker = tokio::spawn(async move {
            while let Some(exe) = restart_rx.recv().await {
                // small delay to avoid tight crash loops
                tokio::time::sleep(Duration::from_millis(200)).await;
                match worker_host.load_exec(&exe).await {
                    Ok(()) => info!(path = %exe.display(), "plugin auto-restarted"),
                    Err(err) => {
                        error!(path = %exe.display(), error = %err, "plugin auto-restart failed")
                    }
                }
            }
        });
        *host._restart_worker.lock().await = Some(worker);
        Ok(host)
    }

    pub fn set_trigger_recorder(&self, recorder: Arc<TriggerRecorder>) {
        *self.trigger_recorder.write().unwrap() = Some(recorder);
    }

    pub fn host_addr(&self) -> String {
        self.host_addr.read().unwrap().clone()
    }

    pub fn generate_trace_id(&self) -> String {
        uuid::Uuid::new_v4().to_string()
    }

    pub fn begin_trace(
        &self,
        trace_id: &str,
        plugin_id: &str,
        listener_id: &str,
        trace_type: &str,
        data: Value,
    ) {
        self.traces.write().unwrap().insert(
            trace_id.to_string(),
            TraceRecord {
                trace_id: trace_id.to_string(),
                plugin_id: plugin_id.to_string(),
                listener_id: listener_id.to_string(),
                trace_type: trace_type.to_string(),
                data,
                start: chrono::Utc::now(),
            },
        );
    }

    pub fn end_trace(&self, trace_id: &str) {
        if let Some(record) = self.traces.write().unwrap().remove(trace_id)
            && let Some(rec) = self.trigger_recorder.read().unwrap().clone()
        {
            let duration_ms = (chrono::Utc::now() - record.start).num_milliseconds();
            let data = record.data.clone();
            let self_id = data.get("self_id").and_then(|v| v.as_i64()).unwrap_or(0);
            let user_id = data.get("user_id").and_then(|v| v.as_i64()).unwrap_or(0);
            let group_id = data.get("group_id").and_then(|v| v.as_i64()).unwrap_or(0);
            let success = data
                .get("success")
                .and_then(|v| v.as_bool())
                .unwrap_or(true);
            let error = data
                .get("error")
                .and_then(|v| v.as_str())
                .or_else(|| data.get("error_message").and_then(|v| v.as_str()))
                .unwrap_or("")
                .to_string();
            let message_id = data.get("message_id").and_then(|v| v.as_i64()).unwrap_or(0);
            let message_seq = data
                .get("message_seq")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();
            let triggered_at = record.start;
            tokio::spawn(async move {
                rec.record_async(crate::triggerlog::TriggerRecord {
                    trace_id: record.trace_id,
                    plugin_id: record.plugin_id,
                    listener_id: record.listener_id,
                    listener_type: record.trace_type,
                    self_id,
                    user_id,
                    group_id,
                    message_id,
                    message_seq,
                    success,
                    error_message: error,
                    duration_ms,
                    trigger_data: record.data,
                    triggered_at,
                })
                .await;
            });
        }
    }

    pub async fn load_dir(self: &Arc<Self>, dir: impl AsRef<Path>) -> Result<()> {
        let dir = dir.as_ref();
        if !dir.exists() {
            info!(path = %dir.display(), "plugins dir missing; skip");
            return Ok(());
        }
        let mut paths = list_plugin_executables(dir)?;
        paths.sort();
        let mut errors = Vec::new();
        for path in paths {
            if let Err(err) = self.load_exec(&path).await {
                errors.push(format!("{}: {err}", path.display()));
            }
        }
        if errors.is_empty() {
            Ok(())
        } else {
            bail!("load_dir errors: {}", errors.join("; "))
        }
    }

    pub async fn load_exec(self: &Arc<Self>, exe_path: impl AsRef<Path>) -> Result<()> {
        let exe_path = exe_path.as_ref().to_path_buf();
        let running = self.start_plugin_process(&exe_path, "").await?;
        self.finish_load(running).await
    }

    async fn finish_load(self: &Arc<Self>, mut running: RunningPlugin) -> Result<()> {
        let host_addr = self.host_addr();
        running
            .client
            .attach_host(Request::new(AttachHostRequest {
                host_addr,
                token: running.host_token.clone(),
                plugin_id: running.plugin_id.clone(),
            }))
            .await
            .context("AttachHost")?;

        self.push_config(&mut running).await?;

        let desc = running.descriptor.clone();
        let plugin_id = running.plugin_id.clone();
        let lazy = running.lazy.clone();
        let plugin_arc: Arc<dyn Plugin> = lazy.clone();
        self.pm.register(plugin_arc, desc.clone()).await?;
        self.by_path
            .lock()
            .await
            .insert(running.exe_path.clone(), plugin_id.clone());
        self.running.lock().await.insert(plugin_id.clone(), running);

        let host = Arc::clone(self);
        let watch_id = plugin_id.clone();
        tokio::spawn(async move {
            host.watch_plugin(watch_id).await;
        });

        info!(
            plugin_id = %plugin_id,
            name = %desc.name,
            version = %desc.version,
            "plugin loaded"
        );
        Ok(())
    }

    async fn push_config(&self, running: &mut RunningPlugin) -> Result<()> {
        let cfg = self.store.get();
        let raw = cfg
            .plugins
            .get(&running.plugin_id)
            .cloned()
            .unwrap_or_else(|| json!({}));
        let (substituted, _) = crate::configtmpl::substitute_json(&raw, &cfg.globals);
        let config_json = serde_json::to_vec(&substituted)?;
        running
            .client
            .configure(Request::new(ConfigureRequest { config_json }))
            .await
            .context("Configure")?;
        Ok(())
    }

    async fn start_plugin_process(
        &self,
        exe_path: &Path,
        known_plugin_id: &str,
    ) -> Result<RunningPlugin> {
        let host_token = generate_token();
        let mut cmd = Command::new(exe_path);
        cmd.stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .kill_on_drop(true);

        let cfg = self.store.get();
        let plugin_env = if known_plugin_id.is_empty() {
            HashMap::new()
        } else {
            cfg.plugin_controls
                .get(known_plugin_id)
                .map(|c| c.env.clone())
                .unwrap_or_default()
        };
        let env = merge_process_env(
            std::env::vars().map(|(k, v)| format!("{k}={v}")),
            &cfg.plugin_env,
            &plugin_env,
        );
        cmd.env_clear();
        for item in env {
            if let Some((k, v)) = item.split_once('=') {
                cmd.env(k, v);
            }
        }

        let mut child = cmd
            .spawn()
            .with_context(|| format!("spawn {}", exe_path.display()))?;
        let stdout = child
            .stdout
            .take()
            .ok_or_else(|| anyhow!("missing plugin stdout"))?;
        let stderr = child.stderr.take();
        if let Some(stderr) = stderr {
            tokio::spawn(async move {
                let mut lines = BufReader::new(stderr).lines();
                while let Ok(Some(line)) = lines.next_line().await {
                    eprintln!("[plugin] {line}");
                }
            });
        }

        let readiness = tokio::time::timeout(START_TIMEOUT, read_readiness(stdout))
            .await
            .map_err(|_| anyhow!("plugin start timeout (10s): {}", exe_path.display()))?
            .with_context(|| format!("read readiness from {}", exe_path.display()))?;
        readiness
            .validate()
            .map_err(|e| anyhow!("invalid readiness: {e}"))?;
        if readiness.protocol_version != PROTOCOL_VERSION {
            bail!(
                "protocol mismatch: got {} want {}",
                readiness.protocol_version,
                PROTOCOL_VERSION
            );
        }

        let channel = Channel::from_shared(format!("http://{}", readiness.addr))?
            .connect()
            .await
            .with_context(|| format!("connect plugin at {}", readiness.addr))?;
        let mut client =
            PluginServiceClient::with_interceptor(channel, TokenInterceptor::new(&readiness.token));

        let desc_resp = client
            .describe(Request::new(DescribeRequest {}))
            .await
            .context("Describe")?
            .into_inner();
        let mut descriptor = Descriptor::from_json_bytes(&desc_resp.descriptor_json)
            .context("parse descriptor json")?;
        ensure_descriptor_arrays(&mut descriptor);
        if descriptor.plugin_id.trim().is_empty() {
            bail!("descriptor plugin_id empty");
        }
        let plugin_id = descriptor.plugin_id.clone();

        // Bind host token to this plugin identity for HostService auth.
        self.host_state.bind_token(&plugin_id, &host_token);

        let sleep_timeout = sleep_timeout_for(&cfg, &plugin_id);
        let rpc = Arc::new(RpcPlugin {
            client: Mutex::new(client.clone()),
        });
        let lazy = LazyPlugin::new(plugin_id.clone(), rpc, sleep_timeout);

        Ok(RunningPlugin {
            plugin_id,
            exe_path: exe_path.to_path_buf(),
            child,
            client,
            plugin_token: readiness.token,
            host_token,
            descriptor,
            lazy,
        })
    }

    async fn watch_plugin(self: Arc<Self>, plugin_id: String) {
        loop {
            let exited = {
                let mut guard = self.running.lock().await;
                let Some(p) = guard.get_mut(&plugin_id) else {
                    return;
                };
                match p.child.try_wait() {
                    Ok(Some(status)) => Some(status),
                    Ok(None) => None,
                    Err(err) => {
                        warn!(plugin_id = %plugin_id, error = %err, "try_wait failed");
                        None
                    }
                }
            };
            if let Some(status) = exited {
                warn!(plugin_id = %plugin_id, %status, "plugin process exited; removing");
                let exe = {
                    let guard = self.running.lock().await;
                    guard.get(&plugin_id).map(|p| p.exe_path.clone())
                };
                self.pm.unregister(&plugin_id).await;
                if let Some(old) = self.running.lock().await.remove(&plugin_id) {
                    self.host_state.unbind_token(&old.host_token);
                }
                if let Some(exe) = exe {
                    let _ = self.restart_tx.send(exe);
                }
                return;
            }
            if let Some(p) = self.running.lock().await.get(&plugin_id) {
                let _ = p.lazy.maybe_sleep();
            }
            tokio::time::sleep(Duration::from_secs(1)).await;
        }
    }

    /// OS pid of a running plugin process, if available.
    pub async fn plugin_os_pid(&self, plugin_id: &str) -> Option<u32> {
        let guard = self.running.lock().await;
        guard.get(plugin_id).and_then(|p| p.child.id())
    }

    /// Force-kill a plugin OS process without graceful Shutdown RPC.
    /// Used to exercise crash monitoring / auto-restart.
    pub async fn force_kill_plugin_process(&self, plugin_id: &str) -> Result<()> {
        let mut guard = self.running.lock().await;
        let Some(p) = guard.get_mut(plugin_id) else {
            bail!("plugin not running: {plugin_id}");
        };
        p.child.kill().await.context("force kill plugin")?;
        Ok(())
    }

    /// Whether the lazy wrapper currently marks the plugin as sleeping.
    pub async fn plugin_is_sleeping(&self, plugin_id: &str) -> bool {
        let guard = self.running.lock().await;
        guard
            .get(plugin_id)
            .map(|p| p.lazy.is_sleeping())
            .unwrap_or(false)
    }

    /// Override idle sleep timeout seconds for a running plugin (test/ops helper).
    pub async fn set_plugin_sleep_timeout(&self, plugin_id: &str, secs: i64) -> Result<()> {
        let guard = self.running.lock().await;
        let Some(p) = guard.get(plugin_id) else {
            bail!("plugin not running: {plugin_id}");
        };
        p.lazy.set_sleep_timeout(secs);
        Ok(())
    }

    pub async fn restart_plugin(self: &Arc<Self>, plugin_id: &str) -> Result<()> {
        let exe = {
            let guard = self.running.lock().await;
            guard.get(plugin_id).map(|p| p.exe_path.clone())
        };
        let Some(exe) = exe else {
            bail!("plugin not running: {plugin_id}");
        };
        self.stop_plugin(plugin_id).await?;
        let running = self.start_plugin_process(&exe, plugin_id).await?;
        self.finish_load(running).await
    }

    pub async fn restart_plugins(self: &Arc<Self>, plugin_ids: Option<Vec<String>>) -> Result<()> {
        let ids = match plugin_ids {
            Some(ids) if !ids.is_empty() => ids,
            _ => self.pm.plugin_ids().await,
        };
        for id in ids {
            if let Err(err) = self.restart_plugin(&id).await {
                warn!(plugin_id = %id, error = %err, "restart failed");
            }
        }
        Ok(())
    }

    pub async fn reconfigure_plugin(&self, plugin_id: &str) -> Result<()> {
        let mut guard = self.running.lock().await;
        let Some(running) = guard.get_mut(plugin_id) else {
            bail!("plugin not running: {plugin_id}");
        };
        self.push_config(running).await
    }

    pub async fn reconfigure_all(&self) -> Result<()> {
        let ids = self.pm.plugin_ids().await;
        for id in ids {
            if let Err(err) = self.reconfigure_plugin(&id).await {
                warn!(plugin_id = %id, error = %err, "reconfigure failed");
            }
        }
        Ok(())
    }

    async fn stop_plugin(&self, plugin_id: &str) -> Result<()> {
        let mut running = {
            let mut guard = self.running.lock().await;
            guard.remove(plugin_id)
        };
        self.pm.unregister(plugin_id).await;
        if let Some(mut p) = running.take() {
            self.host_state.unbind_token(&p.host_token);
            let _ = p.client.shutdown(Request::new(ShutdownRequest {})).await;
            let _ = p.child.kill().await;
            let _ = p.child.wait().await;
        }
        Ok(())
    }

    pub async fn close(&self) {
        let ids: Vec<String> = self.running.lock().await.keys().cloned().collect();
        for id in ids {
            let _ = self.stop_plugin(&id).await;
        }
    }
}

struct RpcPlugin {
    client: Mutex<AuthedPluginClient>,
}

#[async_trait]
impl Plugin for RpcPlugin {
    async fn descriptor(&self) -> Result<Descriptor, StructuredError> {
        let mut client = self.client.lock().await;
        let resp = client
            .describe(Request::new(DescribeRequest {}))
            .await
            .map_err(|e| StructuredError::internal(e.to_string()))?
            .into_inner();
        Descriptor::from_json_bytes(&resp.descriptor_json)
            .map_err(|e| StructuredError::internal(e.to_string()))
    }

    async fn configure(&self, config: Value) -> Result<(), StructuredError> {
        let config_json = serde_json::to_vec(&config)
            .map_err(|e| StructuredError::invalid_params(e.to_string()))?;
        let mut client = self.client.lock().await;
        client
            .configure(Request::new(ConfigureRequest { config_json }))
            .await
            .map_err(|e| StructuredError::internal(e.to_string()))?;
        Ok(())
    }

    async fn invoke(
        &self,
        method: &str,
        params: Value,
        caller_plugin_id: &str,
    ) -> Result<Value, StructuredError> {
        let params_json = serde_json::to_vec(&params)
            .map_err(|e| StructuredError::invalid_params(e.to_string()))?;
        let mut client = self.client.lock().await;
        let resp = client
            .invoke(Request::new(InvokeRequest {
                method: method.to_string(),
                params_json,
                caller_plugin_id: caller_plugin_id.to_string(),
            }))
            .await
            .map_err(|e| StructuredError::internal(e.to_string()))?
            .into_inner();
        if let Some(err) = resp.error {
            return Err(StructuredError::from_pb(err));
        }
        if resp.result_json.is_empty() {
            return Ok(Value::Null);
        }
        serde_json::from_slice(&resp.result_json)
            .map_err(|e| StructuredError::internal(e.to_string()))
    }

    async fn handle(
        &self,
        listener_id: &str,
        event_raw: Value,
        match_data: Option<CommandMatch>,
        trace_id: &str,
    ) -> Result<(), StructuredError> {
        let event_raw_json = serde_json::to_vec(&event_raw)
            .map_err(|e| StructuredError::invalid_params(e.to_string()))?;
        let match_field = match_data.map(|m| nyanyabot_proto::pb::CommandMatch {
            full: m.full,
            groups: m.groups,
        });
        let mut client = self.client.lock().await;
        client
            .handle(Request::new(HandleRequest {
                listener_id: listener_id.to_string(),
                event_raw_json,
                r#match: match_field,
                trace_id: trace_id.to_string(),
            }))
            .await
            .map_err(|e| StructuredError::internal(e.to_string()))?;
        Ok(())
    }

    async fn status(&self) -> Result<String, StructuredError> {
        let mut client = self.client.lock().await;
        let resp = client
            .status(Request::new(StatusRequest {}))
            .await
            .map_err(|e| StructuredError::internal(e.to_string()))?
            .into_inner();
        Ok(resp.status)
    }

    async fn shutdown(&self) -> Result<(), StructuredError> {
        let mut client = self.client.lock().await;
        client
            .shutdown(Request::new(ShutdownRequest {}))
            .await
            .map_err(|e| StructuredError::internal(e.to_string()))?;
        Ok(())
    }
}

async fn read_readiness<R: tokio::io::AsyncRead + Unpin>(stdout: R) -> Result<Readiness> {
    let mut lines = BufReader::new(stdout).lines();
    let line = lines
        .next_line()
        .await?
        .ok_or_else(|| anyhow!("plugin closed stdout before readiness"))?;
    let readiness = Readiness::parse_line(&line)?;
    Ok(readiness)
}

fn list_plugin_executables(dir: &Path) -> Result<Vec<PathBuf>> {
    let mut out = Vec::new();
    for entry in std::fs::read_dir(dir).with_context(|| format!("read_dir {}", dir.display()))? {
        let entry = entry?;
        let path = entry.path();
        if !path.is_file() {
            continue;
        }
        let name = path
            .file_name()
            .and_then(|s| s.to_str())
            .unwrap_or("")
            .to_string();
        if !name.starts_with("nyanyabot-plugin-") {
            continue;
        }
        #[cfg(windows)]
        {
            if !name.ends_with(".exe") {
                continue;
            }
        }
        #[cfg(not(windows))]
        {
            use std::os::unix::fs::PermissionsExt;
            let meta = entry.metadata()?;
            if meta.permissions().mode() & 0o111 == 0 {
                // still allow non-executable during dev? require executable bit
                continue;
            }
        }
        out.push(path);
    }
    Ok(out)
}

fn sleep_timeout_for(cfg: &AppConfig, plugin_id: &str) -> i64 {
    if let Some(ctrl) = cfg.plugin_controls.get(plugin_id)
        && let Some(v) = ctrl.sleep_timeout
    {
        return v as i64;
    }
    if cfg.global_sleep_timeout > 0 {
        cfg.global_sleep_timeout as i64
    } else {
        60
    }
}
