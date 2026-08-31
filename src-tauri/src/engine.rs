use crate::models::{
    CompletionObservation, CompletionRequest, HealthReport, ProbeResult, ProgressEvent, Provider,
    RunCheckRequest,
};
use crate::sdk_bridge;
use chrono::Utc;
use regex::Regex;
use serde_json::{json, Value};
use std::collections::BTreeSet;
use std::time::Instant;
use tauri::{AppHandle, Emitter};
use uuid::Uuid;

const TOKEN_PROMPT: &str = "The quick brown fox jumps over the lazy dog. Pack my box with five dozen liquor jugs. How vexingly quick daft zebras jump! The five boxing wizards jump quickly.";
const LENGTH_PROMPT: &str =
    "Output the integers from 1 to 64 separated by single spaces, and nothing else.";
const SELF_REPORT_PROMPT: &str =
    "What AI model are you, and which company developed you? Answer in one short sentence.";
const TOTAL_REQUESTS: usize = 7;

struct GoldenItem {
    prompt: &'static str,
    answer: &'static str,
}

const GOLDEN_ITEMS: [GoldenItem; 4] = [
    GoldenItem {
        prompt: "What is 37 multiplied by 14? Reply with only the number, no words.",
        answer: "518",
    },
    GoldenItem {
        prompt: "Reverse the letters of CLOUD and reply with only the reversed string.",
        answer: "DUOLC",
    },
    GoldenItem {
        prompt: "How many meters are in 9.5 kilometers? Reply with only the number.",
        answer: "9500",
    },
    GoldenItem {
        prompt: "What is 128 plus 347? Reply with only the number, no words.",
        answer: "475",
    },
];

