use std::io::{Read, Write};
use std::net::TcpStream;
use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use nyanyabot::config::Store;
use nyanyabot::plugin::{Manager, Plugin};
use nyanyabot::pluginhost::PluginHost;
use nyanyabot::stats::Stats;
use nyanyabot::web::WebServer;
use nyanyabot_proto::{CommandListener, Descriptor, StructuredError};
use serde_json::{Value, json};
use tempfile::tempdir;

struct HttpResponse {
    status: u16,
    headers: Vec<(String, String)>,
    body: Vec<u8>,
}

impl HttpResponse {
    fn header(&self, name: &str) -> Option<&str> {
        self.headers
            .iter()
            .find(|(k, _)| k.eq_ignore_ascii_case(name))
            .map(|(_, v)| v.as_str())
    }

    fn body_str(&self) -> &str {
        std::str::from_utf8(&self.body).unwrap_or("")
    }

    fn json(&self) -> Value {
        serde_json::from_slice(&self.body).unwrap_or(Value::Null)
    }
}

fn raw_http(addr: &str, request: &str) -> HttpResponse {
    let mut stream = TcpStream::connect(addr).expect("connect");
    stream
        .set_read_timeout(Some(Duration::from_secs(2)))
        .unwrap();
    stream
        .set_write_timeout(Some(Duration::from_secs(2)))
        .unwrap();
    stream.write_all(request.as_bytes()).unwrap();

    let mut buf = Vec::new();
    let mut tmp = [0u8; 8192];
    loop {
        match stream.read(&mut tmp) {
            Ok(0) => break,
            Ok(n) => {
                buf.extend_from_slice(&tmp[..n]);
                if let Some(header_end) = buf.windows(4).position(|w| w == b"\r\n\r\n") {
                    let header_bytes = &buf[..header_end];
                    let headers_text = String::from_utf8_lossy(header_bytes);
                    if let Some(cl) = headers_text
                        .lines()
                        .find(|l| l.to_ascii_lowercase().starts_with("content-length:"))
                    {
                        if let Ok(n) = cl.split(':').nth(1).unwrap_or("0").trim().parse::<usize>() {
                            let body_start = header_end + 4;
                            if buf.len() >= body_start + n {
                                break;
                            }
                        }
                    } else if buf.len() > header_end + 4 {
                        break;
                    }
                }
            }
            Err(err)
                if err.kind() == std::io::ErrorKind::WouldBlock
                    || err.kind() == std::io::ErrorKind::TimedOut =>
            {
                break;
            }
            Err(err) => panic!("read failed: {err}"),
        }
    }

    let header_end = buf
        .windows(4)
        .position(|w| w == b"\r\n\r\n")
        .expect("missing header terminator");
    let header_text = String::from_utf8_lossy(&buf[..header_end]);
    let mut lines = header_text.lines();
    let status_line = lines.next().expect("status line");
    let status: u16 = status_line
        .split_whitespace()
        .nth(1)
        .expect("status code")
        .parse()
        .expect("parse status");
    let mut headers = Vec::new();
    for line in lines {
        if let Some((k, v)) = line.split_once(':') {
            headers.push((k.trim().to_string(), v.trim().to_string()));
        }
    }
    let body = buf[header_end + 4..].to_vec();
    HttpResponse {
        status,
        headers,
        body,
    }
}

struct DemoPlugin;

#[async_trait]
impl Plugin for DemoPlugin {
    async fn descriptor(&self) -> Result<Descriptor, StructuredError> {
        Ok(demo_desc())
    }
    async fn configure(&self, _: Value) -> Result<(), StructuredError> {
        Ok(())
    }
    async fn invoke(&self, _: &str, _: Value, _: &str) -> Result<Value, StructuredError> {
        Ok(Value::Null)
    }
    async fn handle(
        &self,
        _: &str,
        _: Value,
        _: Option<nyanyabot_proto::CommandMatch>,
        _: &str,
    ) -> Result<(), StructuredError> {
        Ok(())
    }
    async fn status(&self) -> Result<String, StructuredError> {
        Ok("Running".into())
    }
    async fn shutdown(&self) -> Result<(), StructuredError> {
        Ok(())
    }
}

