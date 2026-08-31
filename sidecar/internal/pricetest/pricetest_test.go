package pricetest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"supply-check-sdk/internal/model"
)

func TestScore_AllPassIsOK(t *testing.T) {
	results := []model.ProbeResult{
		{Kind: model.ProbeKindTokenCount, Status: model.ProbeStatusPass},
		{Kind: model.ProbeKindIdentity, Status: model.ProbeStatusPass},
		{Kind: model.ProbeKindCostAnchor, Status: model.ProbeStatusPass},
	}
	score, verdict := Score(results, nil)
	assert.Equal(t, 100, score)
	assert.Equal(t, model.ProbeVerdictOK, verdict)
}

func TestScore_IdentityFailForcesWatered(t *testing.T) {
	// Identity fail alone subtracts only 25 (score 75 → SUSPICIOUS bucket) but
	// the override forces WATERED because a model swap is high-confidence.
	results := []model.ProbeResult{
		{Kind: model.ProbeKindIdentity, Status: model.ProbeStatusFail},
		{Kind: model.ProbeKindTokenCount, Status: model.ProbeStatusPass},
	}
	score, verdict := Score(results, nil)
	assert.Equal(t, 75, score)
	assert.Equal(t, model.ProbeVerdictWatered, verdict)
}

func TestScore_TokenPlusGoldenFailWatered(t *testing.T) {
	results := []model.ProbeResult{
		{Kind: model.ProbeKindTokenCount, Status: model.ProbeStatusFail}, // -25
		{Kind: model.ProbeKindGolden, Status: model.ProbeStatusFail},     // -20
	}
	score, verdict := Score(results, nil)
	assert.Equal(t, 55, score)                          // numerically SUSPICIOUS...
	assert.Equal(t, model.ProbeVerdictWatered, verdict) // ...but override → WATERED
}

func TestScore_SingleWarnStaysOK(t *testing.T) {
	results := []model.ProbeResult{
		{Kind: model.ProbeKindLatency, Status: model.ProbeStatusWarn}, // -2
	}
	score, verdict := Score(results, nil)
	assert.Equal(t, 98, score)
	assert.Equal(t, model.ProbeVerdictOK, verdict)
}

func TestScore_SingleFailCapsAtSuspicious(t *testing.T) {
	results := []model.ProbeResult{
		{Kind: model.ProbeKindLength, Status: model.ProbeStatusFail}, // -15 → 85, OK bucket
	}
	score, verdict := Score(results, nil)
	assert.Equal(t, 85, score)
	// 85 is the OK boundary, but a hard FAIL caps it at SUSPICIOUS.
	assert.Equal(t, model.ProbeVerdictSuspicious, verdict)
}

func TestScore_ErrorAndSkipNotPenalised(t *testing.T) {
	// error/skip carry no penalty, so the score stays 100 — but a run that
	// measured NOTHING (all error/skip) is INCONCLUSIVE, not clean.
	results := []model.ProbeResult{
		{Kind: model.ProbeKindTokenCount, Status: model.ProbeStatusError},
		{Kind: model.ProbeKindCostAnchor, Status: model.ProbeStatusSkip},
	}
	score, verdict := Score(results, nil)
	assert.Equal(t, 100, score)
	assert.Equal(t, model.ProbeVerdictInconclusive, verdict)
}

func TestScore_OneMeasuredIsNotInconclusive(t *testing.T) {
	// A single measured probe (even amid errors) means the run DID produce a
	// signal — it's a real verdict (OK here), not inconclusive.
	results := []model.ProbeResult{
		{Kind: model.ProbeKindTokenCount, Status: model.ProbeStatusError},
		{Kind: model.ProbeKindGolden, Status: model.ProbeStatusPass},
	}
	score, verdict := Score(results, nil)
	assert.Equal(t, 100, score)
	assert.Equal(t, model.ProbeVerdictOK, verdict)
}

