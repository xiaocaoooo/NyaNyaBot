use std::collections::HashMap;
use std::sync::Arc;
use std::sync::atomic::{AtomicI64, Ordering};

use async_trait::async_trait;
use nyanyabot_proto::StructuredError;
use nyanyabot_proto::pb::host_service_server::HostService;
use nyanyabot_proto::pb::{
    CallDependencyRequest, CallDependencyResponse, CallOneBotRequest, CallOneBotResponse,
    GetStatsRequest, GetStatsResponse,
};
use serde_json::Value;
use std::sync::RwLock;
use tokio::sync::Mutex;
use tonic::{Request, Response, Status};

use crate::config::Store;
use crate::onebot::ob11::ApiResponse;
use crate::plugin::Manager;
use crate::stats::Stats;

pub type CallOneBotFn = Arc<
    dyn Fn(
            String,
            Value,
            i64,
            String,
        ) -> std::pin::Pin<
            Box<dyn std::future::Future<Output = Result<ApiResponse, anyhow::Error>> + Send>,
        > + Send
        + Sync,
>;

pub type EnsureAwakeFn = Arc<
    dyn Fn(
            String,
        )
            -> std::pin::Pin<Box<dyn std::future::Future<Output = Result<(), String>> + Send>>
        + Send
        + Sync,
>;

#[derive(Clone)]
pub struct SharedHostState {
    pub plugin_manager: Arc<Manager>,
    pub stats: Arc<Stats>,
    pub store: Arc<Store>,
    pub tokens: Arc<RwLock<HashMap<String, String>>>, // token -> plugin_id
    pub call_onebot: CallOneBotFn,
    pub plugin_sent: Arc<RwLock<HashMap<String, AtomicI64>>>,
    pub ensure_awake: Arc<RwLock<Option<EnsureAwakeFn>>>,
}

impl SharedHostState {
    pub fn bind_token(&self, plugin_id: &str, token: &str) {
        self.tokens
            .write()
            .unwrap()
            .insert(token.to_string(), plugin_id.to_string());
    }

    pub fn unbind_token(&self, token: &str) {
        self.tokens.write().unwrap().remove(token);
    }

    pub fn plugin_id_for_token(&self, token: &str) -> Option<String> {
        self.tokens.read().unwrap().get(token).cloned()
    }

    pub fn inc_plugin_sent(&self, plugin_id: &str) {
        let mut map = self.plugin_sent.write().unwrap();
        map.entry(plugin_id.to_string())
            .or_insert_with(|| AtomicI64::new(0))
            .fetch_add(1, Ordering::Relaxed);
    }
}

pub struct HostServiceImpl {
    pub state: SharedHostState,
}

#[async_trait]
impl HostService for HostServiceImpl {
    async fn call_one_bot(
        &self,
        request: Request<CallOneBotRequest>,
    ) -> Result<Response<CallOneBotResponse>, Status> {
        let token = nyanyabot_proto::extract_token(&request)
            .ok_or_else(|| Status::unauthenticated("missing plugin token"))?;
        let caller = self
            .state
            .plugin_id_for_token(&token)
            .ok_or_else(|| Status::unauthenticated("unknown plugin token"))?;

        let args = request.into_inner();
        let params: Value = if args.params_json.is_empty() {
            Value::Null
        } else {
            serde_json::from_slice(&args.params_json)
                .map_err(|e| Status::invalid_argument(e.to_string()))?
        };
        let fut = (self.state.call_onebot)(
            args.action.clone(),
            params,
            args.self_id,
            args.trace_id.clone(),
        );
        let resp = fut.await.map_err(|e| Status::internal(e.to_string()))?;
        self.state.inc_plugin_sent(&caller);
        self.state.stats.inc_sent_by_plugin(&caller);
        let response_json =
            serde_json::to_vec(&resp).map_err(|e| Status::internal(e.to_string()))?;
        Ok(Response::new(CallOneBotResponse { response_json }))
    }

