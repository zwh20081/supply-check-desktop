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
