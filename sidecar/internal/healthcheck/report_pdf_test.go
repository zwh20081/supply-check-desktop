package healthcheck

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"supply-check-sdk/internal/i18n"
	"supply-check-sdk/internal/model"
)

func TestBuildProbeRunPDFIncludesLocalizedReportAndEvidence(t *testing.T) {
	require.NoError(t, i18n.Init())
	run := &model.ProbeRun{
		Id: 17, ChannelId: 9, ChannelName: "主渠道", Model: "gpt-4o",
		TrustScore: 72, Verdict: model.ProbeVerdictSuspicious, UpstreamCost: 123,
		CreatedAt: 1_725_000_000_000,
		Results: model.ProbeResultList{
			{
				ProbeKey: "p1_token_count", Kind: model.ProbeKindTokenCount,
				Status: model.ProbeStatusWarn, LatencyMs: 245,
				Evidence: map[string]any{"expected_tokens": 42, "observed_tokens": 51, "note": "证据可追溯"},
			},
			{
				ProbeKey: "p21_cache_rate", Kind: model.ProbeKindCacheRate, Status: model.ProbeStatusPass,
				Evidence: map[string]any{
					"cache_rate_pct": 80.0, "hit_prompts": 3, "prompt_variants": 3,
					"warm_cached_tokens": 2400, "warm_prompt_tokens": 3000, "reported_warm_samples": 3,
					"samples": []map[string]any{
						{"prompt_id": "A", "role": "cold", "context_chars": 16000, "prompt_tokens": 1000, "cached_tokens": 0, "cache_rate_pct": 0, "first_response_ms": 500},
						{"prompt_id": "A", "role": "warm", "context_chars": 16000, "prompt_tokens": 1000, "cached_tokens": 800, "cache_rate_pct": 80, "first_response_ms": 180},
					},
				},
			},
		},
	}

	report, err := BuildProbeRunPDF(run, i18n.LangZhCN)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(report, []byte("%PDF-1.7")))
	require.Contains(t, string(report), "/Type /Catalog")
	require.Contains(t, string(report), "/BaseFont /STSong-Light")
	require.Contains(t, string(report), pdfHexText("货源体检报告"))
	require.Contains(t, string(report), pdfHexText("图表分析"))
	require.Contains(t, string(report), pdfHexText("探针状态分布"))
	require.Contains(t, string(report), pdfHexText("详细数据"))
	require.Contains(t, string(report), pdfHexText("探针编号"))
	require.Contains(t, string(report), pdfHexText("证据可追溯"))
	require.Contains(t, string(report), pdfHexText("总缓存率（长上下文）"))
	require.Contains(t, string(report), "(80.00%)")
	require.Contains(t, string(report), "/F2 31.00 Tf", "overall cache rate should use the large display size")
	require.Contains(t, string(report), pdfHexText("缓存"))
}

func TestCacheRateReportUsesTotalInputTokensWhenCacheIsSeparate(t *testing.T) {
	metric := cacheRateFromResults(model.ProbeResultList{{
		Kind: model.ProbeKindCacheRate,
		Evidence: map[string]any{
			"cache_rate_pct": 99.98, "warm_cached_tokens": 8000,
			"warm_prompt_tokens": 2, "warm_total_input_tokens": 8002,
			"reported_warm_samples": 2,
			"samples": []map[string]any{
				{
					"prompt_id": "A", "role": "warm", "round": 1, "cached_tokens": 4000,
					"total_input_tokens": 4001, "cache_rate_pct": 99.98,
				},
				{
					"prompt_id": "A", "role": "warm", "round": 2, "cached_tokens": 2000,
					"total_input_tokens": 4001, "cache_rate_pct": 49.99,
				},
			},
		},
	}})
	require.Equal(t, 8002, metric.promptTokens)
	require.Equal(t, 8000, metric.cachedTokens)
	require.Len(t, metric.items, 1)
	require.Equal(t, "A", metric.items[0].label)
	require.Equal(t, 6000, metric.items[0].cachedTokens)
	require.Equal(t, 8002, metric.items[0].totalTokens)
	require.InDelta(t, 74.98, metric.items[0].rate, 0.01)
}