pub async fn run(app: AppHandle, request: RunCheckRequest) -> Result<HealthReport, String> {
    request.credentials.validate()?;
    if request.model.trim().is_empty() {
        return Err("请选择模型".to_string());
    }

    let started_at = Utc::now();
    let started = Instant::now();
    let mut results = Vec::new();
    let mut observations = Vec::new();
    let mut prompt_tokens = 0_u64;
    let mut completion_tokens = 0_u64;

    emit_progress(&app, 1, "token_count", "校验协议、Token 与模型身份");
    let first = sdk_bridge::complete(
        &app,
        CompletionRequest {
            credentials: &request.credentials,
            model: &request.model,
            prompt: TOKEN_PROMPT,
            system_prompt: None,
            max_tokens: 16,
        },
    )
    .await;

    match first {
        Ok(observation) => {
            add_usage(&observation, &mut prompt_tokens, &mut completion_tokens);
            results.push(token_count_result(
                &request.credentials.provider,
                TOKEN_PROMPT,
                &observation,
            ));
            results.push(identity_result(&request.model, &observation));
            results.push(protocol_result(&observation));
            results.push(usage_result(&observation));
            observations.push(observation);
        }
        Err(error) => {
            results.push(ProbeResult::error(
                "p1_token_count",
                "token_count",
                "Token 计数校验",
                "核对上游输入 Token 是否存在异常膨胀",
                25,
                error.clone(),
            ));
            results.push(ProbeResult::error(
                "p3_identity",
                "identity",
                "模型身份校验",
                "比对请求模型与上游回传的模型标识",
                25,
                error.clone(),
            ));
            results.push(ProbeResult::error(
                "p10_protocol_contract",
                "protocol_contract",
                "协议契约",
                "确认端点能返回可解析的原生协议响应",
                0,
                error.clone(),
            ));
            results.push(ProbeResult::error(
                "p12_usage_reconciliation",
                "usage_reconciliation",
                "用量回传",
                "检查上游是否提供输入与输出 Token 用量",
                0,
                error,
            ));
        }
    }

    emit_progress(&app, 2, "length", "检查输出长度与响应延迟");
    let length = sdk_bridge::complete(
        &app,
        CompletionRequest {
            credentials: &request.credentials,
            model: &request.model,
            prompt: LENGTH_PROMPT,
            system_prompt: None,
            max_tokens: 256,
        },
    )
    .await;
    match length {
        Ok(observation) => {
            add_usage(&observation, &mut prompt_tokens, &mut completion_tokens);
            results.push(length_result(
                &request.credentials.provider,
                &request.model,
                &observation,
            ));
            results.push(latency_result(&observation));
            observations.push(observation);
        }
        Err(error) => {
            results.push(ProbeResult::error(
                "p2_length",
                "length",
                "输出长度校验",
                "检查完成 Token 与实际输出是否匹配",
                15,
                error.clone(),
            ));
            results.push(ProbeResult::error(
                "p5_latency",
                "latency",
                "响应延迟",
                "记录一次固定任务的完整响应耗时",
                5,
                error,
            ));
        }
    }

    let mut golden_answers = Vec::new();
    let mut golden_successes = 0_u32;
    for (offset, item) in GOLDEN_ITEMS.iter().enumerate() {
        emit_progress(
            &app,
            3 + offset,
            "golden",
            &format!("执行金标质量题 {}/{}", offset + 1, GOLDEN_ITEMS.len()),
        );
        match sdk_bridge::complete(
            &app,
            CompletionRequest {
                credentials: &request.credentials,
                model: &request.model,
                prompt: item.prompt,
                system_prompt: None,
                max_tokens: 512,
            },
        )
        .await
        {
            Ok(observation) => {
                add_usage(&observation, &mut prompt_tokens, &mut completion_tokens);
                let correct = golden_match(item.answer, &observation.content);
                golden_successes += 1;
                golden_answers.push(json!({
                    "prompt": item.prompt,
                    "expected": item.answer,
                    "got": observation.content,
                    "correct": correct,
                    "durationMs": observation.request_ms
                }));
                observations.push(observation);
            }
            Err(error) => golden_answers.push(json!({
                "prompt": item.prompt,
                "expected": item.answer,
                "got": "",
                "correct": false,
                "error": error
            })),
        }
    }
    results.push(golden_result(golden_answers, golden_successes));

    emit_progress(&app, 7, "self_report", "核对来源自述与响应指纹");
    let self_report = sdk_bridge::complete(
        &app,
        CompletionRequest {
            credentials: &request.credentials,
            model: &request.model,
            prompt: SELF_REPORT_PROMPT,
            system_prompt: None,
            max_tokens: 96,
        },
    )
    .await;
    match self_report {
        Ok(observation) => {
            add_usage(&observation, &mut prompt_tokens, &mut completion_tokens);
            results.push(self_report_result(&request.model, &observation));
            observations.push(observation);
        }
        Err(error) => results.push(ProbeResult::error(
            "p8_self_report",
            "self_report",
            "来源自述",
            "询问模型身份，识别错配模型与订阅套壳",
            15,
            error,
        )),
    }
    results.push(purity_result(&request.model, &observations));

    results.sort_by_key(|result| probe_order(&result.kind));
    let (trust_score, verdict) = score(&results);
    let finished_at = Utc::now();
    let duration_ms = started.elapsed().as_millis() as u64;
    emit_progress(&app, TOTAL_REQUESTS, "done", "体检完成");

    Ok(HealthReport {
        id: Uuid::new_v4().to_string(),
        provider: request.credentials.provider.clone(),
        provider_label: request.credentials.provider.label().to_string(),
        base_url: request.credentials.base_url.trim().to_string(),
        model: request.model.trim().to_string(),
        started_at: started_at.to_rfc3339(),
        finished_at: finished_at.to_rfc3339(),
        duration_ms,
        trust_score,
        verdict,
        request_count: TOTAL_REQUESTS as u32,
        prompt_tokens,
        completion_tokens,
        results,
    })
}

fn emit_progress(app: &AppHandle, index: usize, probe: &str, message: &str) {
    let _ = app.emit(
        "healthcheck-progress",
        ProgressEvent {
            index,
            total: TOTAL_REQUESTS,
            probe: probe.to_string(),
            // 遗留路径，前端已不调用。phase 留空让前端退回 message 兜底。
            phase: String::new(),
            model: String::new(),
            message: message.to_string(),
        },
    );
}

fn add_usage(observation: &CompletionObservation, input: &mut u64, output: &mut u64) {
    *input += observation.prompt_tokens;
    *output += observation.completion_tokens;
}

