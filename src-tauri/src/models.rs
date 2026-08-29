use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::BTreeMap;

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Provider {
    Openai,
    /// OpenAI 的 /v1/responses 端点，与 Chat Completions 并列而非替代。
    #[serde(rename = "openai-responses")]
    OpenaiResponses,
    Anthropic,
    Google,
}

impl Provider {
    pub fn label(&self) -> &'static str {
        match self {
            Self::Openai => "OpenAI",
            Self::OpenaiResponses => "OpenAI Responses",
            Self::Anthropic => "Claude",
            Self::Google => "Google",
        }
    }

    /// 用于文件名：不含空格，避免路径里出现 "OpenAI Responses"。
    pub fn slug(&self) -> &'static str {
        match self {
            Self::Openai => "OpenAI",
            Self::OpenaiResponses => "OpenAI-Responses",
            Self::Anthropic => "Claude",
            Self::Google => "Google",
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Credentials {
    pub provider: Provider,
    pub base_url: String,
    pub api_key: String,
}

impl Credentials {
    pub fn validate(&self) -> Result<(), String> {
        if self.base_url.trim().is_empty() {
            return Err("请填写 API Base URL".to_string());
        }
        if !self.base_url.trim().starts_with("http://")
            && !self.base_url.trim().starts_with("https://")
        {
            return Err("API Base URL 必须以 http:// 或 https:// 开头".to_string());
        }
        if self.api_key.trim().is_empty() {
            return Err("请填写 API Key".to_string());
        }
        Ok(())
    }
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ListModelsRequest {
    pub credentials: Credentials,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ModelInfo {
    pub id: String,
    pub name: String,
    pub owned_by: Option<String>,
}

#[derive(Clone, Debug)]
pub struct CompletionRequest<'a> {
    pub credentials: &'a Credentials,
    pub model: &'a str,
    pub prompt: &'a str,
    pub system_prompt: Option<&'a str>,
    pub max_tokens: u32,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub struct CompletionObservation {
    pub content: String,
    pub upstream_model: String,
    pub system_fingerprint: String,
    pub prompt_tokens: u64,
    pub completion_tokens: u64,
    pub finish_reason: String,
    pub request_ms: u64,
    pub usage_reported: bool,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RunCheckRequest {
    pub credentials: Credentials,
    pub model: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RunAllRequest {
    pub credentials: Credentials,
    pub models: Vec<String>,
    /// 同时在飞的上游请求上限（请求级，非模型级）。缺省时侧车用 2。
    pub concurrency: Option<usize>,
    /// PDF 报告语言，跟随界面语言。缺省时侧车退回 zh-CN。
    pub lang: Option<String>,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ProgressEvent {
    pub index: usize,
    pub total: usize,
    pub probe: String,
    /// 机器可读阶段（starting / probe / probeFailed / done），前端据此本地化。
    pub phase: String,
    pub model: String,
    /// 侧车发的英文兜底文案，仅在 phase 无法识别时展示。
    pub message: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BatchProbeResult {
    pub probe_key: String,
    pub kind: String,
    pub status: String,
    #[serde(default)]
    pub evidence: BTreeMap<String, Value>,
    #[serde(default)]
    pub latency_ms: i64,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ModelReport {
    pub id: usize,
    pub model: String,
    pub trust_score: i32,
    pub verdict: String,
    pub request_count: usize,
    pub prompt_tokens: usize,
    pub completion_tokens: usize,
    pub total_tokens: usize,
    pub duration_ms: i64,
    #[serde(default)]
    pub error: String,
    pub results: Vec<BatchProbeResult>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BatchReport {
    pub id: String,
    pub provider: Provider,
    pub provider_label: String,
    pub base_url: String,
    pub started_at: String,
    pub finished_at: String,
    pub duration_ms: i64,
    pub total_models: usize,
    pub completed_models: usize,
    pub failed_models: usize,
    pub estimated_requests: usize,
    pub completed_requests: usize,
    pub trust_score: i32,
    pub verdict: String,
    pub pdf_path: String,
    pub models: Vec<ModelReport>,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ProbeResult {
    pub key: String,
    pub kind: String,
    pub label: String,
    pub description: String,
    pub status: String,
    pub weight: u32,
    pub duration_ms: u64,
    pub evidence: BTreeMap<String, Value>,
}

impl ProbeResult {
    pub fn new(
        key: &str,
        kind: &str,
        label: &str,
        description: &str,
        status: &str,
        weight: u32,
    ) -> Self {
        Self {
            key: key.to_string(),
            kind: kind.to_string(),
            label: label.to_string(),
            description: description.to_string(),
            status: status.to_string(),
            weight,
            duration_ms: 0,
            evidence: BTreeMap::new(),
        }
    }

    pub fn error(
        key: &str,
        kind: &str,
        label: &str,
        description: &str,
        weight: u32,
        error: String,
    ) -> Self {
        let mut result = Self::new(key, kind, label, description, "error", weight);
        result
            .evidence
            .insert("error".to_string(), Value::String(error));
        result
    }
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct HealthReport {
    pub id: String,
    pub provider: Provider,
    pub provider_label: String,
    pub base_url: String,
    pub model: String,
    pub started_at: String,
    pub finished_at: String,
    pub duration_ms: u64,
    pub trust_score: u32,
    pub verdict: String,
    pub request_count: u32,
    pub prompt_tokens: u64,
    pub completion_tokens: u64,
    pub results: Vec<ProbeResult>,
}
