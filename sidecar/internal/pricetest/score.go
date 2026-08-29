package pricetest

import (
	"sort"

	"supply-check-sdk/internal/model"
)

// configOnlyKinds are probes whose PASS does not prove that the served model was
// successfully measured. This includes local config/transport contracts and a
// well-formed rate-limit header on an error response. Such a pass must not, on
// its own, rescue an otherwise unreachable run from INCONCLUSIVE.
var configOnlyKinds = map[string]bool{
	model.ProbeKindCostAnchor:           true,
	model.ProbeKindCancellationContract: true, // validates local transport wiring, not the served model
	model.ProbeKindRateLimitContract:    true, // a valid header on a 429 must not rescue an otherwise unreachable run
}

// nonScoringKinds provide evidence but can never subtract numeric trust-score
// points. A concrete protocol contradiction may still cap/override the verdict
// through the explicit FAIL rules below. Databases may contain stale positive
// weights, so this guard must sit in the scorer rather than rely only on
// default-definition migrations.
var nonScoringKinds = map[string]bool{
	model.ProbeKindCacheAccounting:      true,
	model.ProbeKindFreshnessIntegrity:   true,
	model.ProbeKindProviderCacheControl: true,
	model.ProbeKindCacheRate:            true,
	model.ProbeKindProtocolContract:     true,
	model.ProbeKindStreamIntegrity:      true,
	model.ProbeKindUsageReconciliation:  true,
	model.ProbeKindCancellationContract: true,
	model.ProbeKindToolSchemaFidelity:   true,
	model.ProbeKindRateLimitContract:    true,
}

// Default per-kind raw weights. They are overridden by ProbeDefinition.Weight;
// enabled scoring definitions are proportionally normalized when their raw sum
// exceeds 100.
var defaultWeights = map[string]int{
	model.ProbeKindTokenCount:           25,
	model.ProbeKindLength:               15,
	model.ProbeKindIdentity:             25,
	model.ProbeKindGolden:               20,
	model.ProbeKindLatency:              5,
	model.ProbeKindCostAnchor:           10,
	model.ProbeKindCacheAccounting:      0,  // capability/accounting evidence; only contradictions affect verdict
	model.ProbeKindFreshnessIntegrity:   0,  // controlled replay evidence uses the hard verdict override below
	model.ProbeKindProviderCacheControl: 0,  // provider-native controls ride on P7 A/B/C
	model.ProbeKindSelfReport:           15, // WARN on a wrapped (non-pure) source; FAIL on a self-reported vendor swap
	model.ProbeKindProtocolContract:     0,
	model.ProbeKindStreamIntegrity:      0,
	model.ProbeKindUsageReconciliation:  0,
	model.ProbeKindCancellationContract: 0,
	model.ProbeKindToolSchemaFidelity:   0,
	model.ProbeKindRateLimitContract:    0,
	model.ProbeKindPromptLeakage:        20,
	model.ProbeKindInstructionPolicy:    20,
	model.ProbeKindToolSubstitution:     25,
	model.ProbeKindContextIntegrity:     10,
	model.ProbeKindChannelPurity:        15,
	model.ProbeKindCacheRate:            0,
}

// statusPenalty maps a probe status to its fraction of the weight subtracted
// from the trust score. error == inconclusive (network/timeout) and skip == not
// applicable are NEVER penalised — a flaky or unanchored upstream is not a
// watered one.
func statusPenalty(status string) float64 {
	switch status {
	case model.ProbeStatusFail:
		return 1.0
	case model.ProbeStatusWarn:
		return 0.4
	default: // pass, skip, error
		return 0.0
	}
}