fn token_count_result(
    provider: &Provider,
    prompt: &str,
    observation: &CompletionObservation,
) -> ProbeResult {
    let mut result = ProbeResult::new(
        "p1_token_count",
        "token_count",
        "Token 计数校验",
        "核对上游输入 Token 是否存在异常膨胀",
        "skip",
        25,
    );
    result.duration_ms = observation.request_ms;
    let local = estimated_prompt_tokens(prompt);
    result.evidence.insert("localEstimate".into(), json!(local));
    result.evidence.insert(
        "upstreamPromptTokens".into(),
        json!(observation.prompt_tokens),
    );
    if observation.prompt_tokens == 0 || local == 0 {
        result
            .evidence
            .insert("reason".into(), json!("上游未回传输入 Token"));
        return result;
    }
    let ratio = observation.prompt_tokens as f64 / local as f64;
    let over_pct = (ratio - 1.0) * 100.0;
    result.evidence.insert("ratio".into(), json!(round2(ratio)));
    result
        .evidence
        .insert("overPct".into(), json!(round2(over_pct)));
    result.status = if over_pct >= 30.0 && *provider == Provider::Openai {
        "fail"
    } else if over_pct >= 15.0 {
        "warn"
    } else {
        "pass"
    }
    .to_string();
    result
}

fn identity_result(requested: &str, observation: &CompletionObservation) -> ProbeResult {
    let mut result = ProbeResult::new(
        "p3_identity",
        "identity",
        "模型身份校验",
        "比对请求模型与上游回传的模型标识",
        "skip",
        25,
    );
    result.duration_ms = observation.request_ms;
    result.evidence.insert("requested".into(), json!(requested));
    result
        .evidence
        .insert("upstreamModel".into(), json!(observation.upstream_model));
    if !observation.system_fingerprint.is_empty() {
        result.evidence.insert(
            "systemFingerprint".into(),
            json!(observation.system_fingerprint),
        );
    }
    if observation.upstream_model.trim().is_empty() {
        result
            .evidence
            .insert("reason".into(), json!("上游未回传模型标识"));
    } else if same_model_family(requested, &observation.upstream_model) {
        result.status = "pass".to_string();
    } else {
        result.status = "fail".to_string();
        result
            .evidence
            .insert("reason".into(), json!("上游模型家族与请求模型不一致"));
    }
    result
}

fn protocol_result(observation: &CompletionObservation) -> ProbeResult {
    let mut result = ProbeResult::new(
        "p10_protocol_contract",
        "protocol_contract",
        "协议契约",
        "确认端点能返回可解析的原生协议响应",
        "pass",
        0,
    );
    result.duration_ms = observation.request_ms;
    result
        .evidence
        .insert("finishReason".into(), json!(observation.finish_reason));
    result
}

fn usage_result(observation: &CompletionObservation) -> ProbeResult {
    let measured = observation.usage_reported
        && observation
            .prompt_tokens
            .saturating_add(observation.completion_tokens)
            > 0;
    let mut result = ProbeResult::new(
        "p12_usage_reconciliation",
        "usage_reconciliation",
        "用量回传",
        "检查上游是否提供输入与输出 Token 用量",
        if measured { "pass" } else { "skip" },
        0,
    );
    result.duration_ms = observation.request_ms;
    result
        .evidence
        .insert("promptTokens".into(), json!(observation.prompt_tokens));
    result.evidence.insert(
        "completionTokens".into(),
        json!(observation.completion_tokens),
    );
    if !measured {
        result
            .evidence
            .insert("reason".into(), json!("上游未提供完整 usage"));
    }
    result
}

fn length_result(
    provider: &Provider,
    model: &str,
    observation: &CompletionObservation,
) -> ProbeResult {
    let mut result = ProbeResult::new(
        "p2_length",
        "length",
        "输出长度校验",
        "检查完成 Token 与实际输出是否匹配",
        "skip",
        15,
    );
    result.duration_ms = observation.request_ms;
    let local = observation.content.split_whitespace().count() as u64;
    let content_ok = number_sequence_ok(&observation.content);
    result.evidence.insert(
        "completionTokens".into(),
        json!(observation.completion_tokens),
    );
    result.evidence.insert("localRecount".into(), json!(local));
    result
        .evidence
        .insert("contentOk".into(), json!(content_ok));
    if observation.completion_tokens == 0 || local == 0 {
        result
            .evidence
            .insert("reason".into(), json!("缺少可比对的输出 Token"));
        return result;
    }
    let ratio = observation.completion_tokens as f64 / local as f64;
    result.evidence.insert("ratio".into(), json!(round2(ratio)));
    let faithful_openai_tokenizer = *provider == Provider::Openai && !is_reasoning_model(model);
    result.status = if ratio > 1.25 && faithful_openai_tokenizer {
        "fail"
    } else if ratio > 1.25 || !content_ok {
        "warn"
    } else {
        "pass"
    }
    .to_string();
    result
}