func TestScore_ConfigOnlyPassAmidAllErrorsIsInconclusive(t *testing.T) {
	// The gemini-3-flash blind spot: every UPSTREAM probe errored, but the
	// config-only cost_anchor probe (no upstream call) passed. Without excluding
	// config-only kinds this stored as OK/100 and masqueraded as healthy. The
	// cost_anchor pass must not stand in for "we measured the upstream".
	results := []model.ProbeResult{
		{Kind: model.ProbeKindTokenCount, Status: model.ProbeStatusError},
		{Kind: model.ProbeKindIdentity, Status: model.ProbeStatusError},
		{Kind: model.ProbeKindGolden, Status: model.ProbeStatusError},
		{Kind: model.ProbeKindSelfReport, Status: model.ProbeStatusError},
		{Kind: model.ProbeKindCostAnchor, Status: model.ProbeStatusPass}, // config-only, no upstream
	}
	score, verdict := Score(results, nil)
	assert.Equal(t, 100, score)
	assert.Equal(t, model.ProbeVerdictInconclusive, verdict)
}

func TestScore_ConfigOnlyPassWithOneLiveSignalIsNotInconclusive(t *testing.T) {
	// Guard against over-broadening: a config-only pass alongside a genuine
	// upstream signal (golden passed against the real model) is a real verdict.
	results := []model.ProbeResult{
		{Kind: model.ProbeKindTokenCount, Status: model.ProbeStatusError},
		{Kind: model.ProbeKindGolden, Status: model.ProbeStatusPass}, // real upstream signal
		{Kind: model.ProbeKindCostAnchor, Status: model.ProbeStatusPass},
	}
	score, verdict := Score(results, nil)
	assert.Equal(t, 100, score)
	assert.Equal(t, model.ProbeVerdictOK, verdict)
}

func TestScore_CacheV2LegitimateHitAndUnsupportedDoNotPenalize(t *testing.T) {
	results := []model.ProbeResult{
		{Kind: model.ProbeKindGolden, Status: model.ProbeStatusPass},
		{Kind: model.ProbeKindCacheAccounting, Status: model.ProbeStatusPass},
		{Kind: model.ProbeKindFreshnessIntegrity, Status: model.ProbeStatusPass},
	}
	score, verdict := Score(results, nil)
	assert.Equal(t, 100, score)
	assert.Equal(t, model.ProbeVerdictOK, verdict)

	results[1].Status = model.ProbeStatusSkip
	score, verdict = Score(results, nil)
	assert.Equal(t, 100, score)
	assert.Equal(t, model.ProbeVerdictOK, verdict)

	// A stale database row must not reintroduce a deduction for either P7-v2
	// evidence kind. Accounting warnings are reported as evidence, not dilution.
	results[1].Status = model.ProbeStatusWarn
	score, verdict = Score(results, map[string]int{
		model.ProbeKindCacheAccounting:    100,
		model.ProbeKindFreshnessIntegrity: 100,
	})
	assert.Equal(t, 100, score)
	assert.Equal(t, model.ProbeVerdictOK, verdict)
}

func TestScore_FreshnessReplayForcesWateredWithoutWeightPenalty(t *testing.T) {
	results := []model.ProbeResult{
		{Kind: model.ProbeKindGolden, Status: model.ProbeStatusPass},
		{Kind: model.ProbeKindFreshnessIntegrity, Status: model.ProbeStatusFail},
	}
	score, verdict := Score(results, nil)
	assert.Equal(t, 100, score)
	assert.Equal(t, model.ProbeVerdictWatered, verdict)
}

func TestWeightsFromDefinitionsNormalizesOverBudgetTo100(t *testing.T) {
	defs := []*model.ProbeDefinition{
		{Kind: model.ProbeKindTokenCount, Enabled: true, Weight: 60},
		{Kind: model.ProbeKindGolden, Enabled: true, Weight: 30},
		{Kind: model.ProbeKindIdentity, Enabled: true, Weight: 30},
		{Kind: model.ProbeKindCacheAccounting, Enabled: true, Weight: 100},
		{Kind: model.ProbeKindLatency, Enabled: false, Weight: 100},
	}
	weights := WeightsFromDefinitions(defs)
	total := 0
	for _, weight := range weights {
		total += weight
	}
	assert.Equal(t, 100, total)
	assert.Equal(t, 50, weights[model.ProbeKindTokenCount])
	assert.Equal(t, 25, weights[model.ProbeKindGolden])
	assert.Equal(t, 25, weights[model.ProbeKindIdentity])
	assert.NotContains(t, weights, model.ProbeKindCacheAccounting)
	assert.NotContains(t, weights, model.ProbeKindLatency)
}

