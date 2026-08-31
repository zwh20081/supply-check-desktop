package pricetest

import (
	"math"

	"supply-check-sdk/internal/model"
)

const (
	CacheRateCapabilityUnsupported = "unsupported"
	CacheRateCapabilityMeasured    = "measured"
)

// CacheRateSample is one cold/warm observation in the rotated long-context
// cache test. Content is deliberately reduced to marker identity before storage
// so reports remain compact and do not persist the generated prompt payload.
type CacheRateSample struct {
	PromptID            string
	Role                string
	Round               int
	ContextChars        int
	PromptTokens        int
	CachedTokens        int
	CacheCreationTokens int
	// CacheTokensSeparate is true when PromptTokens contains only uncached
	// input, as in Anthropic's /v1/messages usage convention.
	CacheTokensSeparate bool
	TelemetryReported   bool
	FirstResponseMs     int64
	UpstreamCostQuota   int
	MarkerMatch         bool
	// ObservedPromptID is set only when the response contains a known marker
	// belonging to a prompt variant. A different ID is direct replay evidence;
	// an empty value with MarkerMatch=false only proves the expected marker was
	// not observed.
	ObservedPromptID string
	Errored          bool
}

// ProbeCacheRate calculates token-weighted cache rate from warm observations
// after rotating through every cold prompt first. The probe is non-scoring;
// impossible accounting or proven cross-prompt replay remains a hard diagnostic
// FAIL. A missing expected marker alone is retained as a response-quality WARN.
func ProbeCacheRate(samples []CacheRateSample, expectedVariants, warmLoops, contextChars int) model.ProbeResult {
	if warmLoops <= 0 {
		warmLoops = 1
	}
	result := model.ProbeResult{
		ProbeKey: "p21_cache_rate", Kind: model.ProbeKindCacheRate,
		Evidence: map[string]any{
			"prompt_variants": expectedVariants,
			"context_chars":   contextChars,
			"samples":         cacheRateSampleEvidence(samples),
		},
	}
	expectedWarmSamples := expectedVariants * warmLoops
	expectedSamples := expectedVariants + expectedWarmSamples
	warmSamples, reportedWarm, hitWarmSamples := 0, 0, 0
	hitPromptIDs := make(map[string]struct{}, expectedVariants)
	warmPromptTokens, warmCachedTokens, warmCacheCreationTokens, warmTotalInputTokens := 0, 0, 0, 0
	coldPromptTokens, coldCachedTokens, coldCacheCreationTokens, coldTotalInputTokens := 0, 0, 0, 0
	failedRequests := 0
	markerMismatchSamples := 0
	markerMismatchPrompts := make([]string, 0)
	markerMismatchPromptSet := make(map[string]struct{}, expectedVariants)
	var coldTTFT, warmTTFT int64
	coldTTFTSamples, warmTTFTSamples := 0, 0
	for _, sample := range samples {
		if sample.Errored {
			failedRequests++
			continue
		}
		if sample.PromptTokens < 0 || sample.CachedTokens < 0 || sample.CacheCreationTokens < 0 {
			result.Status = model.ProbeStatusFail
			result.Evidence["capability"] = CacheRateCapabilityUnsupported
			result.Evidence["reason_code"] = "negative_token_count"
			result.Evidence["anomaly_prompt"] = sample.PromptID
			return result
		}
		if !sample.CacheTokensSeparate && sample.CachedTokens > sample.PromptTokens {
			result.Status = model.ProbeStatusFail
			result.Evidence["capability"] = CacheRateCapabilityMeasured
			result.Evidence["reason_code"] = "cached_exceeds_prompt"
			result.Evidence["anomaly_prompt"] = sample.PromptID
			return result
		}
		if (sample.CachedTokens > 0 || sample.CacheCreationTokens > 0) && !sample.TelemetryReported {
			result.Status = model.ProbeStatusFail
			result.Evidence["capability"] = CacheRateCapabilityUnsupported
			result.Evidence["reason_code"] = "unattributed_cached_tokens"
			result.Evidence["anomaly_prompt"] = sample.PromptID
			return result
		}
		if sample.ObservedPromptID != "" && sample.ObservedPromptID != sample.PromptID {
			result.Status = model.ProbeStatusFail
			result.Evidence["capability"] = CacheRateCapabilityMeasured
			result.Evidence["reason_code"] = "rotated_prompt_replay"
			result.Evidence["anomaly_prompt"] = sample.PromptID
			result.Evidence["expected_prompt"] = sample.PromptID
			result.Evidence["observed_prompt"] = sample.ObservedPromptID
			return result
		}
		markerMatched := sample.MarkerMatch || (sample.ObservedPromptID != "" && sample.ObservedPromptID == sample.PromptID)
		if !markerMatched {
			markerMismatchSamples++
			if _, exists := markerMismatchPromptSet[sample.PromptID]; sample.PromptID != "" && !exists {
				markerMismatchPromptSet[sample.PromptID] = struct{}{}
				markerMismatchPrompts = append(markerMismatchPrompts, sample.PromptID)
			}
		}
		if sample.Role == "warm" {
			warmSamples++
			if sample.TelemetryReported {
				reportedWarm++
				warmPromptTokens += sample.PromptTokens
				warmCachedTokens += sample.CachedTokens
				warmCacheCreationTokens += sample.CacheCreationTokens
				warmTotalInputTokens += cacheTotalInputTokens(
					sample.PromptTokens, sample.CachedTokens, sample.CacheCreationTokens, sample.CacheTokensSeparate,
				)
				if sample.CachedTokens > 0 {
					hitWarmSamples++
					hitPromptIDs[sample.PromptID] = struct{}{}
				}
			}
			if sample.FirstResponseMs > 0 {
				warmTTFT += sample.FirstResponseMs
				warmTTFTSamples++
			}
		} else if sample.Role == "cold" {
			if sample.TelemetryReported {
				coldPromptTokens += sample.PromptTokens
				coldCachedTokens += sample.CachedTokens
				coldCacheCreationTokens += sample.CacheCreationTokens
				coldTotalInputTokens += cacheTotalInputTokens(
					sample.PromptTokens, sample.CachedTokens, sample.CacheCreationTokens, sample.CacheTokensSeparate,
				)
			}
			if sample.FirstResponseMs > 0 {
				coldTTFT += sample.FirstResponseMs
				coldTTFTSamples++
			}
		}
	}

	cacheRate := percentRatio(warmCachedTokens, warmTotalInputTokens)
	promptHitRate := percentRatio(len(hitPromptIDs), expectedVariants)
	warmSampleHitRate := percentRatio(hitWarmSamples, expectedWarmSamples)
	telemetryCoverage := percentRatio(reportedWarm, expectedWarmSamples)
	result.Evidence["expected_samples"] = expectedSamples
	result.Evidence["warm_loops"] = warmLoops
	result.Evidence["expected_warm_samples"] = expectedWarmSamples
	result.Evidence["completed_samples"] = len(samples) - failedRequests
	result.Evidence["failed_requests"] = failedRequests
	result.Evidence["marker_mismatch_samples"] = markerMismatchSamples
	result.Evidence["marker_mismatch_prompts"] = markerMismatchPrompts
	result.Evidence["warm_samples"] = warmSamples
	result.Evidence["reported_warm_samples"] = reportedWarm
	result.Evidence["hit_prompts"] = len(hitPromptIDs)
	result.Evidence["hit_warm_samples"] = hitWarmSamples
	result.Evidence["cache_rate_pct"] = cacheRate
	result.Evidence["prompt_hit_rate_pct"] = promptHitRate
	result.Evidence["warm_sample_hit_rate_pct"] = warmSampleHitRate
	result.Evidence["telemetry_coverage_pct"] = telemetryCoverage
	result.Evidence["warm_prompt_tokens"] = warmPromptTokens
	result.Evidence["warm_cached_tokens"] = warmCachedTokens
	result.Evidence["warm_cache_creation_tokens"] = warmCacheCreationTokens
	result.Evidence["warm_total_input_tokens"] = warmTotalInputTokens
	result.Evidence["cold_prompt_tokens"] = coldPromptTokens
	result.Evidence["cold_cached_tokens"] = coldCachedTokens
	result.Evidence["cold_cache_creation_tokens"] = coldCacheCreationTokens
	result.Evidence["cold_total_input_tokens"] = coldTotalInputTokens
	if coldTTFTSamples > 0 {
		result.Evidence["cold_avg_ttft_ms"] = coldTTFT / int64(coldTTFTSamples)
	}
	if warmTTFTSamples > 0 {
		result.LatencyMs = warmTTFT / int64(warmTTFTSamples)
		result.Evidence["warm_avg_ttft_ms"] = result.LatencyMs
	}
	if coldTTFTSamples > 0 && warmTTFTSamples > 0 {
		coldAverage := float64(coldTTFT) / float64(coldTTFTSamples)
		warmAverage := float64(warmTTFT) / float64(warmTTFTSamples)
		result.Evidence["ttft_improvement_pct"] = roundCacheRate((coldAverage-warmAverage)/coldAverage*100, 2)
	}

	switch {
	case len(samples) != expectedSamples || failedRequests > 0 || warmSamples != expectedWarmSamples:
		result.Status = model.ProbeStatusError
		result.Evidence["capability"] = CacheRateCapabilityUnsupported
		result.Evidence["reason_code"] = "rotated_evidence_incomplete"
	case reportedWarm == 0:
		result.Status = model.ProbeStatusSkip
		result.Evidence["capability"] = CacheRateCapabilityUnsupported
		result.Evidence["reason_code"] = "telemetry_unsupported"
	case markerMismatchSamples > 0:
		result.Status = model.ProbeStatusWarn
		result.Evidence["capability"] = CacheRateCapabilityMeasured
		result.Evidence["reason_code"] = "expected_marker_missing"
	case reportedWarm < expectedWarmSamples:
		result.Status = model.ProbeStatusWarn
		result.Evidence["capability"] = CacheRateCapabilityMeasured
		result.Evidence["reason_code"] = "telemetry_partial"
	case warmCachedTokens == 0:
		result.Status = model.ProbeStatusWarn
		result.Evidence["capability"] = CacheRateCapabilityMeasured
		result.Evidence["reason_code"] = "cache_rate_zero"
	default:
		result.Status = model.ProbeStatusPass
		result.Evidence["capability"] = CacheRateCapabilityMeasured
		result.Evidence["reason_code"] = "cache_rate_measured"
	}
	return result
}