fn latency_result(observation: &CompletionObservation) -> ProbeResult {
    let status = if observation.request_ms > 30_000 {
        "warn"
    } else {
        "pass"
    };
    let mut result = ProbeResult::new(
        "p5_latency",
        "latency",
        "响应延迟",
        "记录一次固定任务的完整响应耗时",
        status,
        5,
    );
    result.duration_ms = observation.request_ms;
    result
        .evidence
        .insert("requestMs".into(), json!(observation.request_ms));
    result.evidence.insert("thresholdMs".into(), json!(30_000));
    result
}

fn golden_result(answers: Vec<Value>, successful_requests: u32) -> ProbeResult {
    if successful_requests == 0 {
        let mut result = ProbeResult::error(
            "p4_golden",
            "golden",
            "金标质量测试",
            "用四道随机性低的短题检查基础能力",
            20,
            "所有金标请求均失败".to_string(),
        );
        result.evidence.insert("answers".into(), json!(answers));
        return result;
    }
    let correct = answers
        .iter()
        .filter(|answer| answer.get("correct").and_then(Value::as_bool) == Some(true))
        .count();
    let pass_pct = correct as f64 / answers.len() as f64 * 100.0;
    let status = if pass_pct < 60.0 {
        "fail"
    } else if pass_pct < 100.0 {
        "warn"
    } else {
        "pass"
    };
    let mut result = ProbeResult::new(
        "p4_golden",
        "golden",
        "金标质量测试",
        "用四道随机性低的短题检查基础能力",
        status,
        20,
    );
    result.evidence.insert("correct".into(), json!(correct));
    result.evidence.insert("total".into(), json!(answers.len()));
    result
        .evidence
        .insert("passPct".into(), json!(round2(pass_pct)));
    result.evidence.insert("answers".into(), json!(answers));
    result
}

fn self_report_result(requested: &str, observation: &CompletionObservation) -> ProbeResult {
    let content = observation.content.trim();
    let lowered = content.to_lowercase();
    let expected = expected_vendor(requested);
    let expected_confirmed = expected
        .map(|vendor| {
            vendor_markers(vendor)
                .iter()
                .any(|marker| lowered.contains(marker))
        })
        .unwrap_or(false);
    let wrong_vendor = ["openai", "anthropic", "google"]
        .into_iter()
        .find(|vendor| {
            Some(*vendor) != expected
                && vendor_markers(vendor)
                    .iter()
                    .any(|marker| lowered.contains(marker))
        });
    let wrapper = [
        "kiro",
        "cursor",
        "cline",
        "roo code",
        "windsurf",
        "github copilot",
        "copilot",
        "codeium",
        "perplexity",
        "poe",
        "trae",
    ]
    .into_iter()
    .find(|marker| lowered.contains(marker));

    let status = if content.is_empty() {
        "skip"
    } else if expected.is_some() && !expected_confirmed && wrong_vendor.is_some() {
        "fail"
    } else if wrapper.is_some() {
        "warn"
    } else if expected_confirmed {
        "pass"
    } else {
        "skip"
    };
    let mut result = ProbeResult::new(
        "p8_self_report",
        "self_report",
        "来源自述",
        "询问模型身份，识别错配模型与订阅套壳",
        status,
        15,
    );
    result.duration_ms = observation.request_ms;
    result.evidence.insert("requested".into(), json!(requested));
    result.evidence.insert(
        "reply".into(),
        json!(content.chars().take(320).collect::<String>()),
    );
    if let Some(vendor) = expected {
        result
            .evidence
            .insert("expectedVendor".into(), json!(vendor));
    }
    if let Some(vendor) = wrong_vendor {
        result
            .evidence
            .insert("claimedVendor".into(), json!(vendor));
    }
    if let Some(marker) = wrapper {
        result
            .evidence
            .insert("wrapperMarker".into(), json!(marker));
    }
    if status == "skip" {
        result.evidence.insert(
            "reason".into(),
            json!("回复未能明确确认模型来源，不据此扣分"),
        );
    }
    result
}