func TestWeightsFromDefinitionsKeepsUnderBudgetAndUsesDefaults(t *testing.T) {
	defs := []*model.ProbeDefinition{
		{Kind: model.ProbeKindTokenCount, Enabled: true, Weight: 0},
		{Kind: model.ProbeKindLatency, Enabled: true, Weight: 5},
	}
	weights := WeightsFromDefinitions(defs)
	assert.Equal(t, map[string]int{
		model.ProbeKindTokenCount: 25,
		model.ProbeKindLatency:    5,
	}, weights)
}

func TestProbeTokenCount_Bands(t *testing.T) {
	spec := model.ProbeSpec{TolerancePct: f(15), FailPct: f(30)}
	// 1.0 ratio → pass
	r := ProbeTokenCount(spec, TokenCountObs{UpstreamPromptTokens: 100, LocalPromptTokens: 100, IsOpenAIFamily: true})
	assert.Equal(t, model.ProbeStatusPass, r.Status)
	// 1.20 ratio (20% over) OpenAI → warn (>=15, <30)
	r = ProbeTokenCount(spec, TokenCountObs{UpstreamPromptTokens: 120, LocalPromptTokens: 100, IsOpenAIFamily: true})
	assert.Equal(t, model.ProbeStatusWarn, r.Status)
	// 1.40 ratio OpenAI → fail (>=30)
	r = ProbeTokenCount(spec, TokenCountObs{UpstreamPromptTokens: 140, LocalPromptTokens: 100, IsOpenAIFamily: true})
	assert.Equal(t, model.ProbeStatusFail, r.Status)
	// 1.40 ratio NON-OpenAI → capped at warn (tokenizer estimate)
	r = ProbeTokenCount(spec, TokenCountObs{UpstreamPromptTokens: 140, LocalPromptTokens: 100, IsOpenAIFamily: false})
	assert.Equal(t, model.ProbeStatusWarn, r.Status)
}

func TestQuality_NonOpenAITokenWarningsExposeEstimatorLimits(t *testing.T) {
	prompt := ProbeTokenCount(model.ProbeSpec{}, TokenCountObs{
		UpstreamPromptTokens: 170,
		LocalPromptTokens:    100,
		IsOpenAIFamily:       false,
	})
	assert.Equal(t, model.ProbeStatusWarn, prompt.Status)
	assert.Equal(t, "estimated_cross_tokenizer", prompt.Evidence["comparison_confidence"])
	assert.Equal(t, "non_openai_tokenizer_estimate", prompt.Evidence["reason_code"])

	completion := ProbeLength(model.ProbeSpec{}, LengthObs{
		CompletionTokens: 170,
		LocalRecount:     100,
		ContentOK:        true,
		IsOpenAIFamily:   false,
	})
	assert.Equal(t, model.ProbeStatusWarn, completion.Status)
	assert.Equal(t, "estimated_cross_tokenizer", completion.Evidence["comparison_confidence"])
	assert.Equal(t, "non_openai_tokenizer_estimate", completion.Evidence["reason_code"])
}

func TestProbeIdentity_FamilyMatch(t *testing.T) {
	spec := model.ProbeSpec{}
	// echoed dated id, same family → pass
	r := ProbeIdentity(spec, IdentityObs{RequestedModel: "gpt-5", UpstreamModel: "gpt-5-2026-01-01"})
	assert.Equal(t, model.ProbeStatusPass, r.Status)
	// swapped family → fail
	r = ProbeIdentity(spec, IdentityObs{RequestedModel: "gpt-5", UpstreamModel: "gpt-4o-mini"})
	assert.Equal(t, model.ProbeStatusFail, r.Status)
	// no echo → skip
	r = ProbeIdentity(spec, IdentityObs{RequestedModel: "gpt-5", UpstreamModel: ""})
	assert.Equal(t, model.ProbeStatusSkip, r.Status)
	// claude family with version suffix
	r = ProbeIdentity(spec, IdentityObs{RequestedModel: "claude-opus-4-8", UpstreamModel: "claude-opus-4-8-20260101"})
	assert.Equal(t, model.ProbeStatusPass, r.Status)

}

