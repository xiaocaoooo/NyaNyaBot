use std::sync::Arc;

use anyhow::{Context, Result};
use serde_json::Value;
use tracing::info;

use crate::chatlog;
use crate::config::Store;
use crate::cron::Scheduler;
use crate::dedup::{Deduper, MemoryDeduper};
use crate::dispatch::Dispatcher;
use crate::onebot::reversews::Server as ReverseWsServer;
use crate::plugin::Manager;
use crate::pluginhost::PluginHost;
use crate::stats::Stats;
use crate::triggerlog;
use crate::util::workspace_data_dir;
use crate::web::WebServer;

pub struct App {
    pub store: Arc<Store>,
    pub plugin_manager: Arc<Manager>,
    pub plugin_host: Arc<PluginHost>,
    pub onebot: Arc<ReverseWsServer>,
    pub web: Arc<WebServer>,
    pub cron: Arc<Scheduler>,
    pub stats: Arc<Stats>,
    pub chatlog: Option<Arc<chatlog::Recorder>>,
    pub triggerlog: Arc<triggerlog::Recorder>,
    pub dispatcher: Arc<Dispatcher>,
}

impl App {
    pub async fn new() -> Result<Self> {
        let data_dir = workspace_data_dir();
        let store = Store::new(&data_dir).context("config store")?;
        let cfg = store.load_or_create_default().context("load config")?;
        info!(path = %data_dir.join("config.json").display(), "config ready");

        let stats = Stats::new();
        let pm = Manager::new();
        let onebot = ReverseWsServer::new(store.clone());
        let trigger = triggerlog::Recorder::new(&cfg.trigger_log);
        if cfg.trigger_log.enabled {
            trigger.start();
        }
        // DB writer bootstrapped in main via start_async
        let chat = chatlog::Recorder::new(&cfg.chat_log);

        let onebot_for_call = onebot.clone();
        let call_onebot: crate::pluginhost::host_service::CallOneBotFn = Arc::new(
            move |action: String, params: Value, self_id: i64, _trace_id: String| {
                let ob = onebot_for_call.clone();
                Box::pin(async move {
                    if self_id != 0 {
                        ob.call_with_bot(self_id, &action, params).await
                    } else {
                        ob.call(&action, params).await
                    }
                })
            },
        );

        let host = PluginHost::new(pm.clone(), store.clone(), stats.clone(), call_onebot)
            .await
            .context("plugin host")?;
        host.set_trigger_recorder(trigger.clone());

        let deduper: Option<Arc<dyn Deduper>> = if cfg.is_message_dedup_enabled() {
            Some(MemoryDeduper::new(cfg.dedup.ttl_seconds))
        } else {
            None
        };

        let dispatcher = Dispatcher::new(
            pm.clone(),
            store.clone(),
            stats.clone(),
            host.clone(),
            deduper,
        );

        let disp = dispatcher.clone();
        let chat2 = chat.clone();
        onebot.set_handler(move |event: Value| {
            chat2.handle_event(&event);
            disp.dispatch(event);
        });

        let cron = Scheduler::new(pm.clone(), store.clone(), host.clone());
        let web = WebServer::new(
            store.clone(),
            pm.clone(),
            stats.clone(),
            host.clone(),
            onebot.clone(),
            trigger.clone(),
            Some(chat.clone()),
        );

        Ok(Self {
            store,
            plugin_manager: pm,
            plugin_host: host,
            onebot,
            web,
            cron,
            stats,
            chatlog: Some(chat),
            triggerlog: trigger,
            dispatcher,
        })
    }
}

// re-export path for CallOneBotFn visibility
pub use crate::pluginhost::host_service;