fn purity_result(requested: &str, observations: &[CompletionObservation]) -> ProbeResult {
    let raw = observations
        .iter()
        .map(|observation| observation.upstream_model.trim())
        .filter(|model| !model.is_empty())
        .map(str::to_string)
        .collect::<Vec<_>>();
    let versions = raw
        .iter()
        .map(|model| normalize_model(model))
        .collect::<BTreeSet<_>>();
    let families = raw
        .iter()
        .map(|model| model_family(model))
        .collect::<BTreeSet<_>>();
    let mismatch = raw.iter().any(|model| !same_model_family(requested, model));

    let status = if raw.is_empty() {
        "skip"
    } else if mismatch || families.len() > 1 {
        "fail"
    } else if versions.len() > 1 {
        "warn"
    } else {
        "pass"
    };
    let mut result = ProbeResult::new(
        "p20_channel_purity",
        "channel_purity",
        "渠道纯度",
        "聚合本次响应的模型标识，检查混路与版本漂移",
        status,
        15,
    );
    result.evidence.insert("requested".into(), json!(requested));
    result.evidence.insert("samples".into(), json!(raw.len()));
    result.evidence.insert(
        "models".into(),
        json!(versions.into_iter().collect::<Vec<_>>()),
    );
    if raw.is_empty() {
        result
            .evidence
            .insert("reason".into(), json!("上游没有回传模型标识"));
    }
    result
}

pub fn score(results: &[ProbeResult]) -> (u32, String) {
    let total_weight: u32 = results.iter().map(|result| result.weight).sum();
    let mut value = 100.0_f64;
    let mut measured = 0_u32;
    let mut any_fail = false;
    let mut identity_fail = false;
    let mut token_fail = false;
    let mut golden_fail = false;

    for result in results {
        let penalty = match result.status.as_str() {
            "fail" => 1.0,
            "warn" => 0.4,
            _ => 0.0,
        };
        if total_weight > 0 {
            value -= result.weight as f64 / total_weight as f64 * 100.0 * penalty;
        }
        if result.weight > 0 && matches!(result.status.as_str(), "pass" | "warn" | "fail") {
            measured += 1;
        }
        if result.status == "fail" {
            any_fail = true;
            identity_fail |= result.kind == "identity";
            token_fail |= result.kind == "token_count";
            golden_fail |= result.kind == "golden";
        }
    }
    let rounded = value.clamp(0.0, 100.0).round() as u32;
    if measured == 0 {
        return (rounded, "INCONCLUSIVE".to_string());
    }
    let mut verdict = if rounded < 50 {
        "WATERED"
    } else if rounded < 85 {
        "SUSPICIOUS"
    } else {
        "OK"
    };
    if identity_fail || (token_fail && golden_fail) {
        verdict = "WATERED";
    } else if any_fail && verdict == "OK" {
        verdict = "SUSPICIOUS";
    }
    (rounded, verdict.to_string())
}

fn estimated_prompt_tokens(text: &str) -> u64 {
    ((text.chars().count() as f64 / 4.0).round() as u64).saturating_add(8)
}

fn number_sequence_ok(content: &str) -> bool {
    let regex = Regex::new(r"\d+").expect("valid integer regex");
    let numbers = regex
        .find_iter(content)
        .filter_map(|item| item.as_str().parse::<u32>().ok())
        .collect::<Vec<_>>();
    numbers == (1..=64).collect::<Vec<_>>()
}

fn golden_match(expected: &str, content: &str) -> bool {
    let normalize = |value: &str| {
        value
            .chars()
            .filter(|ch| ch.is_ascii_alphanumeric() || *ch == '.')
            .collect::<String>()
            .to_ascii_uppercase()
    };
    normalize(expected) == normalize(content)
}

fn expected_vendor(model: &str) -> Option<&'static str> {
    let model = normalize_model(model);
    if model.starts_with("claude") {
        Some("anthropic")
    } else if model.starts_with("gemini") {
        Some("google")
    } else if model.starts_with("gpt")
        || model.starts_with("chatgpt")
        || model.starts_with("o1")
        || model.starts_with("o3")
        || model.starts_with("o4")
    {
        Some("openai")
    } else {
        None
    }
}

fn vendor_markers(vendor: &str) -> &'static [&'static str] {
    match vendor {
        "anthropic" => &["claude", "anthropic"],
        "google" => &["gemini", "google deepmind", "google"],
        _ => &["openai", "chatgpt"],
    }
}

fn normalize_model(model: &str) -> String {
    let normalized = model.trim().to_ascii_lowercase().replace('_', "-");
    let normalized = normalized
        .strip_prefix("models/")
        .unwrap_or(&normalized)
        .to_string();

    // Bedrock inference profiles add routing/provider qualifiers to the model
    // ID. They do not indicate a different underlying Claude family.
    if let Some(candidate) = bedrock_claude_candidate(&normalized) {
        return candidate.to_string();
    }

    normalized
}