func cacheRateSampleEvidence(samples []CacheRateSample) []map[string]any {
	evidence := make([]map[string]any, 0, len(samples))
	for _, sample := range samples {
		row := map[string]any{
			"prompt_id": sample.PromptID, "role": sample.Role,
			"round":         sample.Round,
			"context_chars": sample.ContextChars, "prompt_tokens": sample.PromptTokens,
			"cached_tokens": sample.CachedTokens, "cache_creation_tokens": sample.CacheCreationTokens,
			"total_input_tokens": cacheTotalInputTokens(
				sample.PromptTokens, sample.CachedTokens, sample.CacheCreationTokens, sample.CacheTokensSeparate,
			),
			"cache_tokens_separate": sample.CacheTokensSeparate,
			"cache_rate_pct": percentRatio(sample.CachedTokens, cacheTotalInputTokens(
				sample.PromptTokens, sample.CachedTokens, sample.CacheCreationTokens, sample.CacheTokensSeparate,
			)),
			"telemetry_reported": sample.TelemetryReported, "first_response_ms": sample.FirstResponseMs,
			"upstream_cost": sample.UpstreamCostQuota, "marker_match": sample.MarkerMatch,
			"errored": sample.Errored,
		}
		if sample.ObservedPromptID != "" {
			row["observed_prompt_id"] = sample.ObservedPromptID
		}
		evidence = append(evidence, row)
	}
	return evidence
}

func percentRatio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return roundCacheRate(float64(numerator)/float64(denominator)*100, 2)
}

func roundCacheRate(value float64, places int) float64 {
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}