    async fn call_dependency(
        &self,
        request: Request<CallDependencyRequest>,
    ) -> Result<Response<CallDependencyResponse>, Status> {
        let token = nyanyabot_proto::extract_token(&request)
            .ok_or_else(|| Status::unauthenticated("missing plugin token"))?;
        let caller = self
            .state
            .plugin_id_for_token(&token)
            .ok_or_else(|| Status::unauthenticated("unknown plugin token"))?;
        let args = request.into_inner();
        let cfg = self.state.store.get();
        if !cfg.is_plugin_enabled(&caller) {
            return Ok(Response::new(CallDependencyResponse {
                result_json: Vec::new(),
                error: Some(
                    StructuredError::forbidden(format!("plugin {caller:?} is disabled")).to_pb(),
                ),
            }));
        }
        if !cfg.is_plugin_enabled(&args.target_plugin_id) {
            return Ok(Response::new(CallDependencyResponse {
                result_json: Vec::new(),
                error: Some(
                    StructuredError::forbidden(format!(
                        "plugin {:?} is disabled",
                        args.target_plugin_id
                    ))
                    .to_pb(),
                ),
            }));
        }

        // Validate dependency declaration.
        let Some((_, caller_desc)) = self.state.plugin_manager.get(&caller).await else {
            return Ok(Response::new(CallDependencyResponse {
                result_json: Vec::new(),
                error: Some(
                    StructuredError::not_found(format!("caller plugin not found: {caller}"))
                        .to_pb(),
                ),
            }));
        };
        if !caller_desc
            .dependencies
            .iter()
            .any(|d| d == &args.target_plugin_id)
        {
            return Ok(Response::new(CallDependencyResponse {
                result_json: Vec::new(),
                error: Some(
                    StructuredError::forbidden(format!(
                        "plugin {caller} does not declare dependency on {}",
                        args.target_plugin_id
                    ))
                    .to_pb(),
                ),
            }));
        }

        let Some((_, target_desc)) = self.state.plugin_manager.get(&args.target_plugin_id).await
        else {
            return Ok(Response::new(CallDependencyResponse {
                result_json: Vec::new(),
                error: Some(
                    StructuredError::not_found(format!(
                        "target plugin not found: {}",
                        args.target_plugin_id
                    ))
                    .to_pb(),
                ),
            }));
        };
        if !target_desc.exports.iter().any(|e| e.name == args.method) {
            return Ok(Response::new(CallDependencyResponse {
                result_json: Vec::new(),
                error: Some(
                    StructuredError::not_found(format!("method not exported: {}", args.method))
                        .to_pb(),
                ),
            }));
        }

        let params: Value = if args.params_json.is_empty() {
            Value::Null
        } else {
            serde_json::from_slice(&args.params_json)
                .map_err(|e| Status::invalid_argument(e.to_string()))?
        };

        // Wake target if it was idle-stopped (true sleep). Process restart replaces Manager entry.
        let wake = self.state.ensure_awake.read().unwrap().clone();
        if let Some(wake) = wake
            && let Err(err) = wake(args.target_plugin_id.clone()).await
        {
            return Ok(Response::new(CallDependencyResponse {
                result_json: Vec::new(),
                error: Some(
                    StructuredError::internal(format!(
                        "failed to wake dependency {}: {err}",
                        args.target_plugin_id
                    ))
                    .to_pb(),
                ),
            }));
        }

        let Some((target, _)) = self.state.plugin_manager.get(&args.target_plugin_id).await else {
            return Ok(Response::new(CallDependencyResponse {
                result_json: Vec::new(),
                error: Some(
                    StructuredError::not_found(format!(
                        "dependency plugin not found after wake: {}",
                        args.target_plugin_id
                    ))
                    .to_pb(),
                ),
            }));
        };

        match target.invoke(&args.method, params, &caller).await {
            Ok(result) => {
                let result_json =
                    serde_json::to_vec(&result).map_err(|e| Status::internal(e.to_string()))?;
                Ok(Response::new(CallDependencyResponse {
                    result_json,
                    error: None,
                }))
            }
            Err(err) => Ok(Response::new(CallDependencyResponse {
                result_json: Vec::new(),
                error: Some(err.to_pb()),
            })),
        }
    }

    async fn get_stats(
        &self,
        request: Request<GetStatsRequest>,
    ) -> Result<Response<GetStatsResponse>, Status> {
        let token = nyanyabot_proto::extract_token(&request)
            .ok_or_else(|| Status::unauthenticated("missing plugin token"))?;
        if self.state.plugin_id_for_token(&token).is_none() {
            return Err(Status::unauthenticated("unknown plugin token"));
        }
        let snap = self.state.stats.snapshot();
        Ok(Response::new(GetStatsResponse {
            recv_count: snap.recv_count,
            sent_count: snap.sent_count,
            start_time: snap.start_time.to_rfc3339(),
            uptime: snap.uptime,
        }))
    }
}

/// Keep mutex imported for future use in process module coordination.
#[allow(dead_code)]
type _Unused = Mutex<()>;
