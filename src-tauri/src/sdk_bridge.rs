use crate::models::{BatchReport, Credentials, ModelInfo, ProgressEvent, RunAllRequest};
use serde::{Deserialize, Serialize};
#[cfg(windows)]
use std::os::windows::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::sync::Mutex;
use tauri::{AppHandle, Emitter, Manager};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};

/// Windows 上 spawn 子进程默认会闪一个控制台窗口，必须显式抑制。
#[cfg(windows)]
const CREATE_NO_WINDOW: u32 = 0x0800_0000;

/// 正在跑的批量体检侧车。只存 PID 而不是 Child——Child 的所有权在
/// call_with_progress 里，wait_with_output 会消耗它，没法再借出去 kill。
static RUNNING_BATCH: Mutex<Option<u32>> = Mutex::new(None);

/// 用户点终止时调用。杀掉侧车进程，正在等待的 run_all 会因管道关闭而返回错误。
pub fn cancel_batch() -> Result<(), String> {
    let pid = RUNNING_BATCH
        .lock()
        .map_err(|_| "取消状态锁已损坏".to_string())?
        .ok_or_else(|| "当前没有正在运行的体检".to_string())?;
    kill_process(pid)
}

#[cfg(windows)]
fn kill_process(pid: u32) -> Result<(), String> {
    // taskkill /T 连带子进程一起收掉，避免留下孤儿
    let status = std::process::Command::new("taskkill")
        .args(["/PID", &pid.to_string(), "/T", "/F"])
        .creation_flags(CREATE_NO_WINDOW)
        .status()
        .map_err(|error| format!("无法终止侧车进程: {error}"))?;
    if status.success() {
        Ok(())
    } else {
        // 进程可能刚好自己退了，这不算失败
        Ok(())
    }
}

#[cfg(not(windows))]
fn kill_process(pid: u32) -> Result<(), String> {
    let status = std::process::Command::new("kill")
        .args(["-TERM", &pid.to_string()])
        .status()
        .map_err(|error| format!("无法终止侧车进程: {error}"))?;
    let _ = status;
    Ok(())
}

/// 进出作用域时登记 / 注销正在运行的 PID。用 RAII 是因为
/// call_with_progress 里有多条 `?` 提前返回的路径。
struct ChildGuard;

impl ChildGuard {
    fn register(pid: Option<u32>) -> Self {
        if let (Ok(mut slot), Some(pid)) = (RUNNING_BATCH.lock(), pid) {
            *slot = Some(pid);
        }
        Self
    }
}

impl Drop for ChildGuard {
    fn drop(&mut self) {
        if let Ok(mut slot) = RUNNING_BATCH.lock() {
            *slot = None;
        }
    }
}

/// 统一构造侧车进程：三条流全部 piped，Windows 上不弹窗。
fn sidecar_command(executable: &Path) -> tokio::process::Command {
    let mut command = tokio::process::Command::new(executable);
    command
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true);
    #[cfg(windows)]
    command.creation_flags(CREATE_NO_WINDOW);
    command
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct SdkRequest<'a> {
    action: &'a str,
    credentials: &'a Credentials,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct SdkResponse {
    #[serde(default)]
    models: Vec<ModelInfo>,
    report: Option<BatchReport>,
    error: Option<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct SidecarProgress {
    model: String,
    probe: String,
    completed_requests: usize,
    estimated_requests: usize,
    #[serde(default)]
    phase: String,
    message: String,
}

pub async fn list_models(
    app: &AppHandle,
    credentials: &Credentials,
) -> Result<Vec<ModelInfo>, String> {
    let response = call(
        app,
        &SdkRequest {
            action: "models",
            credentials,
        },
    )
    .await?;
    Ok(response.models)
}