fn demo_desc() -> Descriptor {
    Descriptor {
        plugin_id: "demo.plugin".into(),
        name: "Demo".into(),
        version: "1.0.0".into(),
        author: "t".into(),
        description: "demo".into(),
        commands: vec![CommandListener {
            id: "cmd.demo".into(),
            name: "demo".into(),
            description: "d".into(),
            pattern: r"^demo (?P<content>.+)$".into(),
            handler: "h".into(),
            ..Default::default()
        }],
        ..Default::default()
    }
}

async fn boot() -> (String, String, tempfile::TempDir) {
    let dir = tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    store.load_or_create_default().unwrap();

    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    drop(listener);

    store
        .update(|cfg| {
            cfg.webui.listen_addr = format!("127.0.0.1:{}", addr.port());
            cfg.webui.password = "admin-pass".into();
            cfg.onebot.reverse_ws.listen_addr = "127.0.0.1:0".into();
            cfg.plugins
                .insert("demo.plugin".into(), json!({"k": "keep-me"}));
            cfg.globals.insert("old".into(), "1".into());
        })
        .unwrap();

    let pm = Manager::new();
    pm.register(Arc::new(DemoPlugin), demo_desc())
        .await
        .unwrap();
    let stats = Stats::new();
    let call_onebot: nyanyabot::pluginhost::host_service::CallOneBotFn =
        Arc::new(|_a, _p, _s, _t| {
            Box::pin(async {
                Ok(nyanyabot::onebot::ob11::ApiResponse {
                    status: "ok".into(),
                    retcode: 0,
                    data: json!({}),
                    ..Default::default()
                })
            })
        });
    let host = PluginHost::new(pm.clone(), store.clone(), stats.clone(), call_onebot)
        .await
        .unwrap();
    let onebot = nyanyabot::onebot::reversews::Server::new(store.clone());
    let trigger = nyanyabot::triggerlog::Recorder::new(&store.get().trigger_log);
    let web = WebServer::new(store, pm, stats, host, onebot, trigger, None);
    let web2 = web.clone();
    tokio::spawn(async move {
        web2.serve().await.unwrap();
    });
    tokio::time::sleep(Duration::from_millis(200)).await;
    (
        format!("127.0.0.1:{}", addr.port()),
        "admin-pass".into(),
        dir,
    )
}

