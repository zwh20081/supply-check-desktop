mod engine;
mod models;
mod sdk_bridge;

use models::{
    BatchReport, HealthReport, ListModelsRequest, ModelInfo, RunAllRequest, RunCheckRequest,
};
use std::path::Path;
use tauri::AppHandle;

#[tauri::command]
async fn list_models(app: AppHandle, request: ListModelsRequest) -> Result<Vec<ModelInfo>, String> {
    request.credentials.validate()?;
    sdk_bridge::list_models(&app, &request.credentials).await
}

#[tauri::command]
async fn run_healthcheck(app: AppHandle, request: RunCheckRequest) -> Result<HealthReport, String> {
    engine::run(app, request).await
}

#[tauri::command]
async fn run_all_healthchecks(
    app: AppHandle,
    request: RunAllRequest,
) -> Result<BatchReport, String> {
    sdk_bridge::run_all(&app, request).await
}

/// 终止正在运行的体检。杀掉侧车进程，run_all_healthchecks 会随之返回错误。
#[tauri::command]
fn cancel_healthcheck() -> Result<(), String> {
    sdk_bridge::cancel_batch()
}

#[tauri::command]
fn open_pdf(path: String) -> Result<(), String> {
    let report = Path::new(&path);
    if !report.is_file()
        || report
            .extension()
            .and_then(|value| value.to_str())
            .map(|value| !value.eq_ignore_ascii_case("pdf"))
            .unwrap_or(true)
    {
        return Err("PDF 报告不存在".to_string());
    }
    #[cfg(target_os = "windows")]
    std::process::Command::new("explorer")
        .arg(report)
        .spawn()
        .map_err(|error| format!("无法打开 PDF: {error}"))?;
    Ok(())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            list_models,
            run_healthcheck,
            run_all_healthchecks,
            cancel_healthcheck,
            open_pdf
        ])
        .run(tauri::generate_context!())
        .expect("error while running supply check desktop app");
}