pub async fn run_all(app: &AppHandle, request: RunAllRequest) -> Result<BatchReport, String> {
    request.credentials.validate()?;
    if request.models.is_empty() {
        return Err("请先拉取模型列表".to_string());
    }
    let output_dir = app
        .path()
        .download_dir()
        .or_else(|_| app.path().app_data_dir())
        .map_err(|error| format!("无法定位 PDF 输出目录: {error}"))?;
    std::fs::create_dir_all(&output_dir)
        .map_err(|error| format!("无法创建 PDF 输出目录: {error}"))?;
    let lang = request.lang.as_deref().unwrap_or("zh-CN");
    // 文件名跟随界面语言，避免英文界面导出中文文件名
    let stem = if lang.starts_with("zh") {
        "货源体检"
    } else {
        "supply-check"
    };
    let filename = format!(
        "{stem}-{}-{}.pdf",
        request.credentials.provider.slug(),
        chrono::Local::now().format("%Y%m%d-%H%M%S")
    );
    let output_path = output_dir.join(filename);
    let payload = serde_json::json!({
        "action": "runAll",
        "credentials": request.credentials,
        "models": request.models,
        "concurrency": request.concurrency.unwrap_or(2),
        "outputPath": output_path.to_string_lossy(),
        "lang": lang,
    });
    let response = call_with_progress(app, &payload).await?;
    response
        .report
        .ok_or_else(|| "SDK sidecar 没有返回批量体检报告".to_string())
}

async fn call_with_progress(
    app: &AppHandle,
    request: &serde_json::Value,
) -> Result<SdkResponse, String> {
    let executable = resolve_sidecar(app)?;
    let payload =
        serde_json::to_vec(request).map_err(|error| format!("无法编码批量 SDK 请求: {error}"))?;
    let mut child = sidecar_command(&executable)
        .spawn()
        .map_err(|error| format!("无法启动 SDK sidecar {}: {error}", executable.display()))?;
    // 登记 PID 供 cancel_batch 使用。ChildGuard 保证任何返回路径都会清理，
    // 否则下次点终止会打到一个已经退出的 PID。
    let _guard = ChildGuard::register(child.id());
    let stderr = child
        .stderr
        .take()
        .ok_or_else(|| "无法读取 SDK sidecar 进度流".to_string())?;
    let progress_app = app.clone();
    let progress_task = tokio::spawn(async move {
        let mut lines = BufReader::new(stderr).lines();
        let mut diagnostics = Vec::new();
        while let Ok(Some(line)) = lines.next_line().await {
            if let Ok(progress) = serde_json::from_str::<SidecarProgress>(&line) {
                let _ = progress_app.emit(
                    "healthcheck-progress",
                    ProgressEvent {
                        index: progress.completed_requests,
                        total: progress.estimated_requests,
                        probe: progress.probe,
                        phase: progress.phase,
                        model: progress.model,
                        message: progress.message,
                    },
                );
            } else if !line.trim().is_empty() {
                diagnostics.push(line);
            }
        }
        diagnostics.join("\n")
    });
    let mut stdin = child
        .stdin
        .take()
        .ok_or_else(|| "无法打开 SDK sidecar 标准输入".to_string())?;
    stdin
        .write_all(&payload)
        .await
        .map_err(|error| format!("无法写入批量 SDK 请求: {error}"))?;
    drop(stdin);
    let output = child
        .wait_with_output()
        .await
        .map_err(|error| format!("等待批量 SDK sidecar 失败: {error}"))?;
    let diagnostics = progress_task
        .await
        .map_err(|error| format!("读取 SDK 进度失败: {error}"))?;
    if !output.status.success() {
        return Err(format!("SDK sidecar 退出异常: {diagnostics}"));
    }
    let response: SdkResponse = serde_json::from_slice(&output.stdout).map_err(|error| {
        format!(
            "SDK sidecar 返回了无效批量 JSON: {error}; 输出: {}",
            String::from_utf8_lossy(&output.stdout)
                .chars()
                .take(500)
                .collect::<String>()
        )
    })?;
    if let Some(error) = response.error.as_deref() {
        return Err(error.to_string());
    }
    Ok(response)
}

/// 单次（非批量）调用的上限。批量路径靠 cancel_batch 兜底，这条路径没有
/// 用户可见的终止入口，所以必须自己超时，否则侧车挂住就是 UI 永久转圈。
const CALL_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(120);