fn model_family(model: &str) -> String {
    parse_model_identity(model).base
}

#[derive(Debug, PartialEq, Eq)]
struct CanonicalModelIdentity {
    base: String,
    snapshot: Option<String>,
}

fn parse_model_identity(model: &str) -> CanonicalModelIdentity {
    let normalized_input = model.trim().to_ascii_lowercase().replace('_', "-");
    let bedrock_qualified = bedrock_claude_candidate(&normalized_input).is_some();
    let mut model = normalize_model(model);

    // Fine-tuned deployment IDs are opaque; date/preview-shaped job names are
    // part of their identity and must never be normalized away.
    if model.starts_with("ft:") {
        return CanonicalModelIdentity {
            base: model,
            snapshot: None,
        };
    }

    // Strip only known Claude provider metadata. Arbitrary :/@/-vN suffixes
    // remain identity-bearing (for example gpt-5:mini is not gpt-5).
    if model.starts_with("claude-") {
        if let Some(index) = bedrock_revision_suffix_start(&model) {
            model.truncate(index);
        } else if bedrock_qualified && has_numeric_v_suffix(&model) {
            model.truncate(model.rfind('-').unwrap_or(model.len()));
        }
        if let Some((base, snapshot)) = model.rsplit_once('@') {
            if is_compact_date(snapshot) || is_dashed_date(snapshot) {
                model = base.to_string();
            }
        }
    }
    let mut preview_stripped = false;
    if model.starts_with("gemini-") {
        if let Some(index) = preview_suffix_start(&model) {
            model.truncate(index);
            preview_stripped = true;
        }
    }
    let mut metadata_stripped = preview_stripped;
    if !metadata_stripped {
        if let Some(index) = snapshot_date_start(&model) {
            model.truncate(index);
            metadata_stripped = true;
        }
    }
    if !metadata_stripped && !model.starts_with("gemini-") {
        if let Some(base) = model.strip_suffix("-latest") {
            model = base.to_string();
        }
    }
    // Claude IDs occur in both generation-first and flavor-first order. Keep
    // both dimensions while normalizing that spelling difference.
    if let Some(rest) = model.strip_prefix("claude-") {
        let mut versions = Vec::new();
        let mut flavor = None;
        let mut modifiers = Vec::new();
        for part in rest.split('-') {
            if matches!(part, "opus" | "sonnet" | "haiku") {
                flavor = Some(part);
            } else if is_numeric_version_part(part) {
                versions.push(part);
            } else if !part.is_empty() {
                modifiers.push(part);
            }
        }
        let mut canonical = vec!["claude"];
        canonical.extend(versions);
        if let Some(flavor) = flavor {
            canonical.push(flavor);
        }
        canonical.extend(modifiers);
        model = canonical.join("-");
    }

    let snapshot = if model.starts_with("gemini-") && !metadata_stripped {
        if let Some((base, revision)) = gemini_revision_suffix(&model) {
            let base = base.to_string();
            let revision = revision.to_string();
            model = base;
            metadata_stripped = true;
            Some(revision)
        } else {
            None
        }
    } else {
        None
    };

    // Apply the alias only after checking immutable Gemini revisions. Thus a
    // malformed chained suffix such as -002-latest remains identity-bearing.
    if !metadata_stripped && model.starts_with("gemini-") {
        if let Some(base) = model.strip_suffix("-latest") {
            model = base.to_string();
        }
    }

    CanonicalModelIdentity {
        base: model,
        snapshot,
    }
}

fn bedrock_claude_candidate(normalized: &str) -> Option<&str> {
    [
        "global.anthropic.",
        "us.anthropic.",
        "eu.anthropic.",
        "apac.anthropic.",
        "global-anthropic.",
        "us-anthropic.",
        "eu-anthropic.",
        "apac-anthropic.",
    ]
    .iter()
    .find_map(|prefix| normalized.strip_prefix(prefix))
    .filter(|candidate| candidate.starts_with("claude-"))
}

fn same_model_family(requested: &str, observed: &str) -> bool {
    let requested = parse_model_identity(requested);
    let observed = parse_model_identity(observed);
    requested.base == observed.base
        && (requested.snapshot.is_none()
            || observed.snapshot.is_none()
            || requested.snapshot == observed.snapshot)
}