func TestBuildHealthCheckJobPDFAddsTargetAndRunPages(t *testing.T) {
	require.NoError(t, i18n.Init())
	run := &model.ProbeRun{
		Id: 3, ChannelId: 2, ChannelName: "source", Model: "claude-sonnet",
		TrustScore: 96, Verdict: model.ProbeVerdictOK,
		Results: model.ProbeResultList{
			{Kind: model.ProbeKindIdentity, Status: model.ProbeStatusPass},
			{
				ProbeKey: "p21_cache_rate", Kind: model.ProbeKindCacheRate, Status: model.ProbeStatusPass,
				Evidence: map[string]any{
					"cache_rate_pct": 99.98, "hit_prompts": 2, "prompt_variants": 2,
					"warm_cached_tokens": 8000, "warm_total_input_tokens": 8002,
					"reported_warm_samples": 2,
					"samples": []map[string]any{
						{"prompt_id": "A", "role": "warm", "cached_tokens": 4000, "total_input_tokens": 4001, "cache_rate_pct": 99.98},
						{"prompt_id": "B", "role": "warm", "cached_tokens": 4000, "total_input_tokens": 4001, "cache_rate_pct": 99.98},
					},
				},
			},
		},
	}
	report, err := BuildHealthCheckJobPDF(HealthCheckReport{
		Job: &model.HealthCheckJob{
			ID: "job-report", Status: model.HealthCheckJobStatusSucceeded,
			TotalTasks: 1, CompletedTasks: 1, Verdict: model.ProbeVerdictOK, TrustScore: 96,
			ProfileVersion: "v1", Endpoint: model.HealthCheckChatEndpoint,
		},
		Tasks: []*model.HealthCheckTask{{
			ID: 8, ProbeRunID: 3, ChannelID: 2, ChannelName: "source", Model: "claude-sonnet",
			Status: model.HealthCheckJobStatusSucceeded, Verdict: model.ProbeVerdictOK, TrustScore: 96,
		}},
		Runs: map[int]*model.ProbeRun{3: run},
	}, i18n.LangEn)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(report, []byte("%PDF-1.7")))
	require.GreaterOrEqual(t, strings.Count(string(report), "/Type /Page "), 2)
	require.Contains(t, string(report), "(claude-sonnet)")
	require.Contains(t, string(report), "(Target verdict distribution)")
	require.Contains(t, string(report), "(Target trust scores)")
	require.Contains(t, string(report), "(Cache rate by target)")
	require.Contains(t, string(report), "(99.98%)")
	require.NotContains(t, string(report), "(Check profile)")
	require.NotContains(t, string(report), "(Check endpoint)")
}

func TestBuildProbeRunPDFDoesNotPromoteRemovedLegacyCacheResults(t *testing.T) {
	require.NoError(t, i18n.Init())
	run := &model.ProbeRun{
		Id: 19, ChannelId: 49, ChannelName: "ccmax", Model: "claude-sonnet-5",
		TrustScore: 78, Verdict: model.ProbeVerdictSuspicious,
		Results: model.ProbeResultList{{
			ProbeKey: "p7_cache_hit", Kind: "cache_hit", Status: model.ProbeStatusPass,
			Evidence: map[string]any{"cached_tokens": 0},
		}, {
			ProbeKey: "p7a_cache_accounting", Kind: model.ProbeKindCacheAccounting,
			Status: model.ProbeStatusPass,
			Evidence: map[string]any{"samples": []map[string]any{
				{"phase": "A", "prompt_tokens": 1, "cached_tokens": 0, "telemetry_reported": true},
				{"phase": "B", "prompt_tokens": 1, "cached_tokens": 0, "telemetry_reported": true},
				{"phase": "C", "prompt_tokens": 4469, "cached_tokens": 0, "telemetry_reported": true},
			}},
		}},
	}

	report, err := BuildProbeRunPDF(run, i18n.LangZhCN)
	require.NoError(t, err)
	require.NotContains(t, string(report), pdfHexText("长上下文缓存率"))
	require.NotContains(t, string(report), "(cache_hit)")
	require.NotContains(t, string(report), "(p7_cache_hit)")
}

func TestQuality_UnavailableCostRendersNAInsteadOfZero(t *testing.T) {
	require.NoError(t, i18n.Init())
	require.Equal(t, "N/A", reportCostDisplay(0))
	require.Equal(t, "37", reportCostDisplay(37))

	report, err := BuildProbeRunPDF(&model.ProbeRun{
		Id: 41, ChannelId: 7, ChannelName: "source", Model: "claude-sonnet",
		TrustScore: 90, Verdict: model.ProbeVerdictOK, UpstreamCost: 0,
	}, i18n.LangEn)
	require.NoError(t, err)
	require.Contains(t, string(report), "Upstream cost: N/A")
	require.NotContains(t, string(report), "Upstream cost: 0")
}