fn login(addr: &str, password: &str) -> String {
    let body = format!(r#"{{"password":"{password}"}}"#);
    let req = format!(
        "POST /api/auth/login HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
        body.len()
    );
    let resp = raw_http(addr, &req);
    assert_eq!(resp.status, 200, "login body={}", resp.body_str());
    let set_cookie = resp.header("set-cookie").expect("session cookie");
    set_cookie.split(';').next().unwrap().trim().to_string()
}

fn authed(method: &str, addr: &str, path: &str, cookie: &str, body: Option<&str>) -> HttpResponse {
    let body = body.unwrap_or("");
    let req = if body.is_empty() {
        format!(
            "{method} {path} HTTP/1.1\r\nHost: localhost\r\nCookie: {cookie}\r\nConnection: close\r\n\r\n"
        )
    } else {
        format!(
            "{method} {path} HTTP/1.1\r\nHost: localhost\r\nCookie: {cookie}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
            body.len()
        )
    };
    raw_http(addr, &req)
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn rest_contracts_globals_config_bots_trigger_override_info() {
    let (addr, password, _dir) = boot().await;
    let cookie = login(&addr, &password);

    let g = authed("GET", &addr, "/api/globals", &cookie, None);
    assert_eq!(g.status, 200, "{}", g.body_str());
    assert!(g.json().get("globals").is_some());

    let put_g = authed(
        "PUT",
        &addr,
        "/api/globals",
        &cookie,
        Some(r#"{"globals":{"hello":"world","  ":"x"," keep ":" v "}}"#),
    );
    assert_eq!(put_g.status, 200, "{}", put_g.body_str());
    let gj = put_g.json();
    assert_eq!(gj["ok"], true);
    assert_eq!(gj["globals"]["hello"], "world");
    assert_eq!(gj["globals"]["keep"], "v");

    let env = authed("GET", &addr, "/api/plugin-env", &cookie, None);
    assert_eq!(env.status, 200);
    assert!(env.json().get("plugin_env").is_some());
    let put_env = authed(
        "PUT",
        &addr,
        "/api/plugin-env",
        &cookie,
        Some(r#"{"plugin_env":{"FOO":"1"}}"#),
    );
    assert_eq!(put_env.status, 200, "{}", put_env.body_str());
    assert_eq!(put_env.json()["plugin_env"]["FOO"], "1");

    let put_cfg = authed(
        "PUT",
        &addr,
        "/api/config",
        &cookie,
        Some(r#"{"webui":{"auto_refresh":false,"refresh_interval":9},"global_sleep_timeout":33}"#),
    );
    assert_eq!(put_cfg.status, 200, "{}", put_cfg.body_str());
    let cfg = put_cfg.json();
    assert_eq!(cfg["webui"]["password"], "admin-pass");
    assert_eq!(cfg["webui"]["refresh_interval"], 9);
    assert_eq!(cfg["global_sleep_timeout"], 33);
    assert_eq!(cfg["plugins"]["demo.plugin"]["k"], "keep-me");

    let bots = authed("GET", &addr, "/api/bots", &cookie, None);
    assert_eq!(bots.status, 200, "{}", bots.body_str());
    let bj = bots.json();
    assert_eq!(bj["group_chat_only"], true);
    assert_eq!(bj["dedupe_key"], "group_id+real_seq");
    assert_eq!(bj["total_bots"], 0);
    assert!(bj["bots"].as_array().unwrap().is_empty());
    assert!(bj["stats"]["filtered_self_count"].is_number());
    assert!(bj["stats"]["filtered_non_group_count"].is_number());
    assert!(bj["stats"]["recv_count"].is_number());
    assert!(bj["global_recv_count"].is_number());
    assert!(bj["global_uptime"].as_str().is_some());

    let logs = authed("GET", &addr, "/api/trigger-logs", &cookie, None);
    assert_eq!(logs.status, 200, "{}", logs.body_str());
    let lj = logs.json();
    assert!(lj["records"].is_array());
    assert_eq!(lj["page"], 1);
    assert!(lj["stats"]["total_count"].is_number());

    let stats = authed("GET", &addr, "/api/trigger-logs/stats", &cookie, None);
    assert_eq!(stats.status, 200);
    assert!(stats.json()["total_count"].is_number());

    let ov = authed(
        "POST",
        &addr,
        "/api/plugins/demo.plugin/test-override",
        &cookie,
        Some(
            r#"{"input":"demo hi","overrides":[],"commands":[{"id":"cmd.demo","name":"demo","pattern":"^demo (?P<content>.+)$"}]}"#,
        ),
    );
    assert_eq!(ov.status, 200, "{}", ov.body_str());
    let oj = ov.json();
    assert_eq!(oj["result"], "demo hi");
    assert_eq!(oj["match_info"]["command_id"], "cmd.demo");
    assert_eq!(oj["match_info"]["groups"]["content"], "hi");

    let info = authed("GET", &addr, "/api/info", &cookie, None);
    assert_eq!(info.status, 400, "{}", info.body_str());
    let info2 = authed("GET", &addr, "/api/info?id=1&type=user", &cookie, None);
    assert_eq!(info2.status, 503, "{}", info2.body_str());
}