func TestQuality_BedrockIdentityQualifierIsRoutingNotFamily(t *testing.T) {
	spec := model.ProbeSpec{}
	r := ProbeIdentity(spec, IdentityObs{
		RequestedModel: "claude-haiku-4-5-20251001",
		UpstreamModel:  "global.anthropic.claude-haiku-4-5-20251001-v1:0",
	})
	assert.Equal(t, model.ProbeStatusPass, r.Status)

	r = ProbeIdentity(spec, IdentityObs{
		RequestedModel: "claude-haiku-4-5-20251001",
		UpstreamModel:  "global.anthropic.claude-sonnet-4-5-20250929-v1:0",
	})
	assert.Equal(t, model.ProbeStatusFail, r.Status)
}

func TestProbeCostAnchor(t *testing.T) {
	spec := model.ProbeSpec{}
	// no anchor → skip
	r := ProbeCostAnchor(spec, CostAnchorObs{AnchorFound: false})
	assert.Equal(t, model.ProbeStatusSkip, r.Status)
	// resolved <= anchor → pass (a normal discount)
	r = ProbeCostAnchor(spec, CostAnchorObs{AnchorFound: true, Mode: model.PricingModeToken,
		ResolvedInput: 1, ResolvedOutput: 4, AnchorInput: 2, AnchorOutput: 8})
	assert.Equal(t, model.ProbeStatusPass, r.Status)
	// resolved input above anchor → fail (markup over official list)
	r = ProbeCostAnchor(spec, CostAnchorObs{AnchorFound: true, Mode: model.PricingModeToken,
		ResolvedInput: 3, ResolvedOutput: 4, AnchorInput: 2, AnchorOutput: 8})
	assert.Equal(t, model.ProbeStatusFail, r.Status)
	// per-call mode
	r = ProbeCostAnchor(spec, CostAnchorObs{AnchorFound: true, Mode: model.PricingModePerCall,
		ResolvedPerCall: 0.05, AnchorPerCall: 0.04})
	assert.Equal(t, model.ProbeStatusFail, r.Status)
}

func TestProbeGolden_PassRate(t *testing.T) {
	spec := model.ProbeSpec{FailPct: f(60)}
	all := []GoldenAnswer{{Correct: true}, {Correct: true}, {Correct: true}, {Correct: true}}
	assert.Equal(t, model.ProbeStatusPass, ProbeGolden(spec, all).Status)
	mixed := []GoldenAnswer{{Correct: true}, {Correct: true}, {Correct: true}, {Correct: false}} // 75%
	assert.Equal(t, model.ProbeStatusWarn, ProbeGolden(spec, mixed).Status)
	bad := []GoldenAnswer{{Correct: true}, {Correct: false}, {Correct: false}, {Correct: false}} // 25%
	assert.Equal(t, model.ProbeStatusFail, ProbeGolden(spec, bad).Status)
}

func TestProbeLatency_NoBaselineSkips(t *testing.T) {
	spec := model.ProbeSpec{}
	// Real streaming, no baseline yet → skip (nothing to compare against).
	res := ProbeLatency(spec, LatencyObs{
		FirstResponseMs: 200, FirstChunkMs: 40, InterTokenMsP50: 12, Chunks: 8, HasContent: true,
	})
	assert.Equal(t, model.ProbeStatusSkip, res.Status)
	assert.Equal(t, int64(40), res.Evidence["ttft_ms"])
	assert.Equal(t, 8, res.Evidence["chunks"])
}