async fn call(app: &AppHandle, request: &SdkRequest<'_>) -> Result<SdkResponse, String> {
    let executable = resolve_sidecar(app)?;
    let payload =
        serde_json::to_vec(request).map_err(|error| format!("无法编码 SDK 请求: {error}"))?;

    let mut child = sidecar_command(&executable)
        .spawn()
        .map_err(|error| format!("无法启动 SDK sidecar {}: {error}", executable.display()))?;
    let mut stdin = child
        .stdin
        .take()
        .ok_or_else(|| "无法打开 SDK sidecar 标准输入".to_string())?;
    stdin
        .write_all(&payload)
        .await
        .map_err(|error| format!("无法写入 SDK 请求: {error}"))?;
    // 必须关掉 stdin：侧车读到 EOF 才会开始干活。
    drop(stdin);

    // wait_with_output 会并发排空 stdout 与 stderr，两条管道都不会因为对侧
    // 缓冲写满而卡死。超时后 kill_on_drop 负责收掉进程。
    let output = match tokio::time::timeout(CALL_TIMEOUT, child.wait_with_output()).await {
        Ok(result) => result.map_err(|error| format!("等待 SDK sidecar 失败: {error}"))?,
        Err(_) => {
            return Err(format!(
                "SDK sidecar 超过 {} 秒没有响应",
                CALL_TIMEOUT.as_secs()
            ))
        }
    };
    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        return Err(format!("SDK sidecar 退出异常: {}", stderr.trim()));
    }
    let response: SdkResponse = serde_json::from_slice(&output.stdout).map_err(|error| {
        format!(
            "SDK sidecar 返回了无效 JSON: {error}; 输出: {}",
            String::from_utf8_lossy(&output.stdout)
                .chars()
                .take(500)
                .collect::<String>()
        )
    })?;
    if let Some(error) = response.error.as_deref() {
        return Err(error.to_string());
    }
    Ok(response)
}

fn resolve_sidecar(app: &AppHandle) -> Result<PathBuf, String> {
    let target_filename = sidecar_filename();
    let bundled_filename = bundled_sidecar_filename();
    let manifest = Path::new(env!("CARGO_MANIFEST_DIR"));
    let resource_dir = app
        .path()
        .resource_dir()
        .map_err(|error| format!("无法定位应用资源目录: {error}"))?;
    let mut candidates = vec![
        resource_dir.join(&bundled_filename),
        resource_dir.join(&target_filename),
        resource_dir.join("binaries").join(&bundled_filename),
        resource_dir.join("binaries").join(&target_filename),
    ];
    if let Ok(current_exe) = std::env::current_exe() {
        if let Some(executable_dir) = current_exe.parent() {
            candidates.push(executable_dir.join(&bundled_filename));
            candidates.push(executable_dir.join(&target_filename));
        }
    }
    candidates.push(manifest.join("binaries").join(&target_filename));
    candidates
        .into_iter()
        .find(|candidate| candidate.is_file())
        .ok_or_else(|| {
            format!("未找到 SDK sidecar {bundled_filename}，请先运行 bun run sidecar:build")
        })
}

fn sidecar_filename() -> String {
    let suffix = if cfg!(windows) { ".exe" } else { "" };
    format!("supply-check-sdk-{}{}", env!("BUILD_TARGET"), suffix)
}

fn bundled_sidecar_filename() -> String {
    let suffix = if cfg!(windows) { ".exe" } else { "" };
    format!("supply-check-sdk{suffix}")
}

#[cfg(test)]
mod tests {
    use super::{
        bundled_sidecar_filename, sidecar_filename, SdkRequest, SdkResponse, SidecarProgress,
    };
    use crate::models::{Credentials, Provider};

    #[test]
    fn sidecar_name_contains_target_triple() {
        assert!(sidecar_filename().contains(env!("BUILD_TARGET")));
    }

    #[test]
    fn bundled_sidecar_name_has_no_target_triple() {
        assert!(!bundled_sidecar_filename().contains(env!("BUILD_TARGET")));
    }

