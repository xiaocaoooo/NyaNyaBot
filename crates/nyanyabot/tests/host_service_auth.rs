use std::sync::Arc;

use async_trait::async_trait;
use nyanyabot::config::{PluginControl, Store};
use nyanyabot::onebot::ob11::ApiResponse;
use nyanyabot::plugin::{Manager, Plugin};
use nyanyabot::pluginhost::host_service::{HostServiceImpl, SharedHostState};
use nyanyabot::stats::Stats;
use nyanyabot_proto::pb::host_service_client::HostServiceClient;
use nyanyabot_proto::pb::host_service_server::HostServiceServer;
use nyanyabot_proto::pb::{CallDependencyRequest, CallOneBotRequest, GetStatsRequest};
use nyanyabot_proto::{CommandMatch, Descriptor, ExportSpec, StructuredError, TokenInterceptor};
use serde_json::{Value, json};
use tokio::sync::Mutex;
use tonic::Request;
use tonic::transport::Server;

struct DepPlugin;

#[async_trait]
impl Plugin for DepPlugin {
    async fn descriptor(&self) -> Result<Descriptor, StructuredError> {
        Ok(Descriptor {
            name: "Dep".into(),
            plugin_id: "test.dep".into(),
            version: "0.1.0".into(),
            author: "t".into(),
            description: "d".into(),
            exports: vec![ExportSpec {
                name: "ping".into(),
                description: "ping".into(),
                params_schema: json!({}),
                result_schema: json!({}),
            }],
            ..Default::default()
        })
    }
    async fn configure(&self, _: Value) -> Result<(), StructuredError> {
        Ok(())
    }
    async fn invoke(
        &self,
        method: &str,
        _params: Value,
        caller: &str,
    ) -> Result<Value, StructuredError> {
        if method != "ping" {
            return Err(StructuredError::not_found("nope"));
        }
        Ok(json!({"pong": true, "caller": caller}))
    }
    async fn handle(
        &self,
        _: &str,
        _: Value,
        _: Option<CommandMatch>,
        _: &str,
    ) -> Result<nyanyabot_proto::HandleResult, StructuredError> {
        Ok(nyanyabot_proto::HandleResult::default())
    }
    async fn status(&self) -> Result<String, StructuredError> {
        Ok("OK".into())
    }
    async fn shutdown(&self) -> Result<(), StructuredError> {
        Ok(())
    }
}

struct CallerPlugin;

#[async_trait]
impl Plugin for CallerPlugin {
    async fn descriptor(&self) -> Result<Descriptor, StructuredError> {
        Ok(Descriptor {
            name: "Caller".into(),
            plugin_id: "test.caller".into(),
            version: "0.1.0".into(),
            author: "t".into(),
            description: "c".into(),
            dependencies: vec!["test.dep".into()],
            ..Default::default()
        })
    }
    async fn configure(&self, _: Value) -> Result<(), StructuredError> {
        Ok(())
    }
    async fn invoke(&self, _: &str, _: Value, _: &str) -> Result<Value, StructuredError> {
        Err(StructuredError::not_found("none"))
    }
    async fn handle(
        &self,
        _: &str,
        _: Value,
        _: Option<CommandMatch>,
        _: &str,
    ) -> Result<nyanyabot_proto::HandleResult, StructuredError> {
        Ok(nyanyabot_proto::HandleResult::default())
    }
    async fn status(&self) -> Result<String, StructuredError> {
        Ok("OK".into())
    }
    async fn shutdown(&self) -> Result<(), StructuredError> {
        Ok(())
    }
}

