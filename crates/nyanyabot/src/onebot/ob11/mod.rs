use serde::{Deserialize, Serialize};
use serde_json::Value;

pub type Event = Value;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ApiRequest {
    pub action: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub params: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub echo: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ApiResponse {
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub retcode: i64,
    #[serde(default)]
    pub msg: String,
    #[serde(default)]
    pub wording: String,
    #[serde(default)]
    pub data: Value,
    #[serde(default)]
    pub echo: String,
}
