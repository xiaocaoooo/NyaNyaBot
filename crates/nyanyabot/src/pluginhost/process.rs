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
    by_path: Mutex<HashMap<PathBuf, Vec<String>>>,
    trigger_recorder: RwLock<Option<Arc<TriggerRecorder>>>,
    traces: RwLock<HashMap<String, TraceRecord>>,
    restart_tx: mpsc::UnboundedSender<(PathBuf, String)>,
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
            store: store.clone(),
            tokens: Arc::new(RwLock::new(HashMap::new())),
            call_onebot,
            plugin_sent: Arc::new(RwLock::new(HashMap::new())),
            ensure_awake: Arc::new(RwLock::new(None)),
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

        let (restart_tx, mut restart_rx) = mpsc::unbounded_channel::<(PathBuf, String)>();
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
            while let Some((exe, plugin_id)) = restart_rx.recv().await {
                tokio::time::sleep(Duration::from_millis(200)).await;
                match worker_host.restart_plugin_at(&exe, &plugin_id).await {
                    Ok(()) => {
                        info!(path = %exe.display(), plugin_id = %plugin_id, "plugin auto-restarted")
                    }
                    Err(err) => {
                        error!(path = %exe.display(), plugin_id = %plugin_id, error = %err, "plugin auto-restart failed")
                    }
                }
            }
        });
        *host._restart_worker.lock().await = Some(worker);

        // Allow HostService CallDependency to wake idle-stopped plugins.
        let wake_host = Arc::clone(&host);
        *host.host_state.ensure_awake.write().unwrap() =
            Some(Arc::new(move |plugin_id: String| {
                let h = Arc::clone(&wake_host);
                Box::pin(async move {
                    h.ensure_awake(&plugin_id)
                        .await
                        .map_err(|e| format!("{e:#}"))
                })
            }));

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

    pub fn end_trace(&self, trace_id: &str, success: bool, error_message: impl Into<String>) {
        let error_message = error_message.into();
        if let Some(record) = self.traces.write().unwrap().remove(trace_id)
            && let Some(rec) = self.trigger_recorder.read().unwrap().clone()
        {
            let duration_ms = (chrono::Utc::now() - record.start).num_milliseconds();
            let mut data = record.data.clone();
            if let Some(obj) = data.as_object_mut() {
                obj.insert("success".into(), serde_json::Value::Bool(success));
                if !error_message.is_empty() {
                    obj.insert(
                        "error_message".into(),
                        serde_json::Value::String(error_message.clone()),
                    );
                }
            }
            let self_id = json_i64(&data, "self_id");
            let user_id = json_i64(&data, "user_id");
            let group_id = json_i64(&data, "group_id");
            let error = error_message;
            // Prefer explicit message_id; fall back to seq/message_seq (Go parseIntOrZero(real_seq)).
            let (message_id, message_seq) = resolve_message_fields(&data);
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
                    trigger_data: data,
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
        let cfg = self.store.get();
        let existing: std::collections::HashSet<String> =
            self.pm.plugin_ids().await.into_iter().collect();

        let mut started: HashMap<String, RunningPlugin> = HashMap::new();
        let mut errors = Vec::new();

        // Phase 1: probe all base descriptors to know dependency set (Go loadStartedCandidates).
        let mut probes: Vec<(std::path::PathBuf, String, Vec<String>)> = Vec::new();
        for path in paths {
            let probe = match self.start_plugin_process(&path, "").await {
                Ok(p) => p,
                Err(err) => {
                    errors.push(format!("{}: {err}", path.display()));
                    continue;
                }
            };
            let base_id = probe.descriptor.plugin_id.clone();
            let deps = probe.descriptor.dependencies.clone();
            let _ = self.discard_unregistered(probe).await;
            probes.push((path, base_id, deps));
        }

        let mut dep_set: std::collections::HashSet<String> = std::collections::HashSet::new();
        for desc in self.pm.list().await {
            for dep in desc.dependencies {
                dep_set.insert(dep);
            }
        }
        for (_, _, deps) in &probes {
            for dep in deps {
                dep_set.insert(dep.clone());
            }
        }

        // Phase 2: start instances with Go multi-instance rules.
        for (path, base_id, _) in probes {
            let is_dependency = dep_set.contains(&base_id);
            let instance_ids = instance_ids_for(&base_id, &cfg, is_dependency);
            for inst in instance_ids {
                if existing.contains(&inst) || started.contains_key(&inst) {
                    errors.push(format!(
                        "{}: duplicate plugin_id already loaded: {inst}",
                        path.display()
                    ));
                    continue;
                }
                match self.start_plugin_process(&path, &inst).await {
                    Ok(mut running) => {
                        // Force instance identity even if binary reports base id.
                        running.plugin_id = inst.clone();
                        running.descriptor.plugin_id = inst.clone();
                        running.lazy = LazyPlugin::new(
                            inst.clone(),
                            running.lazy.inner(),
                            sleep_timeout_for(&cfg, &inst),
                        );
                        started.insert(inst, running);
                    }
                    Err(err) => errors.push(format!("{} [{inst}]: {err}", path.display())),
                }
            }
        }

        let descs: HashMap<String, Descriptor> = started
            .iter()
            .map(|(id, r)| (id.clone(), r.descriptor.clone()))
            .collect();
        let (order, rejected) = resolve_dependency_order(&descs, &existing);
        for (id, reason) in &rejected {
            errors.push(format!("plugin {id} rejected: {reason}"));
            if let Some(r) = started.remove(id) {
                let _ = self.discard_unregistered(r).await;
            }
        }

        let mut loaded = existing;
        for plugin_id in order {
            let Some(running) = started.remove(&plugin_id) else {
                continue;
            };
            if !deps_ready(&running.descriptor.dependencies, &loaded) {
                errors.push(format!(
                    "plugin {plugin_id} skipped: dependency failed during registration"
                ));
                let _ = self.discard_unregistered(running).await;
                continue;
            }
            match self.finish_load(running).await {
                Ok(()) => {
                    loaded.insert(plugin_id);
                }
                Err(err) => errors.push(format!("plugin {plugin_id}: {err}")),
            }
        }
        // Any leftovers failed ordering
        for (id, running) in started {
            errors.push(format!("plugin {id} not registered (ordering)"));
            let _ = self.discard_unregistered(running).await;
        }

        if errors.is_empty() {
            Ok(())
        } else {
            bail!("load_dir errors: {}", errors.join("; "))
        }
    }

    pub async fn load_exec(self: &Arc<Self>, exe_path: impl AsRef<Path>) -> Result<()> {
        let exe_path = exe_path.as_ref().to_path_buf();
        let cfg = self.store.get();
        let probe = self.start_plugin_process(&exe_path, "").await?;
        let base_id = probe.descriptor.plugin_id.clone();
        let probe_deps = probe.descriptor.dependencies.clone();
        let _ = self.discard_unregistered(probe).await;
        let mut dep_set: std::collections::HashSet<String> = std::collections::HashSet::new();
        for desc in self.pm.list().await {
            for dep in desc.dependencies {
                dep_set.insert(dep);
            }
        }
        for dep in probe_deps {
            dep_set.insert(dep);
        }
        let is_dependency = dep_set.contains(&base_id);
        let instances = instance_ids_for(&base_id, &cfg, is_dependency);
        let mut errors = Vec::new();
        let existing: std::collections::HashSet<String> =
            self.pm.plugin_ids().await.into_iter().collect();
        let mut started = HashMap::new();
        for inst in instances {
            if existing.contains(&inst) {
                errors.push(format!("duplicate plugin_id already loaded: {inst}"));
                continue;
            }
            match self.start_plugin_process(&exe_path, &inst).await {
                Ok(mut running) => {
                    running.plugin_id = inst.clone();
                    running.descriptor.plugin_id = inst.clone();
                    running.lazy = LazyPlugin::new(
                        inst.clone(),
                        running.lazy.inner(),
                        sleep_timeout_for(&cfg, &inst),
                    );
                    started.insert(inst, running);
                }
                Err(err) => errors.push(err.to_string()),
            }
        }
        let descs: HashMap<String, Descriptor> = started
            .iter()
            .map(|(id, r)| (id.clone(), r.descriptor.clone()))
            .collect();
        let (order, rejected) = resolve_dependency_order(&descs, &existing);
        for (id, reason) in rejected {
            errors.push(format!("plugin {id} rejected: {reason}"));
            if let Some(r) = started.remove(&id) {
                let _ = self.discard_unregistered(r).await;
            }
        }
        let mut loaded = existing;
        for plugin_id in order {
            let Some(running) = started.remove(&plugin_id) else {
                continue;
            };
            if !deps_ready(&running.descriptor.dependencies, &loaded) {
                errors.push(format!("plugin {plugin_id} skipped: dependency not ready"));
                let _ = self.discard_unregistered(running).await;
                continue;
            }
            if let Err(err) = self.finish_load(running).await {
                errors.push(format!("{plugin_id}: {err}"));
            } else {
                loaded.insert(plugin_id);
            }
        }
        for (id, running) in started {
            errors.push(format!("plugin {id} not registered"));
            let _ = self.discard_unregistered(running).await;
        }
        if errors.is_empty() {
            Ok(())
        } else {
            bail!("load_exec errors: {}", errors.join("; "))
        }
    }

    async fn discard_unregistered(&self, mut running: RunningPlugin) -> Result<()> {
        self.host_state.unbind_token(&running.host_token);
        let _ = running
            .client
            .shutdown(Request::new(ShutdownRequest {}))
            .await;
        let _ = running.child.kill().await;
        let _ = running.child.wait().await;
        Ok(())
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
        running.lazy.clear_crashed();
        self.pm.register(plugin_arc, desc.clone()).await?;
        self.by_path
            .lock()
            .await
            .entry(running.exe_path.clone())
            .or_default()
            .push(plugin_id.clone());
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

    pub async fn restart_plugin_at(self: &Arc<Self>, exe: &Path, plugin_id: &str) -> Result<()> {
        let _ = self.stop_plugin(plugin_id).await;
        let mut running = self.start_plugin_process(exe, plugin_id).await?;
        running.plugin_id = plugin_id.to_string();
        running.descriptor.plugin_id = plugin_id.to_string();
        let cfg = self.store.get();
        running.lazy = LazyPlugin::new(
            plugin_id.to_string(),
            running.lazy.inner(),
            sleep_timeout_for(&cfg, plugin_id),
        );
        self.finish_load(running).await
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
        if !known_plugin_id.is_empty() {
            descriptor.plugin_id = known_plugin_id.to_string();
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
                // If marked sleeping, process exit is expected (idle stop); wait for wake via call path/restart.
                let sleeping = {
                    let guard = self.running.lock().await;
                    guard
                        .get(&plugin_id)
                        .map(|p| p.lazy.is_sleeping())
                        .unwrap_or(false)
                };
                if sleeping {
                    tokio::time::sleep(Duration::from_secs(1)).await;
                    continue;
                }
                warn!(plugin_id = %plugin_id, %status, "plugin process exited; removing");
                let exe = {
                    let guard = self.running.lock().await;
                    if let Some(p) = guard.get(&plugin_id) {
                        p.lazy.mark_crashed();
                    }
                    guard.get(&plugin_id).map(|p| p.exe_path.clone())
                };
                self.pm.unregister(&plugin_id).await;
                if let Some(old) = self.running.lock().await.remove(&plugin_id) {
                    self.host_state.unbind_token(&old.host_token);
                    if let Some(ids) = self.by_path.lock().await.get_mut(&old.exe_path) {
                        ids.retain(|id| id != &plugin_id);
                    }
                }
                if let Some(exe) = exe {
                    let _ = self.restart_tx.send((exe, plugin_id.clone()));
                }
                return;
            }
            let should_sleep = {
                let guard = self.running.lock().await;
                guard
                    .get(&plugin_id)
                    .map(|p| p.lazy.maybe_sleep())
                    .unwrap_or(false)
            };
            if should_sleep {
                info!(plugin_id = %plugin_id, "plugin idle: stopping process");
                if let Some(p) = self.running.lock().await.get_mut(&plugin_id) {
                    let _ = p.client.shutdown(Request::new(ShutdownRequest {})).await;
                    let _ = p.child.kill().await;
                    let _ = p.child.wait().await;
                }
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
        self.restart_plugin_at(&exe, plugin_id).await
    }

    /// Wake a sleeping plugin process if needed (used before handle/invoke).
    pub async fn ensure_awake(self: &Arc<Self>, plugin_id: &str) -> Result<()> {
        let (sleeping, exe, alive) = {
            let guard = self.running.lock().await;
            match guard.get(plugin_id) {
                Some(p) => (
                    p.lazy.is_sleeping(),
                    p.exe_path.clone(),
                    p.child.id().is_some(),
                ),
                None => bail!("plugin not running: {plugin_id}"),
            }
        };
        if sleeping || !alive {
            self.restart_plugin_at(&exe, plugin_id).await?;
        }
        Ok(())
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

    pub async fn reconfigure_plugin(self: &Arc<Self>, plugin_id: &str) -> Result<()> {
        // Config push needs a live process; wake if idle-stopped.
        self.ensure_awake(plugin_id).await?;
        let mut guard = self.running.lock().await;
        let Some(running) = guard.get_mut(plugin_id) else {
            bail!("plugin not running: {plugin_id}");
        };
        self.push_config(running).await?;
        running.lazy.touch();
        Ok(())
    }

    pub async fn reconfigure_all(self: &Arc<Self>) -> Result<()> {
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
            if let Some(ids) = self.by_path.lock().await.get_mut(&p.exe_path) {
                ids.retain(|id| id != plugin_id);
            }
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
    let enable_sleep = cfg
        .plugin_controls
        .get(plugin_id)
        .and_then(|c| c.enable_sleep)
        .unwrap_or(true);
    if !enable_sleep {
        return 0;
    }
    if let Some(ctrl) = cfg.plugin_controls.get(plugin_id)
        && let Some(v) = ctrl.sleep_timeout
        && v > 0
    {
        return v as i64;
    }
    if cfg.global_sleep_timeout > 0 {
        cfg.global_sleep_timeout as i64
    } else {
        60
    }
}

/// Resolve instance IDs to start for a probed base plugin.
/// Go rules:
/// - dependency plugins never multi-instance; always base only
/// - functional plugins may start `base@name` from plugins/plugin_controls
/// - when multi-instances exist, also start base iff base configured OR plugin is a dependency
fn instance_ids_for(base_id: &str, cfg: &AppConfig, is_dependency: bool) -> Vec<String> {
    let prefix = format!("{base_id}@");
    let is_functional = !is_dependency;
    let mut multi_ids = Vec::new();
    let mut has_base_configured = false;
    let mut seen = std::collections::HashSet::new();
    for key in cfg.plugins.keys().chain(cfg.plugin_controls.keys()) {
        if key == base_id {
            has_base_configured = true;
        } else if is_functional && key.starts_with(&prefix) && seen.insert(key.clone()) {
            multi_ids.push(key.clone());
        }
    }
    if multi_ids.is_empty() {
        return vec![base_id.to_string()];
    }
    let mut ids = multi_ids;
    if has_base_configured || is_dependency {
        if seen.insert(base_id.to_string()) {
            ids.push(base_id.to_string());
        }
    }
    ids.sort();
    ids
}

fn deps_ready(deps: &[String], loaded: &std::collections::HashSet<String>) -> bool {
    deps.iter().all(|d| {
        // Multi-instance deps may be declared as base id; accept any loaded id with base or base@*
        if loaded.contains(d) {
            return true;
        }
        let prefix = format!("{d}@");
        loaded.iter().any(|id| id.starts_with(&prefix))
    })
}

fn resolve_dependency_order(
    descs: &HashMap<String, Descriptor>,
    already_loaded: &std::collections::HashSet<String>,
) -> (Vec<String>, HashMap<String, String>) {
    let mut rejected: HashMap<String, String> = HashMap::new();
    if descs.is_empty() {
        return (Vec::new(), rejected);
    }

    // Reject missing deps (allow already loaded or peer batch / multi-instance base).
    let mut changed = true;
    while changed {
        changed = false;
        for (plugin_id, desc) in descs {
            if rejected.contains_key(plugin_id) {
                continue;
            }
            for dep in &desc.dependencies {
                if descs.contains_key(dep) {
                    if rejected.contains_key(dep) {
                        rejected.insert(
                            plugin_id.clone(),
                            format!("dependency {dep} is unavailable"),
                        );
                        changed = true;
                        break;
                    }
                    continue;
                }
                let prefix = format!("{dep}@");
                let in_batch = descs.keys().any(|k| k == dep || k.starts_with(&prefix));
                let loaded = already_loaded.contains(dep)
                    || already_loaded.iter().any(|k| k.starts_with(&prefix));
                if !in_batch && !loaded {
                    rejected.insert(plugin_id.clone(), format!("missing dependency {dep}"));
                    changed = true;
                    break;
                }
            }
        }
    }

    let active: Vec<String> = descs
        .keys()
        .filter(|id| !rejected.contains_key(*id))
        .cloned()
        .collect();
    let mut indegree: HashMap<String, i32> = active.iter().map(|id| (id.clone(), 0)).collect();
    let mut edges: HashMap<String, Vec<String>> = HashMap::new();
    for id in &active {
        let desc = &descs[id];
        for dep in &desc.dependencies {
            // edge from concrete dep node in active set
            let providers: Vec<String> = active
                .iter()
                .filter(|k| *k == dep || k.starts_with(&format!("{dep}@")))
                .cloned()
                .collect();
            for p in providers {
                if p == *id {
                    continue;
                }
                edges.entry(p).or_default().push(id.clone());
                *indegree.entry(id.clone()).or_default() += 1;
            }
        }
    }
    let mut queue: Vec<String> = indegree
        .iter()
        .filter(|(_, d)| **d == 0)
        .map(|(k, _)| k.clone())
        .collect();
    queue.sort();
    let mut order = Vec::new();
    while let Some(current) = {
        if queue.is_empty() {
            None
        } else {
            Some(queue.remove(0))
        }
    } {
        order.push(current.clone());
        if let Some(nexts) = edges.get(&current).cloned() {
            for next in nexts {
                if let Some(d) = indegree.get_mut(&next) {
                    *d -= 1;
                    if *d == 0 {
                        queue.push(next);
                        queue.sort();
                    }
                }
            }
        }
    }
    if order.len() != active.len() {
        for id in active {
            if !order.contains(&id) && !rejected.contains_key(&id) {
                rejected.insert(id, "dependency cycle".into());
            }
        }
    }
    (order, rejected)
}

fn json_i64(data: &serde_json::Value, key: &str) -> i64 {
    data.get(key).map(value_as_i64).unwrap_or(0)
}

fn json_stringish(data: &serde_json::Value, key: &str) -> Option<String> {
    let v = data.get(key)?;
    if let Some(s) = v.as_str() {
        return Some(s.to_string());
    }
    if let Some(n) = v.as_i64() {
        return Some(n.to_string());
    }
    if let Some(n) = v.as_u64() {
        return Some(n.to_string());
    }
    if let Some(n) = v.as_f64() {
        return Some((n as i64).to_string());
    }
    None
}

fn value_as_i64(v: &serde_json::Value) -> i64 {
    v.as_i64()
        .or_else(|| v.as_u64().map(|n| n as i64))
        .or_else(|| v.as_f64().map(|n| n as i64))
        .or_else(|| v.as_str().and_then(|s| s.trim().parse().ok()))
        .unwrap_or(0)
}

/// Resolve message_id / message_seq from begin_trace payload (Go plugin_trigger_logs parity).
fn resolve_message_fields(data: &serde_json::Value) -> (i64, String) {
    let message_id = {
        let direct = json_i64(data, "message_id");
        if direct != 0 {
            direct
        } else {
            let from_seq = json_i64(data, "seq");
            if from_seq != 0 {
                from_seq
            } else {
                json_i64(data, "message_seq")
            }
        }
    };
    let message_seq = json_stringish(data, "message_seq")
        .or_else(|| json_stringish(data, "seq"))
        .unwrap_or_default();
    (message_id, message_seq)
}

#[cfg(test)]
mod tests {
    use super::*;
    use nyanyabot_proto::Descriptor;

    #[test]
    fn instance_ids_default_base() {
        let cfg = AppConfig::default();
        assert_eq!(
            instance_ids_for("external.echo", &cfg, false),
            vec!["external.echo".to_string()]
        );
    }

    #[test]
    fn instance_ids_dependency_ignores_at_suffix() {
        let mut cfg = AppConfig::default();
        cfg.plugins
            .insert("external.screenshot@cdn".into(), json!({}));
        // Dependencies never multi-instance.
        assert_eq!(
            instance_ids_for("external.screenshot", &cfg, true),
            vec!["external.screenshot".to_string()]
        );
    }

    #[test]
    fn instance_ids_functional_multi_without_base() {
        let mut cfg = AppConfig::default();
        cfg.plugins
            .insert("external.echo@a".into(), json!({}));
        assert_eq!(
            instance_ids_for("external.echo", &cfg, false),
            vec!["external.echo@a".to_string()]
        );
    }

    #[test]
    fn resolve_message_fields_prefers_explicit_keys() {
        let data = serde_json::json!({
            "message_id": 7,
            "message_seq": "42",
            "seq": 99
        });
        let (id, seq) = resolve_message_fields(&data);
        assert_eq!(id, 7);
        assert_eq!(seq, "42");
    }

    #[test]
    fn resolve_message_fields_falls_back_to_numeric_seq() {
        let data = serde_json::json!({"seq": 1001});
        let (id, seq) = resolve_message_fields(&data);
        assert_eq!(id, 1001);
        assert_eq!(seq, "1001");
    }

    #[test]
    fn resolve_message_fields_string_seq() {
        let data = serde_json::json!({"message_seq": "55"});
        let (id, seq) = resolve_message_fields(&data);
        assert_eq!(id, 55);
        assert_eq!(seq, "55");
    }

    #[test]
    fn instance_ids_multi_from_controls() {
        let mut cfg = AppConfig::default();
        cfg.plugin_controls.insert(
            "external.echo@a".into(),
            crate::config::PluginControl::default(),
        );
        cfg.plugins.insert("external.echo".into(), json!({}));
        cfg.plugins.insert("external.echo@b".into(), json!({"x":1}));
        let mut ids = instance_ids_for("external.echo", &cfg, false);
        ids.sort();
        assert_eq!(
            ids,
            vec![
                "external.echo".to_string(),
                "external.echo@a".to_string(),
                "external.echo@b".to_string()
            ]
        );
    }

    #[test]
    fn instance_ids_dependency_forces_base_with_multi() {
        // Even though dependency plugins normally ignore @, if somehow multi were
        // collected, base would be forced. With is_dependency=true multi is empty.
        let mut cfg = AppConfig::default();
        cfg.plugins
            .insert("external.account@x".into(), json!({}));
        cfg.plugins.insert("external.account".into(), json!({}));
        assert_eq!(
            instance_ids_for("external.account", &cfg, true),
            vec!["external.account".to_string()]
        );
    }

    #[test]
    fn dependency_order_topo() {
        let mut descs = HashMap::new();
        descs.insert(
            "b".into(),
            Descriptor {
                plugin_id: "b".into(),
                dependencies: vec!["a".into()],
                ..Default::default()
            },
        );
        descs.insert(
            "a".into(),
            Descriptor {
                plugin_id: "a".into(),
                dependencies: vec![],
                ..Default::default()
            },
        );
        let (order, rejected) = resolve_dependency_order(&descs, &Default::default());
        assert!(rejected.is_empty());
        assert_eq!(order, vec!["a".to_string(), "b".to_string()]);
    }

    #[test]
    fn dependency_missing_rejected() {
        let mut descs = HashMap::new();
        descs.insert(
            "x".into(),
            Descriptor {
                plugin_id: "x".into(),
                dependencies: vec!["missing".into()],
                ..Default::default()
            },
        );
        let (order, rejected) = resolve_dependency_order(&descs, &Default::default());
        assert!(order.is_empty());
        assert!(rejected.contains_key("x"));
    }

    #[test]
    fn sleep_timeout_disabled() {
        let mut cfg = AppConfig {
            global_sleep_timeout: 30,
            ..Default::default()
        };
        let ctrl = crate::config::PluginControl {
            enable_sleep: Some(false),
            ..Default::default()
        };
        cfg.plugin_controls.insert("p".into(), ctrl);
        assert_eq!(sleep_timeout_for(&cfg, "p"), 0);
    }
}
