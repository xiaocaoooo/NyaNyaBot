use std::net::SocketAddr;
use std::time::Duration;

use anyhow::Context;
use tokio::signal;
use tracing::{error, info};
use tracing_subscriber::EnvFilter;
use url::form_urlencoded;

use nyanyabot::App;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")),
        )
        .init();

    let app = App::new().await.context("app init failed")?;
    if let Err(err) = app.plugin_host.load_dir("plugins").await {
        error!(error = %err, "load plugins dir failed");
    }
    app.cron.register_all_plugins().await;
    app.cron.start();

    let plugins = app.plugin_manager.list().await;
    info!(count = plugins.len(), "plugins loaded");
    for p in &plugins {
        info!(
            plugin_id = %p.plugin_id,
            name = %p.name,
            version = %p.version,
            commands = p.commands.len(),
            events = p.events.len(),
            crons = p.crons.len(),
            "plugin"
        );
    }

    let web_addr = app.store.get().webui.listen_addr.clone();
    let password = app.store.get().webui.password.clone();
    let (webui_url, auto_login) = build_webui_urls(&web_addr, &password);
    info!(addr = %web_addr, url = %webui_url, auto_login_url = %auto_login, "webui listening");

    let web = app.web.clone();
    let web_task = tokio::spawn(async move {
        if let Err(err) = web.serve().await {
            error!(error = %err, "webui server error");
        }
    });

    let ob = app.onebot.clone();
    let ob_task = tokio::spawn(async move {
        if let Err(err) = ob.start().await {
            error!(error = %err, "onebot reverse ws error");
        }
    });

    if let Some(chatlog) = app.chatlog.clone() {
        chatlog.start().await;
    }
    app.triggerlog.start_async().await;

    shutdown_signal().await;
    info!("shutting down");

    app.cron.stop().await;
    let _ = app.web.shutdown().await;
    let _ = app.onebot.shutdown().await;
    if let Some(chatlog) = &app.chatlog {
        let _ = chatlog.stop().await;
    }
    app.plugin_host.close().await;
    let _ = tokio::time::timeout(Duration::from_secs(2), web_task).await;
    let _ = tokio::time::timeout(Duration::from_secs(2), ob_task).await;
    Ok(())
}

async fn shutdown_signal() {
    let ctrl_c = async {
        let _ = signal::ctrl_c().await;
    };
    #[cfg(unix)]
    let terminate = async {
        use tokio::signal::unix::{SignalKind, signal};
        let mut sig = signal(SignalKind::terminate()).expect("install SIGTERM handler");
        sig.recv().await;
    };
    #[cfg(not(unix))]
    let terminate = std::future::pending::<()>();
    tokio::select! {
        _ = ctrl_c => {},
        _ = terminate => {},
    }
}

fn build_webui_urls(listen_addr: &str, password: &str) -> (String, String) {
    let host_port = normalize_display_host_port(listen_addr);
    let base = format!("http://{host_port}/");
    let q: String = form_urlencoded::Serializer::new(String::new())
        .append_pair("password", password)
        .finish();
    let auto = format!("http://{host_port}/login/?{q}");
    (base, auto)
}

fn normalize_display_host_port(listen_addr: &str) -> String {
    let listen_addr = listen_addr.trim();
    if listen_addr.is_empty() {
        return "127.0.0.1:3000".into();
    }
    match listen_addr.parse::<SocketAddr>() {
        Ok(addr) => {
            let ip = addr.ip();
            let host = if ip.is_unspecified() {
                "127.0.0.1".to_string()
            } else {
                ip.to_string()
            };
            format!("{host}:{}", addr.port())
        }
        Err(_) => listen_addr.to_string(),
    }
}