fn snapshot_date_start(model: &str) -> Option<usize> {
    if let Some((base, suffix)) = model.rsplit_once('-') {
        if is_compact_date(suffix) {
            return Some(base.len());
        }
    }
    let mut parts = model.rsplitn(4, '-');
    let day = parts.next()?;
    let month = parts.next()?;
    let year = parts.next()?;
    let base = parts.next()?;
    if is_two_digits(day)
        && is_two_digits(month)
        && year.len() == 4
        && year.starts_with("20")
        && year.chars().all(|character| character.is_ascii_digit())
    {
        return Some(base.len());
    }
    None
}

fn preview_suffix_start(model: &str) -> Option<usize> {
    let index = model.rfind("-preview")?;
    let suffix = &model[index + "-preview".len()..];
    if suffix.is_empty() {
        return Some(index);
    }
    let suffix = suffix.strip_prefix('-')?;
    if is_month_day(suffix) || is_compact_date(suffix) || is_dashed_date(suffix) {
        return Some(index);
    }
    None
}

fn bedrock_revision_suffix_start(model: &str) -> Option<usize> {
    let (revision, subrevision) = model.rsplit_once(':')?;
    if subrevision.is_empty()
        || !subrevision
            .chars()
            .all(|character| character.is_ascii_digit())
    {
        return None;
    }
    let (base, version) = revision.rsplit_once("-v")?;
    (!version.is_empty() && version.chars().all(|character| character.is_ascii_digit()))
        .then_some(base.len())
}

fn has_numeric_v_suffix(model: &str) -> bool {
    model.rsplit('-').next().is_some_and(|part| {
        part.len() > 1
            && part.starts_with('v')
            && part[1..]
                .chars()
                .all(|character| character.is_ascii_digit())
    })
}

fn gemini_revision_suffix(model: &str) -> Option<(&str, &str)> {
    let (base, revision) = model.rsplit_once('-')?;
    (revision.len() == 3 && revision.chars().all(|character| character.is_ascii_digit()))
        .then_some((base, revision))
}

fn is_two_digits(value: &str) -> bool {
    value.len() == 2 && value.chars().all(|character| character.is_ascii_digit())
}

fn is_month_day(value: &str) -> bool {
    value
        .split_once('-')
        .is_some_and(|(month, day)| is_two_digits(month) && is_two_digits(day))
}

fn is_compact_date(value: &str) -> bool {
    value.len() == 8
        && value.starts_with("20")
        && value.chars().all(|character| character.is_ascii_digit())
}

fn is_dashed_date(value: &str) -> bool {
    let mut parts = value.split('-');
    let Some(year) = parts.next() else {
        return false;
    };
    let Some(month) = parts.next() else {
        return false;
    };
    let Some(day) = parts.next() else {
        return false;
    };
    parts.next().is_none()
        && year.len() == 4
        && year.starts_with("20")
        && year.chars().all(|character| character.is_ascii_digit())
        && is_two_digits(month)
        && is_two_digits(day)
}

fn is_numeric_version_part(part: &str) -> bool {
    !part.is_empty()
        && part
            .chars()
            .all(|character| character.is_ascii_digit() || character == '.')
        && part.chars().any(|character| character.is_ascii_digit())
}

fn is_reasoning_model(model: &str) -> bool {
    let model = normalize_model(model);
    model.starts_with("o1")
        || model.starts_with("o3")
        || model.starts_with("o4")
        || model.starts_with("gpt-5")
}

fn probe_order(kind: &str) -> usize {
    match kind {
        "token_count" => 1,
        "length" => 2,
        "identity" => 3,
        "golden" => 4,
        "latency" => 5,
        "self_report" => 6,
        "channel_purity" => 7,
        "protocol_contract" => 8,
        "usage_reconciliation" => 9,
        _ => 99,
    }
}

fn round2(value: f64) -> f64 {
    (value * 100.0).round() / 100.0
}

#[cfg(test)]
mod tests {
    use super::{number_sequence_ok, same_model_family, score};
    use crate::models::ProbeResult;

    #[test]
    fn accepts_expected_number_sequence() {
        let content = (1..=64)
            .map(|number| number.to_string())
            .collect::<Vec<_>>()
            .join(" ");
        assert!(number_sequence_ok(&content));
        assert!(!number_sequence_ok("1 2 3 5"));
    }

