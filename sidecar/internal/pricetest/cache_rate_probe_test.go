package pricetest

import (
	"testing"

	"github.com/stretchr/testify/require"
	"supply-check-sdk/internal/model"
)

func TestProbeCacheRateMeasuresRotatedWarmPrompts(t *testing.T) {
	samples := []CacheRateSample{
		{PromptID: "A", Role: "cold", PromptTokens: 1000, TelemetryReported: true, MarkerMatch: true, FirstResponseMs: 500},
		{PromptID: "B", Role: "cold", PromptTokens: 1000, TelemetryReported: true, MarkerMatch: true, FirstResponseMs: 600},
		{PromptID: "C", Role: "cold", PromptTokens: 1000, TelemetryReported: true, MarkerMatch: true, FirstResponseMs: 700},
		{PromptID: "A", Role: "warm", PromptTokens: 1000, CachedTokens: 800, TelemetryReported: true, MarkerMatch: true, FirstResponseMs: 200},
		{PromptID: "B", Role: "warm", PromptTokens: 1000, CachedTokens: 900, TelemetryReported: true, MarkerMatch: true, FirstResponseMs: 180},
		{PromptID: "C", Role: "warm", PromptTokens: 1000, CachedTokens: 700, TelemetryReported: true, MarkerMatch: true, FirstResponseMs: 220},
	}
	result := ProbeCacheRate(samples, 3, 1, 16000)
	require.Equal(t, model.ProbeStatusPass, result.Status)
	require.Equal(t, 80.0, result.Evidence["cache_rate_pct"])
	require.Equal(t, 100.0, result.Evidence["prompt_hit_rate_pct"])
	require.Equal(t, 2400, result.Evidence["warm_cached_tokens"])
	require.EqualValues(t, 200, result.LatencyMs)
}

func TestProbeCacheRateUsesAnthropicTotalInputDenominator(t *testing.T) {
	samples := []CacheRateSample{
		{PromptID: "A", Role: "cold", PromptTokens: 1, CacheCreationTokens: 4000, CacheTokensSeparate: true, TelemetryReported: true, MarkerMatch: true},
		{PromptID: "B", Role: "cold", PromptTokens: 1, CacheCreationTokens: 4200, CacheTokensSeparate: true, TelemetryReported: true, MarkerMatch: true},
		{PromptID: "A", Role: "warm", PromptTokens: 1, CachedTokens: 4000, CacheTokensSeparate: true, TelemetryReported: true, MarkerMatch: true},
		{PromptID: "B", Role: "warm", PromptTokens: 1, CachedTokens: 4200, CacheTokensSeparate: true, TelemetryReported: true, MarkerMatch: true},
	}

	result := ProbeCacheRate(samples, 2, 1, 16000)
	require.Equal(t, model.ProbeStatusPass, result.Status)
	require.Equal(t, "cache_rate_measured", result.Evidence["reason_code"])
	require.Equal(t, 99.98, result.Evidence["cache_rate_pct"])
	require.Equal(t, 8202, result.Evidence["warm_total_input_tokens"])
	require.Equal(t, 8202, result.Evidence["cold_total_input_tokens"])
}

func TestProbeCacheRateSeparatesUnsupportedZeroAndMarkerMismatch(t *testing.T) {
	unsupported := make([]CacheRateSample, 0, 4)
	for _, role := range []string{"cold", "cold", "warm", "warm"} {
		unsupported = append(unsupported, CacheRateSample{Role: role, PromptTokens: 100, MarkerMatch: true})
	}
	result := ProbeCacheRate(unsupported, 2, 1, 8000)
	require.Equal(t, model.ProbeStatusSkip, result.Status)

	for index := range unsupported {
		unsupported[index].TelemetryReported = true
	}
	result = ProbeCacheRate(unsupported, 2, 1, 8000)
	require.Equal(t, model.ProbeStatusWarn, result.Status)
	require.Equal(t, "cache_rate_zero", result.Evidence["reason_code"])

	unsupported[3].MarkerMatch = false
	result = ProbeCacheRate(unsupported, 2, 1, 8000)
	require.Equal(t, model.ProbeStatusWarn, result.Status)
	require.Equal(t, "expected_marker_missing", result.Evidence["reason_code"])
}

func TestQuality_CacheRateMarkerMismatchDoesNotClaimReplay(t *testing.T) {
	samples := []CacheRateSample{
		{PromptID: "A", Role: "cold", PromptTokens: 1000, TelemetryReported: true, MarkerMatch: true},
		{PromptID: "B", Role: "cold", PromptTokens: 1000, TelemetryReported: true, MarkerMatch: true},
		{PromptID: "A", Role: "warm", PromptTokens: 1000, CachedTokens: 800, TelemetryReported: true, MarkerMatch: true},
		{PromptID: "B", Role: "warm", PromptTokens: 1000, CachedTokens: 800, TelemetryReported: true, MarkerMatch: false},
	}

	result := ProbeCacheRate(samples, 2, 1, 8000)
	require.Equal(t, model.ProbeStatusWarn, result.Status)
	require.Equal(t, "expected_marker_missing", result.Evidence["reason_code"])
	require.Equal(t, 1, result.Evidence["marker_mismatch_samples"])
	require.Equal(t, []string{"B"}, result.Evidence["marker_mismatch_prompts"])
	require.NotEqual(t, "rotated_prompt_replay", result.Evidence["reason_code"])
}

func TestQuality_CacheRateRequiresObservedAlternateMarkerForReplay(t *testing.T) {
	samples := []CacheRateSample{
		{PromptID: "A", Role: "cold", PromptTokens: 1000, TelemetryReported: true, MarkerMatch: true},
		{PromptID: "B", Role: "cold", PromptTokens: 1000, TelemetryReported: true, MarkerMatch: true},
		{PromptID: "A", Role: "warm", PromptTokens: 1000, CachedTokens: 800, TelemetryReported: true, MarkerMatch: false, ObservedPromptID: "B"},
		{PromptID: "B", Role: "warm", PromptTokens: 1000, CachedTokens: 800, TelemetryReported: true, MarkerMatch: true},
	}

	result := ProbeCacheRate(samples, 2, 1, 8000)
	require.Equal(t, model.ProbeStatusFail, result.Status)
	require.Equal(t, "rotated_prompt_replay", result.Evidence["reason_code"])
	require.Equal(t, "A", result.Evidence["expected_prompt"])
	require.Equal(t, "B", result.Evidence["observed_prompt"])
}
