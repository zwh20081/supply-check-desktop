package pricetest

import (
	"strings"
	"time"

	"supply-check-sdk/internal/model"
)

const (
	CacheCapabilityUnsupported  = "unsupported"
	CacheCapabilityReportedMiss = "reported_no_hit"
	CacheCapabilityHit          = "hit"
)

// CachePhaseObservation is the provider-neutral evidence emitted by one
// controlled P7-v2 request. TelemetryReported distinguishes an explicit zero
// from a provider/adaptor that exposes no cache-accounting field at all.
type CachePhaseObservation struct {
	Phase               string
	PromptTokens        int
	CachedTokens        int
	CacheCreationTokens int
	// CacheTokensSeparate is true for providers such as Anthropic whose
	// input_tokens excludes cache read and cache creation tokens.
	CacheTokensSeparate bool
	TelemetryReported   bool
	FirstResponseMs     int64
	Content             string
	ObservedAt          time.Time
}

type ProviderCacheControlSample struct {
	Phase    string
	Expected []string
	Applied  []string
}

type ProviderCacheControlObs struct {
	Provider    string
	Supported   []string
	Unsupported []string
	Samples     []ProviderCacheControlSample
}

// ProbeProviderCacheControl reports which provider-native controls were safely
// applied to the shared P7 A/B/C sequence. It judges adapter/control fidelity,
// not whether a cache hit occurred (P7a owns that evidence). Unsupported
// providers are explicitly SKIP and never receive fabricated cache resource IDs.
func ProbeProviderCacheControl(obs ProviderCacheControlObs) model.ProbeResult {
	result := model.ProbeResult{
		ProbeKey: "p7c_provider_cache_control", Kind: model.ProbeKindProviderCacheControl,
		Evidence: map[string]any{
			"provider": obs.Provider, "supported_features": obs.Supported,
			"unsupported_features": obs.Unsupported,
		},
	}
	if len(obs.Supported) == 0 {
		result.Status = model.ProbeStatusSkip
		result.Evidence["reason_code"] = "provider_control_unsupported"
		return result
	}
	samples := make([]map[string]any, 0, len(obs.Samples))
	for _, sample := range obs.Samples {
		samples = append(samples, map[string]any{
			"phase": sample.Phase, "expected": sample.Expected, "applied": sample.Applied,
		})
		for _, expected := range sample.Expected {
			if !containsString(sample.Applied, expected) {
				result.Status = model.ProbeStatusError
				result.Evidence["reason_code"] = "provider_control_not_applied"
				result.Evidence["phase"] = sample.Phase
				result.Evidence["samples"] = samples
				return result
			}
		}
	}
	result.Evidence["samples"] = samples
	result.Status = model.ProbeStatusPass
	if len(obs.Unsupported) == 0 {
		result.Evidence["capability"] = "full"
	} else {
		result.Evidence["capability"] = "partial"
	}
	result.Evidence["reason_code"] = "provider_control_applied"
	return result
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

// ProbeCacheAccounting evaluates only the three controlled A/B/C requests.
// It deliberately does not consume incidental cached-token values from the
// other health probes, so an ordinary successful request is never promoted to
// cache-hit evidence.
func ProbeCacheAccounting(samples []CachePhaseObservation) model.ProbeResult {
	result := model.ProbeResult{
		ProbeKey: "p7a_cache_accounting",
		Kind:     model.ProbeKindCacheAccounting,
		Evidence: map[string]any{"samples": cacheSampleEvidence(samples)},
	}
	if len(samples) != 3 {
		result.Status = model.ProbeStatusError
		result.Evidence["capability"] = CacheCapabilityUnsupported
		result.Evidence["reason_code"] = "controlled_evidence_incomplete"
		return result
	}

	reported := 0
	hit := false
	for _, sample := range samples {
		if sample.PromptTokens < 0 || sample.CachedTokens < 0 || sample.CacheCreationTokens < 0 {
			result.Status = model.ProbeStatusFail
			result.Evidence["capability"] = CacheCapabilityUnsupported
			result.Evidence["reason_code"] = "negative_token_count"
			result.Evidence["anomaly_phase"] = sample.Phase
			return result
		}
		if (sample.CachedTokens > 0 || sample.CacheCreationTokens > 0) && !sample.TelemetryReported {
			result.Status = model.ProbeStatusFail
			result.Evidence["capability"] = CacheCapabilityUnsupported
			result.Evidence["reason_code"] = "unattributed_cached_tokens"
			result.Evidence["anomaly_phase"] = sample.Phase
			return result
		}
		if sample.TelemetryReported {
			reported++
			if !sample.CacheTokensSeparate && sample.CachedTokens > sample.PromptTokens {
				result.Status = model.ProbeStatusFail
				result.Evidence["capability"] = CacheCapabilityHit
				result.Evidence["reason_code"] = "cached_exceeds_prompt"
				result.Evidence["anomaly_phase"] = sample.Phase
				return result
			}
			if sample.CachedTokens > 0 {
				hit = true
			}
		}
	}

	result.Evidence["reported_samples"] = reported
	switch {
	case reported == 0:
		result.Status = model.ProbeStatusSkip
		result.Evidence["capability"] = CacheCapabilityUnsupported
		result.Evidence["reason_code"] = "telemetry_unsupported"
	case hit:
		result.Status = model.ProbeStatusPass
		result.Evidence["capability"] = CacheCapabilityHit
		result.Evidence["reason_code"] = "cache_hit"
	default:
		result.Status = model.ProbeStatusPass
		result.Evidence["capability"] = CacheCapabilityReportedMiss
		result.Evidence["reason_code"] = "reported_no_hit"
	}
	return result
}

// ProbeFreshnessIntegrity verifies the controlled response markers. A and B
// use the same challenge, while C changes the nonce and expected marker but
// retains the stable prefix. Returning A's marker for C is direct replay
// evidence. Unparseable/decorated controls are inconclusive rather than a false
// freshness accusation.
func ProbeFreshnessIntegrity(a, b, c CachePhaseObservation, markerA, markerC string) model.ProbeResult {
	ageMs := c.ObservedAt.Sub(a.ObservedAt).Milliseconds()
	if ageMs < 0 {
		ageMs = 0
	}
	result := model.ProbeResult{
		ProbeKey: "p7b_freshness_integrity",
		Kind:     model.ProbeKindFreshnessIntegrity,
		Evidence: map[string]any{
			"a_b_same_challenge":  true,
			"c_changed_challenge": true,
			"freshness_age_ms":    ageMs,
		},
	}
	markerA = strings.TrimSpace(markerA)
	markerC = strings.TrimSpace(markerC)
	if markerA == "" || markerC == "" || markerA == markerC {
		result.Status = model.ProbeStatusError
		result.Evidence["reason_code"] = "invalid_markers"
		return result
	}
	aMatches := containsMarker(a.Content, markerA)
	bMatches := containsMarker(b.Content, markerA)
	cReplayedA := containsMarker(c.Content, markerA)
	cMatches := containsMarker(c.Content, markerC)
	result.Evidence["a_marker_match"] = aMatches
	result.Evidence["b_marker_match"] = bMatches
	result.Evidence["c_replayed_a_marker"] = cReplayedA
	result.Evidence["c_marker_match"] = cMatches
	if !aMatches || !bMatches {
		result.Status = model.ProbeStatusError
		result.Evidence["reason_code"] = "control_marker_missing"
		return result
	}
	if cReplayedA {
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "stale_replay"
		result.Evidence["freshness_violation"] = true
		return result
	}
	if !cMatches {
		result.Status = model.ProbeStatusError
		result.Evidence["reason_code"] = "changed_marker_missing"
		return result
	}
	result.Status = model.ProbeStatusPass
	result.Evidence["freshness_violation"] = false
	result.Evidence["reason_code"] = "fresh_marker"
	return result
}

func containsMarker(content, marker string) bool {
	return strings.Contains(strings.ToUpper(content), strings.ToUpper(marker))
}

func cacheSampleEvidence(samples []CachePhaseObservation) []map[string]any {
	evidence := make([]map[string]any, 0, len(samples))
	for _, sample := range samples {
		evidence = append(evidence, map[string]any{
			"phase":                 sample.Phase,
			"prompt_tokens":         sample.PromptTokens,
			"cached_tokens":         sample.CachedTokens,
			"cache_creation_tokens": sample.CacheCreationTokens,
			"total_input_tokens": cacheTotalInputTokens(
				sample.PromptTokens, sample.CachedTokens, sample.CacheCreationTokens, sample.CacheTokensSeparate,
			),
			"cache_tokens_separate": sample.CacheTokensSeparate,
			"telemetry_reported":    sample.TelemetryReported,
			"first_response_ms":     sample.FirstResponseMs,
			"observed_at_ms":        sample.ObservedAt.UnixMilli(),
		})
	}
	return evidence
}

func cacheTotalInputTokens(promptTokens, cachedTokens, cacheCreationTokens int, cacheTokensSeparate bool) int {
	if cacheTokensSeparate {
		return promptTokens + cachedTokens + cacheCreationTokens
	}
	return promptTokens
}