    #[test]
    fn dated_model_snapshots_share_family() {
        assert!(same_model_family(
            "claude-sonnet-4-5",
            "claude-sonnet-4-5-20250929"
        ));
        assert!(same_model_family(
            "claude-haiku-4-5-20251001",
            "global.anthropic.claude-haiku-4-5-20251001-v1:0"
        ));
        assert!(!same_model_family("gpt-5", "claude-sonnet-4-5"));
    }

    #[test]
    fn identity_family_preserves_generation_and_variant() {
        assert!(!same_model_family(
            "claude-3-haiku-20240307",
            "claude-haiku-4-5-20251001"
        ));
        assert!(same_model_family(
            "claude-haiku-4-5-20251001",
            "global.anthropic.claude-haiku-4-5-20251115-v1:0"
        ));
        assert!(same_model_family(
            "claude-haiku-4-5-20251001",
            "GLOBAL_ANTHROPIC.CLAUDE_HAIKU_4_5_20251001_V1:0"
        ));
        assert!(!same_model_family(
            "gemini-2.5-pro-preview-03-25",
            "models/gemini-2.5-flash-20250605"
        ));
        assert!(same_model_family(
            "models/gemini-2.5-pro-preview-03-25",
            "gemini-2.5-pro-20250605"
        ));
        assert!(same_model_family("gemini-1.5-pro", "gemini-1.5-pro-002"));
        assert!(!same_model_family(
            "gemini-1.5-pro-001",
            "gemini-1.5-pro-002"
        ));
        assert!(!same_model_family("gpt-4", "gpt-4o"));
        assert!(!same_model_family("gpt-5", "gpt-5-mini"));
        assert!(same_model_family("gpt-5-mini", "gpt-5-mini-2026-01-01"));
        assert!(!same_model_family(
            "claude-3-haiku",
            "claude-3-haiku-thinking"
        ));
        assert!(!same_model_family("gpt-5", "gpt-5-v"));
        assert!(!same_model_family("gpt-5", "gpt-5-v2"));
        assert!(!same_model_family("gpt-5", "gpt-5:mini"));
        assert!(!same_model_family("gpt-5", "gpt-5@mini"));
        assert!(!same_model_family(
            "ft:gpt-4o-mini:org:job-a",
            "ft:gpt-4o-mini:org:job-b"
        ));
        assert!(!same_model_family(
            "ft:gpt-4o-mini:org:job",
            "ft:gpt-4o-mini:org:job-20260101"
        ));
        assert!(!same_model_family(
            "claude-haiku-4-5",
            "evil.anthropic.claude-haiku-4-5-v1:0"
        ));
        assert!(!same_model_family(
            "claude-haiku-4-5-anthropic.fake",
            "claude-haiku-4-5-anthropic.fake-v2"
        ));
        assert!(!same_model_family("gpt-5", "gpt-5-20260101-preview"));
        assert!(!same_model_family(
            "gemini-1.5-pro",
            "gemini-1.5-pro-002-latest"
        ));
        assert!(!same_model_family(
            "gemini-2.5-pro",
            "gemini-2.5-pro-20260101-preview"
        ));
        assert!(!same_model_family(
            "gemini-1.5-pro",
            "gemini-1.5-pro-002-preview"
        ));
        assert!(!same_model_family("gpt-5", "gpt-5-latest-20260101"));
        assert!(!same_model_family(
            "gemini-2.5-pro",
            "gemini-2.5-pro-latest-20260101"
        ));
        assert!(!same_model_family(
            "gemini-1.5-pro",
            "gemini-1.5-pro-002-20260101"
        ));
        assert!(!same_model_family(
            "claude-3-haiku",
            "claude-latest-3-haiku"
        ));
        assert!(!same_model_family("gpt-5", "gpt-5-2026mini"));
        assert!(!same_model_family(
            "gemini-2.5-pro",
            "gemini-2.5-pro-preview2"
        ));
        assert!(!same_model_family("o1", "o1-preview"));
    }

    #[test]
    fn identity_failure_forces_watered_verdict() {
        let results = vec![
            ProbeResult::new("p1", "token_count", "", "", "pass", 25),
            ProbeResult::new("p3", "identity", "", "", "fail", 25),
            ProbeResult::new("p4", "golden", "", "", "pass", 20),
        ];
        let (_, verdict) = score(&results);
        assert_eq!(verdict, "WATERED");
    }

    #[test]
    fn all_errors_are_inconclusive() {
        let results = vec![ProbeResult::new("p1", "token_count", "", "", "error", 25)];
        assert_eq!(score(&results), (100, "INCONCLUSIVE".to_string()));
    }
}
