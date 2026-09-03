package pricetest

import (
	"testing"

	"supply-check-sdk/internal/model"
)

// Adversarial scoring suite.
//
// Threat model: the relay operator has READ THIS SOURCE. They know which probes
// convict them and they control every byte the auditor sees, including the
// choice to answer nothing at all. These tests encode the property that makes
// the audit meaningful:
//
//	Refusing to be measured must never score better than being measured.
//
// The `error` status carries no numeric penalty on purpose (a flaky upstream is
// not a watered one). That kindness is exactly what an adversary would farm, so
// every test below checks the evidence gate rather than the arithmetic alone.

// adversaryWeights mirrors the enabled scoring definitions the runner ships, so
// these tests move if production weights move.
func adversaryWeights() map[string]int {
	values := []model.ProbeDefinition{
		{Key: "p1", Kind: model.ProbeKindTokenCount, Enabled: true, Weight: 25},
		{Key: "p2", Kind: model.ProbeKindLength, Enabled: true, Weight: 15},
		{Key: "p3", Kind: model.ProbeKindIdentity, Enabled: true, Weight: 25},
		{Key: "p4", Kind: model.ProbeKindGolden, Enabled: true, Weight: 20},
		{Key: "p5", Kind: model.ProbeKindLatency, Enabled: true, Weight: 5},
		{Key: "p6", Kind: model.ProbeKindCostAnchor, Enabled: true, Weight: 10},
		{Key: "p8", Kind: model.ProbeKindSelfReport, Enabled: true, Weight: 15},
		{Key: "p16", Kind: model.ProbeKindPromptLeakage, Enabled: true, Weight: 20},
		{Key: "p17", Kind: model.ProbeKindInstructionPolicy, Enabled: true, Weight: 20},
		{Key: "p18", Kind: model.ProbeKindToolSubstitution, Enabled: true, Weight: 25},
		{Key: "p19", Kind: model.ProbeKindContextIntegrity, Enabled: true, Weight: 10},
		{Key: "p20", Kind: model.ProbeKindChannelPurity, Enabled: true, Weight: 15},
	}
	defs := make([]*model.ProbeDefinition, len(values))
	for i := range values {
		defs[i] = &values[i]
	}
	return WeightsFromDefinitions(defs)
}

// scoringKinds is every probe that can move the numeric score.
var scoringKinds = []string{
	model.ProbeKindTokenCount, model.ProbeKindLength, model.ProbeKindIdentity,
	model.ProbeKindGolden, model.ProbeKindLatency, model.ProbeKindSelfReport,
	model.ProbeKindPromptLeakage, model.ProbeKindInstructionPolicy,
	model.ProbeKindToolSubstitution, model.ProbeKindContextIntegrity,
	model.ProbeKindChannelPurity,
}

// suite builds a full result set where every kind passes except those listed in
// overrides, which take the given status.
func suite(overrides map[string]string) []model.ProbeResult {
	results := make([]model.ProbeResult, 0, len(scoringKinds)+1)
	for _, kind := range scoringKinds {
		status := model.ProbeStatusPass
		if override, ok := overrides[kind]; ok {
			status = override
		}
		results = append(results, model.ProbeResult{Kind: kind, Status: status})
	}
	// cost_anchor is config-only and always SKIP in standalone SDK mode.
	results = append(results, model.ProbeResult{Kind: model.ProbeKindCostAnchor, Status: model.ProbeStatusSkip})
	return results
}

// The four probes that actually catch a model swap.
var catchProbes = []string{
	model.ProbeKindIdentity, model.ProbeKindSelfReport,
	model.ProbeKindGolden, model.ProbeKindTokenCount,
}

func statusMap(kinds []string, status string) map[string]string {
	out := make(map[string]string, len(kinds))
	for _, kind := range kinds {
		out[kind] = status
	}
	return out
}