    fn credentials() -> Credentials {
        Credentials {
            provider: Provider::Openai,
            base_url: "https://api.openai.com/v1".to_string(),
            api_key: "sk-secret-value-do-not-leak".to_string(),
        }
    }

    /// The sidecar contract is camelCase on the wire. A silent rename here means
    /// the Go side reads a zero value and the audit runs on wrong parameters.
    #[test]
    fn request_serializes_camel_case_contract() {
        let creds = credentials();
        let payload = serde_json::to_value(SdkRequest {
            action: "models",
            credentials: &creds,
        })
        .expect("request must serialize");

        assert_eq!(payload["action"], "models");
        assert_eq!(payload["credentials"]["baseUrl"], "https://api.openai.com/v1");
        assert_eq!(payload["credentials"]["provider"], "openai");
        assert!(
            payload["credentials"].get("base_url").is_none(),
            "snake_case would not be understood by the sidecar"
        );
    }

    /// The API key must reach the sidecar over stdin, but it must never appear
    /// in a Debug rendering — those end up in logs and error strings.
    #[test]
    fn credentials_debug_does_not_expose_api_key() {
        let rendered = format!("{:?}", credentials());
        assert!(
            !rendered.contains("sk-secret-value-do-not-leak"),
            "API key leaked through Debug: {rendered}"
        );
    }

    #[test]
    fn progress_parses_sidecar_camel_case_fields() {
        let line = r#"{"kind":"progress","model":"gpt-4o","modelIndex":1,"modelTotal":2,
            "probe":"token_count","completedRequests":7,"estimatedRequests":60,
            "phase":"probe","message":"gpt-4o: token_count"}"#;
        let progress: SidecarProgress =
            serde_json::from_str(line).expect("progress line must parse");

        assert_eq!(progress.model, "gpt-4o");
        assert_eq!(progress.completed_requests, 7);
        assert_eq!(progress.estimated_requests, 60);
        assert_eq!(progress.phase, "probe");
    }

    /// A sidecar that omits `phase` must not abort the run — progress is
    /// cosmetic, and the batch itself is expensive to restart.
    #[test]
    fn progress_tolerates_missing_optional_phase() {
        let line = r#"{"model":"m","probe":"p","completedRequests":1,
            "estimatedRequests":60,"message":"x"}"#;
        let progress: SidecarProgress = serde_json::from_str(line).expect("must parse without phase");
        assert_eq!(progress.phase, "");
    }

    #[test]
    fn response_surfaces_sidecar_error() {
        let response: SdkResponse =
            serde_json::from_str(r#"{"error":"上游返回 401"}"#).expect("error response must parse");
        assert_eq!(response.error.as_deref(), Some("上游返回 401"));
        assert!(response.report.is_none());
        assert!(response.models.is_empty());
    }

    /// Coverage fields carry the INCONCLUSIVE audit trail; dropping them here
    /// would hide why a channel could not be measured.
    #[test]
    fn report_preserves_evidence_coverage_fields() {
        let response: SdkResponse = serde_json::from_str(
            r#"{"report":{"id":"desktop-1","provider":"openai","providerLabel":"OpenAI",
                "baseUrl":"https://x/v1","startedAt":"t","finishedAt":"t","durationMs":1,
                "totalModels":1,"completedModels":1,"failedModels":0,"estimatedRequests":60,
                "completedRequests":60,"trustScore":0,"verdict":"INCONCLUSIVE","pdfPath":"",
                "models":[
                  {"id":1,"model":"gpt-4o","trustScore":0,"verdict":"INCONCLUSIVE",
                   "requestCount":60,"promptTokens":1,"completionTokens":1,"totalTokens":2,
                   "durationMs":1,"results":[],"criticalErrorRate":1.0,"criticalErrors":5,
                   "criticalProbes":5,"insufficientReason":"identity_unmeasured"}]}}"#,
        )
        .expect("batch report must parse");

        let report = response.report.expect("report present");
        let model = &report.models[0];
        assert_eq!(model.verdict, "INCONCLUSIVE");
        assert_eq!(model.critical_error_rate, 1.0);
        assert_eq!(model.insufficient_reason.as_deref(), Some("identity_unmeasured"));
    }
}