#[tokio::test]
async fn host_service_token_and_dependency() {
    let pm = Manager::new();
    let mut dep_desc = DepPlugin.descriptor().await.unwrap();
    let mut caller_desc = CallerPlugin.descriptor().await.unwrap();
    nyanyabot_proto::ensure_descriptor_arrays(&mut dep_desc);
    nyanyabot_proto::ensure_descriptor_arrays(&mut caller_desc);
    pm.register(Arc::new(DepPlugin), dep_desc).await.unwrap();
    pm.register(Arc::new(CallerPlugin), caller_desc)
        .await
        .unwrap();

    let stats = Stats::new();
    let dir = tempfile::tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    store.load_or_create_default().unwrap();
    store
        .update(|cfg| {
            cfg.plugin_controls.insert(
                "test.caller".into(),
                PluginControl {
                    enabled: Some(true),
                    ..Default::default()
                },
            );
            cfg.plugin_controls.insert(
                "test.dep".into(),
                PluginControl {
                    enabled: Some(true),
                    ..Default::default()
                },
            );
        })
        .unwrap();
    let onebot_calls = Arc::new(Mutex::new(0u32));
    let onebot_calls2 = onebot_calls.clone();
    let state = SharedHostState {
        plugin_manager: pm,
        stats: stats.clone(),
        store,
        tokens: Arc::new(std::sync::RwLock::new(Default::default())),
        call_onebot: Arc::new(move |_, _, _, _| {
            let c = onebot_calls2.clone();
            Box::pin(async move {
                *c.lock().await += 1;
                Ok(ApiResponse {
                    status: "ok".into(),
                    retcode: 0,
                    ..Default::default()
                })
            })
        }),
        plugin_sent: Arc::new(std::sync::RwLock::new(Default::default())),
        ensure_awake: Arc::new(std::sync::RwLock::new(None)),
        command_reactions: nyanyabot::reaction::CommandReactionTracker::new(),
    };
    state.bind_token("test.caller", "caller-token");
    state.bind_token("test.dep", "dep-token");

    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let incoming = tokio_stream::wrappers::TcpListenerStream::new(listener);
    let svc_state = state.clone();
    tokio::spawn(async move {
        Server::builder()
            .add_service(HostServiceServer::new(HostServiceImpl { state: svc_state }))
            .serve_with_incoming(incoming)
            .await
            .unwrap();
    });

    // missing token
    let mut raw = HostServiceClient::connect(format!("http://{addr}"))
        .await
        .unwrap();
    let err = raw
        .get_stats(Request::new(GetStatsRequest {}))
        .await
        .unwrap_err();
    assert_eq!(err.code(), tonic::Code::Unauthenticated);

    // with token: get stats + call onebot + dependency
    let channel = tonic::transport::Channel::from_shared(format!("http://{addr}"))
        .unwrap()
        .connect()
        .await
        .unwrap();
    let mut client =
        HostServiceClient::with_interceptor(channel, TokenInterceptor::new("caller-token"));

    let st = client
        .get_stats(Request::new(GetStatsRequest {}))
        .await
        .unwrap()
        .into_inner();
    assert!(!st.uptime.is_empty());

    client
        .call_one_bot(Request::new(CallOneBotRequest {
            action: "send_private_msg".into(),
            params_json: serde_json::to_vec(&json!({"user_id": 1, "message": "hi"})).unwrap(),
            trace_id: "t".into(),
            self_id: 0,
        }))
        .await
        .unwrap();
    assert_eq!(*onebot_calls.lock().await, 1);

    let dep = client
        .call_dependency(Request::new(CallDependencyRequest {
            target_plugin_id: "test.dep".into(),
            method: "ping".into(),
            params_json: b"{}".to_vec(),
        }))
        .await
        .unwrap()
        .into_inner();
    assert!(dep.error.is_none());
    let v: Value = serde_json::from_slice(&dep.result_json).unwrap();
    assert_eq!(v["pong"], json!(true));
    assert_eq!(v["caller"], json!("test.caller"));

    // undeclared dependency from dep-token calling itself wrongly target without export path
    let channel2 = tonic::transport::Channel::from_shared(format!("http://{addr}"))
        .unwrap()
        .connect()
        .await
        .unwrap();
    let mut dep_client =
        HostServiceClient::with_interceptor(channel2, TokenInterceptor::new("dep-token"));
    let forbidden = dep_client
        .call_dependency(Request::new(CallDependencyRequest {
            target_plugin_id: "test.caller".into(),
            method: "x".into(),
            params_json: b"{}".to_vec(),
        }))
        .await
        .unwrap()
        .into_inner();
    assert!(forbidden.error.is_some());
    let code = forbidden.error.unwrap().code;
    // FORBIDDEN maps to enum value
    assert!(code != 0);
}
