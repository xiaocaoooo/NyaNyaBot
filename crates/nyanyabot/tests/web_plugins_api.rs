use std::io::{Read, Write};
use std::net::TcpStream;
use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use nyanyabot::config::{PluginControl, Store};
use nyanyabot::plugin::{Manager, Plugin};
use nyanyabot::pluginhost::PluginHost;
use nyanyabot::stats::Stats;
use nyanyabot::web::WebServer;
use nyanyabot_proto::{
    CommandListener, CronListener, Descriptor, EventListener, StructuredError,
};
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
                        // no content-length; stop after some data
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
        plugin_id: "external.demo".into(),
        name: "Demo".into(),
        version: "1.0.0".into(),
        author: "t".into(),
        description: "demo".into(),
        commands: vec![
            CommandListener {
                id: "cmd.one".into(),
                name: "One".into(),
                description: "c1".into(),
                pattern: "one".into(),
                handler: "h".into(),
                ..Default::default()
            },
            CommandListener {
                id: "cmd.two".into(),
                name: "Two".into(),
                description: "c2".into(),
                pattern: "two".into(),
                handler: "h".into(),
                ..Default::default()
            },
        ],
        events: vec![EventListener {
            id: "evt.one".into(),
            name: "E1".into(),
            description: "e".into(),
            event: "message".into(),
            handler: "h".into(),
        }],
        crons: vec![CronListener {
            id: "cron.one".into(),
            name: "C1".into(),
            description: "c".into(),
            schedule: "* * * * *".into(),
            handler: "h".into(),
        }],
        ..Default::default()
    }
}

async fn start_web_with_demo() -> (Arc<WebServer>, String, String, tempfile::TempDir) {
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
            cfg.plugin_controls.insert(
                "external.demo".into(),
                PluginControl {
                    enabled: Some(false),
                    disabled_commands: vec!["cmd.two".into()],
                    ..Default::default()
                },
            );
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
    let web = WebServer::new(store, pm, stats, host, onebot, trigger);

    let web2 = web.clone();
    tokio::spawn(async move {
        web2.serve().await.unwrap();
    });
    tokio::time::sleep(Duration::from_millis(200)).await;
    (web, format!("127.0.0.1:{}", addr.port()), "admin-pass".into(), dir)
}

fn login(addr: &str, password: &str) -> String {
    let body = format!(r#"{{"password":"{password}"}}"#);
    let req = format!(
        "POST /api/auth/login HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
        body.len()
    );
    let resp = raw_http(addr, &req);
    assert_eq!(resp.status, 200, "login body={}", resp.body_str());
    let set_cookie = resp.header("set-cookie").expect("cookie").to_string();
    set_cookie.split(';').next().unwrap().trim().to_string()
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn plugins_list_is_array_with_state() {
    let (web, addr, password, _dir) = start_web_with_demo().await;

    let unauth = raw_http(
        &addr,
        "GET /api/plugins HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
    );
    assert_eq!(unauth.status, 401);

    let session = login(&addr, &password);
    let resp = raw_http(
        &addr,
        &format!(
            "GET /api/plugins HTTP/1.1\r\nHost: localhost\r\nCookie: {session}\r\nConnection: close\r\n\r\n"
        ),
    );
    assert_eq!(resp.status, 200, "body={}", resp.body_str());
    let body = resp.json();
    let arr = body.as_array().expect("top-level array");
    assert_eq!(arr.len(), 1);
    let item = &arr[0];
    assert_eq!(item["plugin_id"], "external.demo");
    assert_eq!(item["state"]["enabled"], false);
    assert_eq!(item["state"]["commands"]["cmd.one"], true);
    assert_eq!(item["state"]["commands"]["cmd.two"], false);
    assert_eq!(item["state"]["events"]["evt.one"], true);
    assert_eq!(item["state"]["status"], "Running");

    web.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn plugin_config_get_put_envelope() {
    let (web, addr, password, _dir) = start_web_with_demo().await;
    let session = login(&addr, &password);

    let get = raw_http(
        &addr,
        &format!(
            "GET /api/plugins/external.demo/config HTTP/1.1\r\nHost: localhost\r\nCookie: {session}\r\nConnection: close\r\n\r\n"
        ),
    );
    assert_eq!(get.status, 200, "{}", get.body_str());
    let g = get.json();
    assert_eq!(g["plugin_id"], "external.demo");
    assert!(g["config"].is_object());

    let body = r#"{"config":{"foo":1}}"#;
    let put = raw_http(
        &addr,
        &format!(
            "PUT /api/plugins/external.demo/config HTTP/1.1\r\nHost: localhost\r\nCookie: {session}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
            body.len()
        ),
    );
    assert_eq!(put.status, 200, "{}", put.body_str());
    assert_eq!(put.json()["ok"], true);

    let bad = r#"{"config":[1,2,3]}"#;
    let bad_resp = raw_http(
        &addr,
        &format!(
            "PUT /api/plugins/external.demo/config HTTP/1.1\r\nHost: localhost\r\nCookie: {session}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{bad}",
            bad.len()
        ),
    );
    assert_eq!(bad_resp.status, 400);

    let get2 = raw_http(
        &addr,
        &format!(
            "GET /api/plugins/external.demo/config HTTP/1.1\r\nHost: localhost\r\nCookie: {session}\r\nConnection: close\r\n\r\n"
        ),
    );
    assert_eq!(get2.json()["config"]["foo"], 1);

    web.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn plugin_switches_updates_state() {
    let (web, addr, password, _dir) = start_web_with_demo().await;
    let session = login(&addr, &password);

    let body = r#"{"enabled":true,"commands":{"cmd.one":false},"events":{"evt.one":false},"crons":{"cron.one":false},"prefix":"/@","enable_sleep":false,"sleep_timeout":30}"#;
    let put = raw_http(
        &addr,
        &format!(
            "PUT /api/plugins/external.demo/switches HTTP/1.1\r\nHost: localhost\r\nCookie: {session}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
            body.len()
        ),
    );
    assert_eq!(put.status, 200, "{}", put.body_str());
    let state = &put.json()["state"];
    assert_eq!(state["enabled"], true);
    assert_eq!(state["commands"]["cmd.one"], false);
    assert_eq!(state["events"]["evt.one"], false);
    assert_eq!(state["crons"]["cron.one"], false);
    assert_eq!(state["command_prefix"], "/@");
    assert_eq!(state["enable_sleep"], false);
    assert_eq!(state["sleep_timeout"], 30);

    let unknown = r#"{"commands":{"cmd.missing":false}}"#;
    let bad = raw_http(
        &addr,
        &format!(
            "PUT /api/plugins/external.demo/switches HTTP/1.1\r\nHost: localhost\r\nCookie: {session}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{unknown}",
            unknown.len()
        ),
    );
    assert_eq!(bad.status, 400);

    web.shutdown().await;
}