func TestQuality_BatchCacheSummarySeparatesValidRatesFromTelemetry(t *testing.T) {
	require.NoError(t, i18n.Init())
	valid := model.ProbeResultList{{
		ProbeKey: "p21_cache_rate", Kind: model.ProbeKindCacheRate, Status: model.ProbeStatusPass,
		Evidence: map[string]any{
			"reported_warm_samples": 1, "warm_total_input_tokens": 100,
			"warm_cached_tokens": 80, "cache_rate_pct": 80,
		},
	}}
	telemetryOnly := model.ProbeResultList{{
		ProbeKey: "p7a_cache_accounting", Kind: model.ProbeKindCacheAccounting, Status: model.ProbeStatusPass,
		Evidence: map[string]any{"samples": []map[string]any{{
			"phase": "A", "cached_tokens": 0, "telemetry_reported": true,
		}}},
	}}
	tasks := []*model.HealthCheckTask{
		{ProbeRunID: 1, Model: "valid-rate"},
		{ProbeRunID: 2, Model: "telemetry-only"},
	}
	metric := batchCacheRateFromRuns(tasks, map[int]*model.ProbeRun{
		1: {Results: valid},
		2: {Results: telemetryOnly},
	})

	require.Equal(t, 1, metric.validTargets)
	require.Equal(t, 2, metric.telemetryTargets)
	require.Equal(t, 80, metric.cachedTokens)
	require.Equal(t, 100, metric.promptTokens)
	require.Len(t, metric.targets, 1)
	summary := trReport(i18n.LangEn, i18n.MsgHealthCheckReportCacheRateBatchSummary, map[string]any{
		"Covered": metric.validTargets, "Telemetry": metric.telemetryTargets, "Targets": len(tasks),
		"Cached": metric.cachedTokens, "Tokens": metric.promptTokens,
	})
	require.Contains(t, summary, "computed for 1/2 targets")
	require.Contains(t, summary, "telemetry present for 2/2")
}

func TestQuality_CacheSummaryTemplatesRenderBothCountsInEveryLocale(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, lang := range []string{
		i18n.LangZhCN, i18n.LangZhTW, i18n.LangEn, i18n.LangFr,
		i18n.LangRu, i18n.LangJa, i18n.LangVi,
	} {
		summary := trReport(lang, i18n.MsgHealthCheckReportCacheRateBatchSummary, map[string]any{
			"Covered": 1, "Telemetry": 2, "Targets": 2, "Cached": 80, "Tokens": 100,
		})
		require.NotEqual(t, i18n.MsgHealthCheckReportCacheRateBatchSummary, summary, lang)
		require.NotContains(t, summary, "{{", lang)
		require.Contains(t, summary, "1/2", lang)
		require.Contains(t, summary, "2/2", lang)
	}
}

func TestQuality_CacheRateRequiresTokenDenominator(t *testing.T) {
	metric := cacheRateFromResults(model.ProbeResultList{{
		ProbeKey: "p21_cache_rate", Kind: model.ProbeKindCacheRate,
		Evidence: map[string]any{
			"reported_warm_samples": 2,
			"warm_cached_tokens":    20,
			"cache_rate_pct":        100,
		},
	}})
	require.True(t, metric.telemetry)
	require.False(t, metric.available, "telemetry alone must not be presented as a computed rate")
}

func TestQuality_CacheTelemetryRequiresAReportedSample(t *testing.T) {
	results := model.ProbeResultList{{
		ProbeKey: "p21_cache_rate", Kind: model.ProbeKindCacheRate,
		Evidence: map[string]any{
			"reported_warm_samples": 0,
			"warm_cached_tokens":    0,
			"samples": []map[string]any{{
				"cached_tokens": 0, "telemetry_reported": false,
			}},
		},
	}}
	require.False(t, hasCacheTelemetry(results), "placeholder zero fields are not cache telemetry")
}

func TestQuality_NestedEvidenceUsesReadableStructuredFormatting(t *testing.T) {
	value := evidenceValue(map[string]any{
		"clusters": []map[string]any{{
			"center_ms": 123,
			"members":   []string{"sample-a", "sample-b"},
		}},
		"counts": map[string]int{"pass": 2, "warn": 1},
	})

	require.Contains(t, value, "{\n")
	require.Contains(t, value, "\"clusters\": [")
	require.Contains(t, value, "\"center_ms\": 123")
	require.Contains(t, value, "\"counts\": {")
	require.NotContains(t, value, "map[")
	require.Contains(t, wrapPDFText(value, 320, 8.5), "  \"clusters\": [", "explicit JSON indentation should survive PDF wrapping")
}

func TestQuality_ProbeIdentifiersWrapAtSemanticBoundaries(t *testing.T) {
	lines := wrapPDFText("p13_cancellation_contract", 65, 8.7)
	require.Equal(t, []string{"p13_", "cancellation_", "contract"}, lines)
	require.Equal(t, "p13_cancellation_contract", strings.Join(lines, ""))
	for _, line := range lines {
		require.LessOrEqual(t, pdfTextWidth(line, 8.7), 65.0)
	}
}