// TestStonewallingNeverBeatsHonesty is the core property. A relay that fails the
// catching probes and one that refuses them are the same relay; the refusing one
// must not receive the better verdict.
func TestStonewallingNeverBeatsHonesty(t *testing.T) {
	weights := adversaryWeights()

	honestScore, honestVerdict, _ := ScoreWithCoverage(suite(statusMap(catchProbes, model.ProbeStatusFail)), weights)
	if honestVerdict != model.ProbeVerdictWatered {
		t.Fatalf("a channel that fails identity+self_report+golden+token must be WATERED, got %s (%d)", honestVerdict, honestScore)
	}

	stonewallScore, stonewallVerdict, coverage := ScoreWithCoverage(suite(statusMap(catchProbes, model.ProbeStatusError)), weights)
	if stonewallVerdict == model.ProbeVerdictOK {
		t.Fatalf("stonewalling the catching probes scored OK (%d) — refusing measurement must never read as clean", stonewallScore)
	}
	if stonewallVerdict != model.ProbeVerdictInconclusive {
		t.Fatalf("stonewalled run should be INCONCLUSIVE, got %s", stonewallVerdict)
	}
	if coverage.InsufficientReason == "" {
		t.Error("an INCONCLUSIVE verdict must record why the evidence was insufficient")
	}
	// catchProbes covers 4 of the 5 critical probes (p2_length still answers).
	if coverage.CriticalErrors != len(catchProbes) {
		t.Errorf("critical errors = %d, want %d", coverage.CriticalErrors, len(catchProbes))
	}
	if got := coverage.CriticalErrorRate(); got < 0.75 {
		t.Errorf("critical error rate = %v, want a high rate when the catching probes are stonewalled", got)
	}
}

// TestSingleSurvivingPassCannotCertifyChannel covers the narrower laundering
// route: keep exactly one cheap probe alive so measuredLive > 0, error the rest.
func TestSingleSurvivingPassCannotCertifyChannel(t *testing.T) {
	weights := adversaryWeights()
	overrides := map[string]string{}
	for _, kind := range scoringKinds {
		if kind != model.ProbeKindLatency {
			overrides[kind] = model.ProbeStatusError
		}
	}
	score, verdict, coverage := ScoreWithCoverage(suite(overrides), weights)
	if verdict == model.ProbeVerdictOK {
		t.Fatalf("one surviving latency pass certified the channel as OK (%d); latency proves nothing about identity", score)
	}
	if verdict != model.ProbeVerdictInconclusive {
		t.Fatalf("want INCONCLUSIVE, got %s", verdict)
	}
	if coverage.MeasuredLive == 0 {
		t.Error("this scenario must exercise the evidence gate, not the older measuredLive==0 guard")
	}
}

// TestPartialStonewallingStillGated: silencing only the identity group is enough
// to void a clean verdict, even when authenticity probes all pass.
func TestPartialStonewallingStillGated(t *testing.T) {
	weights := adversaryWeights()

	identitySilenced, verdict, coverage := ScoreWithCoverage(
		suite(statusMap([]string{model.ProbeKindIdentity, model.ProbeKindSelfReport}, model.ProbeStatusError)), weights)
	if verdict != model.ProbeVerdictInconclusive {
		t.Fatalf("identity group fully unmeasured must be INCONCLUSIVE, got %s (%d)", verdict, identitySilenced)
	}
	if coverage.InsufficientReason != "identity_unmeasured" {
		t.Errorf("reason = %q, want identity_unmeasured", coverage.InsufficientReason)
	}

	_, verdict, coverage = ScoreWithCoverage(
		suite(statusMap([]string{model.ProbeKindTokenCount, model.ProbeKindLength, model.ProbeKindGolden}, model.ProbeStatusError)), weights)
	if verdict != model.ProbeVerdictInconclusive {
		t.Fatalf("authenticity group fully unmeasured must be INCONCLUSIVE, got %s", verdict)
	}
	if coverage.InsufficientReason != "authenticity_unmeasured" {
		t.Errorf("reason = %q, want authenticity_unmeasured", coverage.InsufficientReason)
	}
}

