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

pub struct Scheduler {
    pm: Arc<Manager>,
    store: Arc<Store>,
    host: Arc<PluginHost>,
    tasks: RwLock<Vec<JoinHandle<()>>>,
    running: RwLock<bool>,
}

impl Scheduler {
    pub fn new(pm: Arc<Manager>, store: Arc<Store>, host: Arc<PluginHost>) -> Arc<Self> {
        Arc::new(Self {
            pm,
            store,
            host,
            tasks: RwLock::new(Vec::new()),
            running: RwLock::new(false),
        })
    }

    pub async fn register_all_plugins(&self) {}

    pub fn start(self: &Arc<Self>) {
        let mut running = self.running.write();
        if *running {
            return;
        }
        *running = true;
        drop(running);

        let this = Arc::clone(self);
        tokio::spawn(async move {
            let entries = this.pm.entries().await;
            for (pid, desc, plugin) in entries {
                for cron_item in desc.crons {
                    let Ok(schedule) = Schedule::from_str(&cron_item.schedule) else {
                        warn!(plugin_id = %pid, schedule = %cron_item.schedule, "invalid cron");
                        continue;
                    };
                    let store = this.store.clone();
                    let host = this.host.clone();
                    let plugin = plugin.clone();
                    let plugin_id = pid.clone();
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
                            let trace_id = host.generate_trace_id();
                            host.begin_trace(
                                &trace_id,
                                &plugin_id,
                                &listener_id,
                                "cron",
                                json!({"schedule": schedule_str}),
                            );
                            info!(plugin_id = %plugin_id, cron_id = %listener_id, "cron fired");
                            let event = json!({"post_type":"cron","cron_id": listener_id});
                            if let Err(err) =
                                plugin.handle(&listener_id, event, None, &trace_id).await
                            {
                                warn!(plugin_id = %plugin_id, error = %err, "cron handle failed");
                            }
                            host.end_trace(&trace_id);
                        }
                    });
                    this.tasks.write().push(handle);
                }
            }
        });
    }

    pub async fn stop(&self) {
        *self.running.write() = false;
        let tasks = std::mem::take(&mut *self.tasks.write());
        for t in tasks {
            t.abort();
        }
    }
}