// Score computes the per-channel trust score (0–100, higher is cleaner) and the
// verdict from a run's probe results. weightByKind overrides defaultWeights
// (pass nil to use defaults). Verdict thresholds:
//
//	85–100 OK | 50–84 SUSPICIOUS | 0–49 WATERED
//
// with two overrides that force WATERED regardless of the numeric score because
// they are high-confidence dilution signals: a model-identity swap (P3 fail), or
// the compound of token inflation (P1 fail) AND quality degradation (P4 fail).
// A single hard FAIL also caps the verdict at SUSPICIOUS.
func Score(results []model.ProbeResult, weightByKind map[string]int) (int, string) {
	if len(results) == 0 {
		return 100, model.ProbeVerdictOK
	}
	score := 100.0
	anyFail := false
	identityFail := false
	tokenFail := false
	goldenFail := false
	freshnessFail := false
	// measuredLive counts probes that produced a real signal (pass/warn/fail)
	// AND actually contacted the upstream. Config-only probes (cost_anchor) are
	// deliberately excluded: their pass reflects pricing config, not the served
	// model, so it can't stand in for "we measured the upstream".
	measuredLive := 0

	for _, r := range results {
		w := weightFor(r.Kind, weightByKind)
		score -= float64(w) * statusPenalty(r.Status)
		switch r.Status {
		case model.ProbeStatusPass, model.ProbeStatusWarn, model.ProbeStatusFail:
			if !configOnlyKinds[r.Kind] {
				measuredLive++
			}
		}
		if r.Status == model.ProbeStatusFail {
			anyFail = true
			switch r.Kind {
			case model.ProbeKindIdentity:
				identityFail = true
			case model.ProbeKindTokenCount:
				tokenFail = true
			case model.ProbeKindGolden:
				goldenFail = true
			case model.ProbeKindFreshnessIntegrity:
				freshnessFail = true
			}
		}
	}
	if score < 0 {
		score = 0
	}
	intScore := int(score + 0.5)

	// Nothing was actually measured against the upstream — every live probe errored
	// (upstream unreachable) or was N/A, and any pass came only from a config-only
	// probe (e.g. cost_anchor). Such a run is INCONCLUSIVE, not clean: without this
	// it would store as OK/100 (error/skip carry no penalty, and a config-only pass
	// leaves the score at 100) and masquerade as a healthy channel — the observed
	// gemini-3-flash "errored on every probe yet OK/100" blind spot.
	if measuredLive == 0 {
		return intScore, model.ProbeVerdictInconclusive
	}

	verdict := model.ProbeVerdictOK
	switch {
	case intScore < 50:
		verdict = model.ProbeVerdictWatered
	case intScore < 85:
		verdict = model.ProbeVerdictSuspicious
	}
	// Hard overrides — high-confidence dilution beats the numeric bucket.
	if identityFail || freshnessFail || (tokenFail && goldenFail) {
		verdict = model.ProbeVerdictWatered
	} else if anyFail && verdict == model.ProbeVerdictOK {
		verdict = model.ProbeVerdictSuspicious
	}
	return intScore, verdict
}

func weightFor(kind string, override map[string]int) int {
	if nonScoringKinds[kind] {
		return 0
	}
	if override != nil {
		if w, ok := override[kind]; ok {
			return w
		}
	}
	if w, ok := defaultWeights[kind]; ok {
		return w
	}
	return 10
}

// WeightsFromDefinitions builds a kind→weight map from enabled probe
// definitions so the score honours admin-tuned weights. An over-budget suite
// is proportionally normalized to 100: independently editable definitions
// must never make the numeric score subtract more than the documented scale.
// Under-budget suites stay under budget so disabling probes does not silently
// amplify the remaining signals.
func WeightsFromDefinitions(defs []*model.ProbeDefinition) map[string]int {
	type weightedKind struct {
		kind      string
		weight    int
		remainder int
	}
	weighted := make([]weightedKind, 0, len(defs))
	total := 0
	for _, d := range defs {
		if d == nil || !d.Enabled || nonScoringKinds[d.Kind] {
			continue
		}
		weight := d.Weight
		if weight <= 0 {
			weight = weightFor(d.Kind, nil)
		}
		if weight > 100 {
			weight = 100
		}
		weighted = append(weighted, weightedKind{kind: d.Kind, weight: weight})
		total += weight
	}
	out := make(map[string]int, len(weighted))
	if total <= 100 {
		for _, item := range weighted {
			out[item.kind] = item.weight
		}
		return out
	}

	assigned := 0
	for i := range weighted {
		scaled := weighted[i].weight * 100
		weighted[i].remainder = scaled % total
		weighted[i].weight = scaled / total
		assigned += weighted[i].weight
	}
	// Largest-remainder allocation keeps the integer map deterministic and its
	// sum byte-exactly 100 without favouring database row order.
	sort.Slice(weighted, func(i, j int) bool {
		if weighted[i].remainder != weighted[j].remainder {
			return weighted[i].remainder > weighted[j].remainder
		}
		return weighted[i].kind < weighted[j].kind
	})
	for i := 0; i < 100-assigned; i++ {
		weighted[i%len(weighted)].weight++
	}
	for _, item := range weighted {
		out[item.kind] = item.weight
	}
	return out
}