func TestQuality_CacheCalloutSeparatesTitleAndSummaryBands(t *testing.T) {
	require.NoError(t, i18n.Init())
	report := newPDFReport(i18n.LangEn)
	report.y = 700
	report.drawCacheRateCallout(77.93, "8/8 targets have valid cache-rate evidence", nil)
	content := report.page.content.String()

	require.Contains(t, content, `1 0 0 1 68.00 680.00 Tm (TOTAL CACHE RATE \(LONG CONTEXT\)) Tj`)
	require.Contains(t, content, "1 0 0 1 225.00 657.00 Tm (8/8 targets have valid cache-rate evidence) Tj")
	require.NotContains(t, content, "1 0 0 1 225.00 680.00 Tm", "summary must not share the title baseline")
}

func TestQuality_SectionAndEvidenceHeadingsReserveClearSpace(t *testing.T) {
	report := newPDFReport(i18n.LangEn)
	report.y = reportPDFBottom + 80
	report.addSection("Evidence details")
	require.Len(t, report.pages, 2, "a section must move with its first content block")

	startY := report.y
	report.addEvidenceHeading("Model identity validation", "Failed")
	require.GreaterOrEqual(t, startY-report.y, 39.0, "evidence heading needs clearance above and below its text")
}

func TestQuality_EvidenceHeadingKeepsCompactBlockOnOnePage(t *testing.T) {
	report := newPDFReport(i18n.LangEn)
	report.y = reportPDFBottom + 95
	report.addEvidenceHeading("Model identity validation", "Pass")

	require.Len(t, report.pages, 2, "heading should move instead of orphaning a compact evidence block")
	require.Greater(t, report.y, reportPDFBottom+76, "new page must retain room for the compact block")
}

func TestQuality_BatchTrustScoreLabelsLeadWithModel(t *testing.T) {
	task := &model.HealthCheckTask{
		ChannelID: 17, ChannelName: "Claude Bedrock production channel with a long display name",
		Model: "claude-haiku-4-5-20251001-v1:0", Verdict: model.ProbeVerdictOK, TrustScore: 96,
	}
	label := batchTrustScoreLabel(task)
	visible := firstOrEmpty(wrapPDFText(label, 190-8, 8.2))

	require.Equal(t, "claude-haiku-4-5-20251001-v1:0 / #17", label)
	require.Contains(t, visible, "claude-haiku", "the differentiating model identifier must be visible")
	require.Contains(t, visible, "#17", "the compact label must retain channel context")
	require.NotContains(t, visible, task.ChannelName, "the full channel name belongs in the target table, not the compact chart label")
}

func TestQuality_AbsoluteTTFTWithoutBaselineIsNotReportedAsMissing(t *testing.T) {
	require.NoError(t, i18n.Init())
	result := model.ProbeResult{
		ProbeKey: "p5_latency", Kind: model.ProbeKindLatency, Status: model.ProbeStatusSkip,
		Evidence: map[string]any{
			"ttft_ms": 1968, "first_response_ms": 2400,
			"baseline_first_ms": 0, "baseline_tokens_per_sec": 0,
			"reason": "no baseline yet",
		},
	}

	latencyMs, basis, noBaseline := reportLatencySample(result)
	require.Equal(t, int64(1968), latencyMs)
	require.Equal(t, "TTFT", basis)
	require.True(t, noBaseline)

	report := newPDFReport(i18n.LangEn)
	report.addProbeCharts(model.ProbeResultList{result})
	content := report.page.content.String()
	require.NotContains(t, content, "No latency data available")
	require.Contains(t, content, "Absolute latency measurements are shown")
	require.Contains(t, content, "(1968ms)", "the absolute TTFT value should remain visible in the chart")
}

func TestQuality_MultipageTablesRepeatHeadersAndAvoidAnOrphanFinalRow(t *testing.T) {
	report := newPDFReport(i18n.LangEn)
	// Exactly enough room for the header and three rows. The final two rows must
	// move together to a continuation page with a repeated header.
	report.y = reportPDFBottom + 96
	headers := []string{"Prompt", "Value"}
	rows := [][]string{
		{"row-1", "1"}, {"row-2", "2"}, {"row-3", "3"},
		{"row-4", "4"}, {"row-5", "5"},
	}
	report.addTable(headers, rows, []float64{120, 120}, nil)

	require.Len(t, report.pages, 2)
	for _, page := range report.pages {
		require.Contains(t, page.content.String(), "(Prompt)", "every table continuation page needs its header")
	}
	continuation := report.pages[1].content.String()
	require.Contains(t, continuation, "(row-4)")
	require.Contains(t, continuation, "(row-5)")
}

func TestQuality_CacheSamplePromptHeaderDoesNotSplitInsideTheWord(t *testing.T) {
	promptColumnWidth := cacheRateSampleColumnWidths()[0]
	lines := wrapPDFText("Prompt", promptColumnWidth-12, 8.7)
	require.Equal(t, []string{"Prompt"}, lines)
	require.LessOrEqual(t, pdfTextWidth("Prompt", 8.7), promptColumnWidth-12)
}
