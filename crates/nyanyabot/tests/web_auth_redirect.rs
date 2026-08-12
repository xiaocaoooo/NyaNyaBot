use std::io::{Read, Write};
use std::net::TcpStream;
use std::time::Duration;

use nyanyabot::config::Store;
use nyanyabot::pluginhost::PluginHost;
use nyanyabot::stats::Stats;
use nyanyabot::web::WebServer;
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
    let mut tmp = [0u8; 4096];
    loop {
        match stream.read(&mut tmp) {
            Ok(0) => break,
            Ok(n) => {
                buf.extend_from_slice(&tmp[..n]);
                if buf.windows(4).any(|w| w == b"\r\n\r\n") {
                    // For small responses we may already have full body; keep reading briefly.
                    if let Ok(n2) = stream.read(&mut tmp) {
                        if n2 == 0 {
                            break;
                        }
                        buf.extend_from_slice(&tmp[..n2]);
                    }
                    // Best-effort: if Content-Length satisfied, stop.
                    if let Some(header_end) = buf.windows(4).position(|w| w == b"\r\n\r\n") {
                        let header_bytes = &buf[..header_end];
                        let headers_text = String::from_utf8_lossy(header_bytes);
                        if let Some(cl) = headers_text
                            .lines()
                            .find(|l| l.to_ascii_lowercase().starts_with("content-length:"))
                        {
                            if let Ok(n) =
                                cl.split(':').nth(1).unwrap_or("0").trim().parse::<usize>()
                            {
                                let body_start = header_end + 4;
                                if buf.len() >= body_start + n {
                                    break;
                                }
                            }
                        } else {
                            break;
                        }
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

async fn start_web() -> (std::sync::Arc<WebServer>, String, String) {
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
        })
        .unwrap();

    let pm = nyanyabot::plugin::Manager::new();
    let stats = Stats::new();
    let call_onebot: nyanyabot::pluginhost::host_service::CallOneBotFn =
        std::sync::Arc::new(|_a, _p, _s, _t| {
            Box::pin(async {
                Ok(nyanyabot::onebot::ob11::ApiResponse {
                    status: "ok".into(),
                    retcode: 0,
                    data: serde_json::json!({}),
                    ..Default::default()
                })
            })
        });
    let host = PluginHost::new(pm.clone(), store.clone(), stats.clone(), call_onebot)
        .await
        .unwrap();
    let onebot = nyanyabot::onebot::reversews::Server::new(store.clone(), stats.clone());
    let trigger = nyanyabot::triggerlog::Recorder::new(&store.get().trigger_log);
    let web = WebServer::new(store, pm, stats, host, onebot, trigger, None);

    let web2 = web.clone();
    tokio::spawn(async move {
        web2.serve().await.unwrap();
    });
    tokio::time::sleep(Duration::from_millis(200)).await;

    let password = "admin-pass".to_string();
    (web, format!("127.0.0.1:{}", addr.port()), password)
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn unauthenticated_html_redirects_to_login() {
    let (web, addr, _) = start_web().await;

    let page = raw_http(
        &addr,
        "GET /plugins/?tab=installed HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
    );
    assert_eq!(page.status, 302, "expected Found redirect");
    assert_eq!(
        page.header("location"),
        Some("/login/?next=%2Fplugins%2F%3Ftab%3Dinstalled")
    );

    let home = raw_http(
        &addr,
        "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
    );
    assert_eq!(home.status, 302);
    assert_eq!(home.header("location"), Some("/login/?next=%2F"));

    web.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn unauthenticated_api_is_unauthorized() {
    let (web, addr, _) = start_web().await;

    let api = raw_http(
        &addr,
        "GET /api/config HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
    );
    assert_eq!(api.status, 401);
    assert!(api.body_str().contains("unauthorized"));

    let status = raw_http(
        &addr,
        "GET /api/auth/status HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
    );
    assert_eq!(status.status, 200);
    assert!(status.body_str().contains("\"authenticated\":false"));

    web.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn login_page_serves_login_html_not_dashboard() {
    let (web, addr, _) = start_web().await;

    let login = raw_http(
        &addr,
        "GET /login/ HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
    );
    assert_eq!(login.status, 200);
    let body = login.body_str();
    assert!(
        body.contains("nyanyabot-frontend-placeholder:login")
            || body.contains("data-nyanyabot-page=\"login\"")
            || body.contains("/login")
            || body.contains("app/login/"),
        "login route html missing login markers; got {} bytes",
        login.body.len()
    );

    let dashboard = raw_http(
        &addr,
        "GET /login/ HTTP/1.1\r\nHost: localhost\r\nCookie: nyanyabot_session=dead\r\nConnection: close\r\n\r\n",
    );
    // still public
    assert_eq!(dashboard.status, 200);

    web.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn login_logout_flow_sets_and_clears_session() {
    let (web, addr, password) = start_web().await;

    let bad_body = r#"{"password":"wrong-pass"}"#;
    let bad = raw_http(
        &addr,
        &format!(
            "POST /api/auth/login HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{bad_body}",
            bad_body.len()
        ),
    );
    assert_eq!(bad.status, 401, "body={}", bad.body_str());

    let body = format!(r#"{{"password":"{password}"}}"#);
    let login_req = format!(
        "POST /api/auth/login HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
        body.len()
    );
    let login = raw_http(&addr, &login_req);
    assert_eq!(login.status, 200, "body={}", login.body_str());
    let set_cookie = login
        .header("set-cookie")
        .expect("session cookie")
        .to_string();
    assert!(set_cookie.contains("nyanyabot_session="));
    let session = set_cookie.split(';').next().unwrap().trim().to_string();

    let authed = raw_http(
        &addr,
        &format!(
            "GET /api/config HTTP/1.1\r\nHost: localhost\r\nCookie: {session}\r\nConnection: close\r\n\r\n"
        ),
    );
    assert_eq!(authed.status, 200, "body={}", authed.body_str());

    let status = raw_http(
        &addr,
        &format!(
            "GET /api/auth/status HTTP/1.1\r\nHost: localhost\r\nCookie: {session}\r\nConnection: close\r\n\r\n"
        ),
    );
    assert!(status.body_str().contains("\"authenticated\":true"));

    let home = raw_http(
        &addr,
        &format!(
            "GET / HTTP/1.1\r\nHost: localhost\r\nCookie: {session}\r\nConnection: close\r\n\r\n"
        ),
    );
    assert_eq!(home.status, 200);
    let home_body = home.body_str();
    assert!(
        !home_body.contains("nyanyabot-frontend-placeholder:login")
            && !home_body.contains("data-nyanyabot-page=\"login\"")
            && !home_body.contains("app/login/"),
        "authenticated home should serve dashboard html, not login page"
    );
    assert!(
        home_body.contains("nyanyabot-frontend-placeholder:home")
            || home_body.contains("data-nyanyabot-page=\"home\"")
            || home_body.contains("NyaNyaBot"),
        "authenticated home missing home markers"
    );

    let logout = raw_http(
        &addr,
        &format!(
            "POST /api/auth/logout HTTP/1.1\r\nHost: localhost\r\nCookie: {session}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
        ),
    );
    assert_eq!(logout.status, 200);

    let after = raw_http(
        &addr,
        &format!(
            "GET /api/config HTTP/1.1\r\nHost: localhost\r\nCookie: {session}\r\nConnection: close\r\n\r\n"
        ),
    );
    assert_eq!(after.status, 401);

    web.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn plugins_page_requires_auth_and_serves_html() {
    let (web, addr, password) = start_web().await;

    let unauth = raw_http(
        &addr,
        "GET /plugins/ HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
    );
    assert_eq!(unauth.status, 302);
    assert_eq!(
        unauth.header("location"),
        Some("/login/?next=%2Fplugins%2F")
    );

    let body = format!(r#"{{"password":"{password}"}}"#);
    let login_req = format!(
        "POST /api/auth/login HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
        body.len()
    );
    let login = raw_http(&addr, &login_req);
    assert_eq!(login.status, 200, "body={}", login.body_str());
    let set_cookie = login
        .header("set-cookie")
        .expect("session cookie")
        .to_string();
    let session = set_cookie.split(';').next().unwrap().trim().to_string();

    for path in ["/plugins/", "/plugins"] {
        let page = raw_http(
            &addr,
            &format!(
                "GET {path} HTTP/1.1\r\nHost: localhost\r\nCookie: {session}\r\nConnection: close\r\n\r\n"
            ),
        );
        assert_eq!(page.status, 200, "path={path} body={}", page.body_str());
        let html = page.body_str();
        assert!(
            html.contains("nyanyabot-frontend-placeholder:plugins")
                || html.contains("data-nyanyabot-page=\"plugins\"")
                || html.contains("app/plugins/")
                || html.contains("/plugins"),
            "plugins html missing markers for {path}"
        );
    }

    web.shutdown().await;
}
