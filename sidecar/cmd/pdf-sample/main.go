package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"supply-check-sdk/batch"
	"supply-check-sdk/protocol"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: pdf-sample <output.pdf>")
		os.Exit(2)
	}
	outputPath, err := filepath.Abs(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	started := time.Date(2026, time.August, 29, 14, 0, 0, 0, time.Local)
	finished := started.Add(7*time.Minute + 31*time.Second)
	reports := []protocol.ModelReport{
		sampleModel("gpt-5.2", 96, "OK", started, finished),
		sampleModel("claude-sonnet-4-5", 82, "SUSPICIOUS", started.Add(15*time.Second), finished.Add(24*time.Second)),
	}
	report := &protocol.BatchReport{
		ID:                "desktop-pdf-sample",
		Provider:          "openai",
		ProviderLabel:     "OpenAI",
		BaseURL:           "https://api.openai.com/v1",
		StartedAt:         started.Format(time.RFC3339),
		FinishedAt:        finished.Format(time.RFC3339),
		DurationMs:        finished.Sub(started).Milliseconds(),
		TotalModels:       len(reports),
		CompletedModels:   len(reports),
		EstimatedRequests: len(reports) * batch.RequestsPerModel,
		CompletedRequests: len(reports) * batch.RequestsPerModel,
		TrustScore:        89,
		Verdict:           "SUSPICIOUS",
		Models:            reports,
	}
	// 第二个参数可选，用来检查其他语言的排版：go run ./cmd/pdf-sample out.pdf en
	lang := ""
	if len(os.Args) > 2 {
		lang = os.Args[2]
	}
	if err := batch.WritePDF(report, reports, outputPath, lang, started, finished); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(outputPath)
}

func sampleModel(name string, score int, verdict string, started, finished time.Time) protocol.ModelReport {
	definitions := []struct {
		key, kind, title string
	}{
		{"p1_token_count", "token_count", "Token 计数一致性"},
		{"p2_length", "length", "输出长度遵循度"},
		{"p3_identity", "identity", "模型身份真实性"},
		{"p4_golden", "golden", "动态金标题"},
		{"p5_latency", "latency", "首字与总延迟"},
		{"p6_cost_anchor", "cost_anchor", "成本锚点"},
		{"p7a_cache_accounting", "cache_accounting", "缓存记账"},
		{"p7b_freshness_integrity", "freshness_integrity", "缓存新鲜度"},
		{"p7c_provider_cache_control", "provider_cache_control", "Provider Cache-Control"},
		{"p8_self_report", "self_report", "来源自述"},
		{"p10_protocol_contract", "protocol_contract", "协议契约"},
		{"p11_stream_integrity", "stream_integrity", "流式完整性"},
		{"p12_usage_reconciliation", "usage_reconciliation", "用量对账"},
		{"p13_cancellation_contract", "cancellation_contract", "取消契约"},
		{"p14_tool_schema_fidelity", "tool_schema_fidelity", "工具 Schema 忠实度"},
		{"p15_rate_limit_contract", "rate_limit_contract", "限流契约"},
		{"p16_prompt_leakage", "prompt_leakage", "提示词泄露"},
		{"p17_instruction_policy", "instruction_policy", "指令策略"},
		{"p18_tool_substitution", "tool_substitution", "工具替换"},
		{"p19_context_integrity", "context_integrity", "上下文完整性"},
		{"p20_channel_purity", "channel_purity", "渠道纯度"},
		{"p21_cache_rate", "cache_rate", "长上下文缓存率"},
	}
	results := make([]protocol.ProbeResult, 0, len(definitions))
	for index, definition := range definitions {
		status := "pass"
		evidence := map[string]any{
			"model":        name,
			"sample_index": index + 1,
			"summary":      "已按原测试流程完成，协议证据与用量字段已记录。",
		}
		if definition.kind == "cost_anchor" {
			status = "skip"
			evidence["reason"] = "纯 API Base/Key 模式没有网关客户价格账本"
		}
		if verdict == "SUSPICIOUS" && definition.kind == "channel_purity" {
			status = "warn"
			evidence["anomaly"] = "10 次采样中存在 1 次身份措辞偏移"
		}
		results = append(results, protocol.ProbeResult{
			ProbeKey: definition.key,
			Kind:     definition.kind,
			Status:   status,
			Evidence: evidence,
		})
	}
	return protocol.ModelReport{
		ID:               1000 + len(name),
		Model:            name,
		DurationMs:       finished.Sub(started).Milliseconds(),
		RequestCount:     batch.RequestsPerModel,
		PromptTokens:     48210,
		CompletionTokens: 1938,
		TotalTokens:      50148,
		TrustScore:       score,
		Verdict:          verdict,
		Results:          results,
	}
}
