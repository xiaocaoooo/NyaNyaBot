use std::collections::HashMap;
use std::str::FromStr;
use std::sync::Arc;

use chrono::Utc;
use cron::Schedule;
use parking_lot::RwLock;
use serde_json::json;
use tokio::task::JoinHandle;
use tracing::{info, warn};

use crate::config::Store;
use crate::plugin::Manager;
use crate::pluginhost::PluginHost;

struct CronTask {
    handle: JoinHandle<()>,
}

pub struct Scheduler {
    pm: Arc<Manager>,
    store: Arc<Store>,
    host: Arc<PluginHost>,
    tasks: RwLock<HashMap<String, CronTask>>,
    running: RwLock<bool>,
}

impl Scheduler {
    pub fn new(pm: Arc<Manager>, store: Arc<Store>, host: Arc<PluginHost>) -> Arc<Self> {
        Arc::new(Self {
            pm,
            store,
            host,
            tasks: RwLock::new(HashMap::new()),
            running: RwLock::new(false),
        })
    }

    pub async fn register_all_plugins(self: &Arc<Self>) {
        let entries = self.pm.entries().await;
        for (pid, desc, _) in entries {
            self.register_plugin(&pid, &desc).await;
        }
    }

    pub async fn register_plugin(
        self: &Arc<Self>,
        plugin_id: &str,
        desc: &nyanyabot_proto::Descriptor,
    ) {
        self.unregister_plugin(plugin_id).await;
        if !*self.running.read() {
            // Tasks are created on start(); remember by re-scan.
            return;
        }
        for cron_item in &desc.crons {
            self.spawn_cron(plugin_id, cron_item).await;
        }
    }

    pub async fn unregister_plugin(&self, plugin_id: &str) {
        let keys: Vec<String> = self
            .tasks
            .read()
            .keys()
            .filter(|k| k.starts_with(&format!("{plugin_id}\0")))
            .cloned()
            .collect();
        let mut tasks = self.tasks.write();
        for key in keys {
            if let Some(t) = tasks.remove(&key) {
                t.handle.abort();
            }
        }
    }

    pub async fn refresh_plugin(self: &Arc<Self>, plugin_id: &str) {
        if let Some((_, desc, _)) = self
            .pm
            .entries()
            .await
            .into_iter()
            .find(|(id, _, _)| id == plugin_id)
        {
            self.register_plugin(plugin_id, &desc).await;
        } else {
            self.unregister_plugin(plugin_id).await;
        }
    }

    pub fn start(self: &Arc<Self>) {
        let mut running = self.running.write();
        if *running {
            return;
        }
        *running = true;
        drop(running);
        let this = Arc::clone(self);
        tokio::spawn(async move {
            this.register_all_plugins().await;
        });
    }

    async fn spawn_cron(
        self: &Arc<Self>,
        plugin_id: &str,
        cron_item: &nyanyabot_proto::CronListener,
    ) {
        let Ok(schedule) = Schedule::from_str(&cron_item.schedule) else {
            warn!(plugin_id = %plugin_id, schedule = %cron_item.schedule, "invalid cron");
            return;
        };
        let key = format!("{plugin_id}\0{}", cron_item.id);
        let store = self.store.clone();
        let host = self.host.clone();
        let pm = self.pm.clone();
        let plugin_id = plugin_id.to_string();
        let listener_id = cron_item.id.clone();
        let schedule_str = cron_item.schedule.clone();
        let handle = tokio::spawn(async move {
            loop {
                let cfg = store.get();
                if !cfg.is_plugin_enabled(&plugin_id)
                    || !cfg.is_cron_enabled(&plugin_id, &listener_id)
                {
                    tokio::time::sleep(std::time::Duration::from_secs(1)).await;
                    continue;
                }
                let Some(next) = schedule.upcoming(Utc).next() else {
                    break;
                };
                let now = Utc::now();
                let dur = (next - now)
                    .to_std()
                    .unwrap_or(std::time::Duration::from_secs(1));
                tokio::time::sleep(dur).await;
                let Some((_, _, plugin)) = pm
                    .entries()
                    .await
                    .into_iter()
                    .find(|(id, _, _)| id == &plugin_id)
                else {
                    break;
                };
                let trace_id = host.generate_trace_id();
                host.begin_trace(
                    &trace_id,
                    &plugin_id,
                    &listener_id,
                    "cron",
                    json!({"schedule": schedule_str}),
                );
                info!(plugin_id = %plugin_id, cron_id = %listener_id, "cron fired");
                let _ = host.ensure_awake(&plugin_id).await;
                let event = json!({"post_type":"cron","cron_id": listener_id});
                let handle_res = plugin.handle(&listener_id, event, None, &trace_id).await;
                let (ok, err_msg) = match &handle_res {
                    Ok(_) => (true, String::new()),
                    Err(err) => {
                        warn!(plugin_id = %plugin_id, error = %err, "cron handle failed");
                        (false, err.to_string())
                    }
                };
                host.end_trace(&trace_id, ok, err_msg);
            }
        });
        self.tasks.write().insert(key, CronTask { handle });
    }

    pub async fn stop(&self) {
        *self.running.write() = false;
        let tasks = std::mem::take(&mut *self.tasks.write());
        for (_, t) in tasks {
            t.handle.abort();
        }
    }
}