func TestProbeLatency_FakeStreamWarnsWithoutBaseline(t *testing.T) {
	spec := model.ProbeSpec{}
	// Whole answer in one frame despite content present → suspected fake stream,
	// WARN even with no baseline.
	res := ProbeLatency(spec, LatencyObs{
		FirstResponseMs: 300, FirstChunkMs: 300, Chunks: 1, HasContent: true, Streamed: true,
	})
	assert.Equal(t, model.ProbeStatusWarn, res.Status)
	assert.Equal(t, true, res.Evidence["suspected_fake_stream"])
}

func TestProbeLatency_NonStreamLengthProbeNoFakeStream(t *testing.T) {
	spec := model.ProbeSpec{}
	// A length probe run with Stream:false delivers content but zero chunks; it must
	// NOT be mistaken for fake streaming (Streamed=false gates the tell off).
	res := ProbeLatency(spec, LatencyObs{
		FirstResponseMs: 300, Chunks: 0, HasContent: true, Streamed: false,
	})
	assert.Equal(t, model.ProbeStatusSkip, res.Status)
	assert.Nil(t, res.Evidence["suspected_fake_stream"])
}

func TestProbeLatency_PrefersTTFTForSlowFirst(t *testing.T) {
	spec := model.ProbeSpec{}
	// Whole-request latency looks fine vs baseline, but TTFT is >3x the baseline
	// first-token time → WARN driven by the real TTFT, not whole-request latency.
	res := ProbeLatency(spec, LatencyObs{
		FirstResponseMs: 500, FirstChunkMs: 900, BaselineFirstMs: 100,
		Chunks: 10, InterTokenMsP50: 30, HasContent: true,
	})
	assert.Equal(t, model.ProbeStatusWarn, res.Status)
}

func TestProbeLatency_HealthyStreamPasses(t *testing.T) {
	spec := model.ProbeSpec{}
	res := ProbeLatency(spec, LatencyObs{
		FirstResponseMs: 400, FirstChunkMs: 120, BaselineFirstMs: 100,
		TokensPerSec: 40, BaselineTokensPerSec: 45,
		Chunks: 20, InterTokenMsP50: 18, HasContent: true,
	})
	assert.Equal(t, model.ProbeStatusPass, res.Status)
}

func f(v float64) *float64 { return &v }

// A streamed request where the timing reader recognised NO content frames
// (Chunks==0 — e.g. an upstream SSE schema we can't parse) must NOT be flagged
// as fake streaming; it degrades to "timing unavailable". This is the real-Claude
// production case that a naive Chunks<=1 guard false-flagged.
func TestProbeLatency_StreamedButUnparseableNoFakeStream(t *testing.T) {
	res := ProbeLatency(model.ProbeSpec{}, LatencyObs{
		FirstResponseMs: 300, Chunks: 0, HasContent: true, Streamed: true,
	})
	assert.Equal(t, model.ProbeStatusSkip, res.Status)
	assert.Nil(t, res.Evidence["suspected_fake_stream"])
}

// The length probe must not FAIL non-OpenAI models on a token-count over-ratio:
// their real completion_tokens legitimately differs from the local tiktoken
// recount (different tokenizer), so a gap is capped at WARN, never a conviction.
// This is the Claude/Gemini false-FAIL found in production real-machine testing.
func TestProbeLength_NonOpenAICapsAtWarn(t *testing.T) {
	over := LengthObs{CompletionTokens: 200, LocalRecount: 100, ContentOK: true} // ratio 2.0, well past tol
	// OpenAI family: a genuine 2x over-ratio is padding → FAIL.
	over.IsOpenAIFamily = true
	assert.Equal(t, model.ProbeStatusFail, ProbeLength(model.ProbeSpec{}, over).Status)
	// Non-OpenAI (Claude/Gemini): same ratio is tokenizer noise → WARN, not FAIL.
	over.IsOpenAIFamily = false
	assert.Equal(t, model.ProbeStatusWarn, ProbeLength(model.ProbeSpec{}, over).Status)

	// A clean ratio passes regardless of family.
	clean := LengthObs{CompletionTokens: 100, LocalRecount: 100, ContentOK: true, IsOpenAIFamily: false}
	assert.Equal(t, model.ProbeStatusPass, ProbeLength(model.ProbeSpec{}, clean).Status)
}