// TestObservedSwapSurvivesStonewalling is the counterweight to the gate: a relay
// must not be able to UPGRADE a caught swap to INCONCLUSIVE by erroring the rest
// of the suite. Real evidence outranks missing evidence in both directions.
func TestObservedSwapSurvivesStonewalling(t *testing.T) {
	weights := adversaryWeights()
	overrides := statusMap(scoringKinds, model.ProbeStatusError)
	overrides[model.ProbeKindIdentity] = model.ProbeStatusFail // the one thing that came back

	_, verdict, _ := ScoreWithCoverage(suite(overrides), weights)
	if verdict != model.ProbeVerdictWatered {
		t.Fatalf("an observed identity swap must stay WATERED even when everything else errors, got %s", verdict)
	}

	// Same for the freshness (replay) override.
	overrides = statusMap(scoringKinds, model.ProbeStatusError)
	results := append(suite(overrides), model.ProbeResult{
		Kind: model.ProbeKindFreshnessIntegrity, Status: model.ProbeStatusFail,
	})
	if _, verdict, _ = ScoreWithCoverage(results, weights); verdict != model.ProbeVerdictWatered {
		t.Fatalf("observed stale replay must stay WATERED, got %s", verdict)
	}
}

// TestHonestChannelStillPasses guards the other failure mode: the gate must not
// turn ordinary healthy runs into INCONCLUSIVE noise.
func TestHonestChannelStillPasses(t *testing.T) {
	weights := adversaryWeights()

	score, verdict, coverage := ScoreWithCoverage(suite(nil), weights)
	if verdict != model.ProbeVerdictOK || score != 100 {
		t.Fatalf("a fully passing suite must be OK/100, got %s/%d", verdict, score)
	}
	if coverage.CriticalErrorRate() != 0 {
		t.Errorf("critical error rate = %v, want 0", coverage.CriticalErrorRate())
	}

	// A genuinely flaky upstream: one probe from each critical group errors, the
	// rest answer. Evidence still exists in both groups, so a verdict is allowed.
	flaky := map[string]string{
		model.ProbeKindSelfReport: model.ProbeStatusError,
		model.ProbeKindLength:     model.ProbeStatusError,
	}
	if _, verdict, _ = ScoreWithCoverage(suite(flaky), weights); verdict != model.ProbeVerdictOK {
		t.Fatalf("partial flakiness with surviving evidence should still allow OK, got %s", verdict)
	}
}

// TestEmptyRunIsInconclusive: no results is the absence of evidence, not proof
// of cleanliness. The old contract returned OK/100 here.
func TestEmptyRunIsInconclusive(t *testing.T) {
	_, verdict, coverage := ScoreWithCoverage(nil, nil)
	if verdict != model.ProbeVerdictInconclusive {
		t.Fatalf("empty run must be INCONCLUSIVE, got %s", verdict)
	}
	if coverage.InsufficientReason != "no_results" {
		t.Errorf("reason = %q, want no_results", coverage.InsufficientReason)
	}
}

// TestSkipCountsAsUnmeasured: a SKIP is "not applicable", which is still not
// evidence about the served model. An adversary should gain nothing by steering
// probes into SKIP instead of ERROR.
func TestSkipCountsAsUnmeasured(t *testing.T) {
	weights := adversaryWeights()
	_, verdict, coverage := ScoreWithCoverage(suite(statusMap(catchProbes, model.ProbeStatusSkip)), weights)
	if verdict == model.ProbeVerdictOK {
		t.Fatal("skipping every catching probe must not read as clean")
	}
	if coverage.CriticalErrors != len(catchProbes) {
		t.Errorf("critical errors = %d, want %d — SKIP must count as uncovered", coverage.CriticalErrors, len(catchProbes))
	}
}

// TestScoreWrapperMatchesCoverageVariant keeps the two entry points honest.
func TestScoreWrapperMatchesCoverageVariant(t *testing.T) {
	weights := adversaryWeights()
	for name, results := range map[string][]model.ProbeResult{
		"clean":     suite(nil),
		"watered":   suite(statusMap(catchProbes, model.ProbeStatusFail)),
		"stonewall": suite(statusMap(catchProbes, model.ProbeStatusError)),
	} {
		wantScore, wantVerdict, _ := ScoreWithCoverage(results, weights)
		gotScore, gotVerdict := Score(results, weights)
		if gotScore != wantScore || gotVerdict != wantVerdict {
			t.Errorf("%s: Score() = %d/%s, ScoreWithCoverage() = %d/%s", name, gotScore, gotVerdict, wantScore, wantVerdict)
		}
	}
}
