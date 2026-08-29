package pricetest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"supply-check-sdk/internal/model"
)

func cacheSamples(reported bool, cached ...int) []CachePhaseObservation {
	base := time.Unix(100, 0)
	result := make([]CachePhaseObservation, 0, 3)
	for index, phase := range []string{"A", "B", "C"} {
		value := 0
		if index < len(cached) {
			value = cached[index]
		}
		result = append(result, CachePhaseObservation{
			Phase: phase, PromptTokens: 100, CachedTokens: value,
			TelemetryReported: reported, ObservedAt: base.Add(time.Duration(index) * time.Second),
		})
	}
	return result
}

func TestProbeCacheAccountingSeparatesUnsupportedMissAndHit(t *testing.T) {
	unsupported := ProbeCacheAccounting(cacheSamples(false, 0, 0, 0))
	require.Equal(t, model.ProbeStatusSkip, unsupported.Status)
	require.Equal(t, CacheCapabilityUnsupported, unsupported.Evidence["capability"])

	miss := ProbeCacheAccounting(cacheSamples(true, 0, 0, 0))
	require.Equal(t, model.ProbeStatusPass, miss.Status)
	require.Equal(t, CacheCapabilityReportedMiss, miss.Evidence["capability"])

	hit := ProbeCacheAccounting(cacheSamples(true, 0, 80, 0))
	require.Equal(t, model.ProbeStatusPass, hit.Status)
	require.Equal(t, CacheCapabilityHit, hit.Evidence["capability"])
}

func TestProbeCacheAccountingRejectsFakeOrImpossibleEvidence(t *testing.T) {
	unattributed := cacheSamples(false, 0, 20, 0)
	result := ProbeCacheAccounting(unattributed)
	require.Equal(t, model.ProbeStatusFail, result.Status)
	require.Equal(t, "unattributed_cached_tokens", result.Evidence["reason_code"])

	unattributedCreation := cacheSamples(false, 0, 0, 0)
	unattributedCreation[0].CacheCreationTokens = 20
	result = ProbeCacheAccounting(unattributedCreation)
	require.Equal(t, model.ProbeStatusFail, result.Status)
	require.Equal(t, "unattributed_cached_tokens", result.Evidence["reason_code"])

	impossible := cacheSamples(true, 0, 101, 0)
	result = ProbeCacheAccounting(impossible)
	require.Equal(t, model.ProbeStatusFail, result.Status)
	require.Equal(t, "cached_exceeds_prompt", result.Evidence["reason_code"])
}

func TestProbeCacheAccountingAcceptsAnthropicSeparateCacheTokens(t *testing.T) {
	samples := cacheSamples(true, 0, 4354, 4218)
	for index := range samples {
		samples[index].PromptTokens = 1
		samples[index].CacheTokensSeparate = true
	}
	samples[0].CacheCreationTokens = 4354

	result := ProbeCacheAccounting(samples)
	require.Equal(t, model.ProbeStatusPass, result.Status)
	require.Equal(t, CacheCapabilityHit, result.Evidence["capability"])
	require.Equal(t, "cache_hit", result.Evidence["reason_code"])

	evidence := result.Evidence["samples"].([]map[string]any)
	require.Equal(t, 4355, evidence[0]["total_input_tokens"])
	require.Equal(t, 4355, evidence[1]["total_input_tokens"])
}

func TestProbeFreshnessIntegrityDetectsReplayAndRecordsAge(t *testing.T) {
	base := time.Unix(200, 0)
	a := CachePhaseObservation{Phase: "A", Content: "CACHE-A", ObservedAt: base}
	b := CachePhaseObservation{Phase: "B", Content: "cache-a", ObservedAt: base.Add(time.Second)}
	c := CachePhaseObservation{Phase: "C", Content: "CACHE-A", ObservedAt: base.Add(3 * time.Second)}

	replayed := ProbeFreshnessIntegrity(a, b, c, "CACHE-A", "CACHE-C")
	require.Equal(t, model.ProbeStatusFail, replayed.Status)
	require.EqualValues(t, 3000, replayed.Evidence["freshness_age_ms"])
	require.Equal(t, true, replayed.Evidence["freshness_violation"])

	c.Content = "CACHE-C"
	fresh := ProbeFreshnessIntegrity(a, b, c, "CACHE-A", "CACHE-C")
	require.Equal(t, model.ProbeStatusPass, fresh.Status)
	require.Equal(t, false, fresh.Evidence["freshness_violation"])
}

func TestProbeFreshnessIntegrityInvalidControlIsUnknownNotFail(t *testing.T) {
	base := time.Unix(300, 0)
	result := ProbeFreshnessIntegrity(
		CachePhaseObservation{Content: "ordinary answer", ObservedAt: base},
		CachePhaseObservation{Content: "ordinary answer", ObservedAt: base},
		CachePhaseObservation{Content: "ordinary answer", ObservedAt: base},
		"CACHE-A", "CACHE-C",
	)
	require.Equal(t, model.ProbeStatusError, result.Status)
}

func TestProbeProviderCacheControlPassPartialSkipAndAdapterError(t *testing.T) {
	openAI := ProbeProviderCacheControl(ProviderCacheControlObs{
		Provider: "openai", Supported: []string{"key_partition"},
		Unsupported: []string{"explicit_on", "explicit_off"},
		Samples: []ProviderCacheControlSample{
			{Phase: "A", Expected: []string{"key_partition"}, Applied: []string{"key_partition"}},
			{Phase: "B", Expected: []string{"key_partition"}, Applied: []string{"key_partition"}},
			{Phase: "C", Expected: []string{"key_partition"}, Applied: []string{"key_partition"}},
		},
	})
	require.Equal(t, model.ProbeStatusPass, openAI.Status)
	require.Equal(t, "partial", openAI.Evidence["capability"])

	unsupported := ProbeProviderCacheControl(ProviderCacheControlObs{
		Provider: "gemini", Unsupported: []string{"explicit_on", "explicit_off", "key_partition"},
	})
	require.Equal(t, model.ProbeStatusSkip, unsupported.Status)
	require.Equal(t, "provider_control_unsupported", unsupported.Evidence["reason_code"])

	notApplied := ProbeProviderCacheControl(ProviderCacheControlObs{
		Provider: "claude", Supported: []string{"explicit_on"},
		Samples: []ProviderCacheControlSample{{Phase: "A", Expected: []string{"explicit_on"}}},
	})
	require.Equal(t, model.ProbeStatusError, notApplied.Status, "an adapter wiring issue must not accuse the provider")
	require.Equal(t, "provider_control_not_applied", notApplied.Evidence["reason_code"])
}
