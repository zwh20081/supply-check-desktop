package pricetest

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"supply-check-sdk/internal/model"
)

// Channel-purity / backend-blend analysis. A reseller often serves one "Claude"
// endpoint by load-balancing across multiple real backends (AWS Bedrock, GCP
// Vertex, Anthropic-direct) and/or repackaged subscriptions (Kiro/CCMax). This
// analysis samples a channel N times and infers, from the signals that survive
// the reseller's normalization, whether it's a clean single source, an opaque
// proxy, a suspected blend, or wrapper-repackaged.
//
// HONEST BY DESIGN: on a fully-normalizing reseller the backend cannot be NAMED
// (headers/model-id/fingerprint are all scrubbed — verified empirically). So the
// detector names a backend only when a signal actually leaks, and otherwise
// reports transparency + blend-SHAPE (single vs multi-source) without inventing
// a named pie chart.

// PuritySample is one billing-free identity+timing probe reduced to the
// backend-fingerprint signals that can survive reseller normalization.
type PuritySample struct {
	Errored        bool
	FirstChunkMs   int64   // TTFT (streamed); 0 if unavailable — physics, hard to fake
	TokensPerSec   float64 // throughput
	PromptTokens   int     // for a FIXED prompt: varies across backends that count differently
	SysFingerprint string  // often empty under normalization
	IdScheme       string  // "msg" | "chatcmpl" | "other" | "none"
	StopReason     string
	WrapperMarker  string // non-empty when self-report leaked a wrapper (Kiro/CCMax…)
	NamedBackend   string // "" | "bedrock" | "vertex" | "anthropic" — when a header/model-id named it
	Cached         bool   // upstream reported cached tokens (skews TTFT → excluded from timing)
}

// PurityCluster is one inferred source group and its share of the samples.
type PurityCluster struct {
	Label string  `json:"label"` // named backend, or "簇 A" / wrapper name
	Named bool    `json:"named"` // did a real signal NAME this, or is it shape-only?
	Share float64 `json:"share"` // 0..1
	Count int     `json:"count"`
}

// PurityResult is the operator-facing verdict.
type PurityResult struct {
	Samples      int             `json:"samples"`
	OkSamples    int             `json:"ok_samples"`
	Purity       int             `json:"purity"`        // dominant cluster share, 0..100
	Verdict      string          `json:"verdict"`       // see consts below
	Transparency string          `json:"transparency"`  // "transparent" | "partial" | "opaque"
	WrapperShare float64         `json:"wrapper_share"` // 0..1
	Clusters     []PurityCluster `json:"clusters"`
	Signals      map[string]any  `json:"signals"` // auditable evidence
}

const (
	PurityVerdictSingleClean       = "single_clean"       // one coherent, transparent source
	PurityVerdictOpaqueSingle      = "opaque_single"      // one source but fully normalized (black box)
	PurityVerdictSuspectedBlend    = "suspected_blend"    // ≥2 sources supported by a discrete signature split
	PurityVerdictSignatureVariance = "signature_variance" // a discrete outlier exists, but lacks repeatable support
	PurityVerdictTimingVariance    = "timing_variance"    // multimodal TTFT only; noteworthy, but not proof of a blend
	PurityVerdictWrapped           = "wrapped"            // repackaged via an agent/IDE subscription
	PurityVerdictInconclusive      = "inconclusive"       // too few usable samples
)

// AnalyzePurity is pure: samples → verdict. Thresholds are conservative to avoid
// crying "blend" on ordinary network jitter.
func AnalyzePurity(samples []PuritySample) PurityResult {
	res := PurityResult{Samples: len(samples), Signals: map[string]any{
		// These fields describe confidence in a blend/wrapper assertion, not
		// confidence that an otherwise coherent channel is globally "pure".
		"blend_basis": "none",
		"confidence":  "insufficient_evidence",
	}}
	ok := make([]PuritySample, 0, len(samples))
	for _, s := range samples {
		if !s.Errored {
			ok = append(ok, s)
		}
	}
	res.OkSamples = len(ok)
	if len(ok) < 3 {
		res.Verdict = PurityVerdictInconclusive
		res.Purity = 0
		res.Transparency = "opaque"
		res.Signals["reason"] = "need at least 3 usable samples"
		return res
	}

	// --- wrapper share (survives normalization; NAMES the wrapper) -------------
	wrapCount := map[string]int{}
	for _, s := range ok {
		if s.WrapperMarker != "" {
			wrapCount[strings.ToLower(s.WrapperMarker)]++
		}
	}
	wrapTotal, wrapName, wrapMax := 0, "", 0
	for name, c := range wrapCount {
		wrapTotal += c
		if c > wrapMax {
			wrapMax, wrapName = c, name
		}
	}
	res.WrapperShare = float64(wrapTotal) / float64(len(ok))
	if wrapTotal > 0 {
		res.Signals["wrapper"] = wrapName
	}

	// --- transparency: did any backend-NAMING signal leak? --------------------
	named := map[string]int{}
	for _, s := range ok {
		if backend := strings.ToLower(strings.TrimSpace(s.NamedBackend)); backend != "" {
			named[backend]++
		}
	}
	repeatableNamedBackends := 0
	for _, count := range named {
		if count >= 2 {
			repeatableNamedBackends++
		}
	}
	namedBackendConflict := repeatableNamedBackends >= 2
	namedBackendAnomaly := len(named) >= 2 && !namedBackendConflict
	switch {
	case len(named) == 0:
		res.Transparency = "opaque"
	case len(named) == 1 && named[firstKey(named)] == len(ok):
		// exactly one backend, named on EVERY sample
		res.Transparency = "transparent"
	default:
		res.Transparency = "partial"
	}
	res.Signals["named_backends"] = named
	res.Signals["repeatable_named_backends"] = repeatableNamedBackends
	res.Signals["named_backend_conflict"] = namedBackendConflict
	res.Signals["named_backend_anomaly"] = namedBackendAnomaly

	// --- discrete signature grouping (exact, noise-free) ----------------------
	// A single backend answering a FIXED prompt normally returns a stable
	// (prompt_tokens, fingerprint, id-scheme). A repeatable split is evidence of
	// multiple sources; a singleton is only an anomaly because optional
	// fingerprint / id telemetry may occasionally be absent.
	// The key is a struct (not a delimited string) so a raw fingerprint that
	// happens to contain the delimiter can't collide two backends into one group.
	sigGroups := map[puritySig][]PuritySample{}
	for _, s := range ok {
		sig := puritySig{
			promptTok: s.PromptTokens,
			fp:        strings.TrimSpace(s.SysFingerprint),
			scheme:    strings.ToLower(strings.TrimSpace(s.IdScheme)),
		}
		sigGroups[sig] = append(sigGroups[sig], s)
	}
	distinctPromptTok := distinctInts(ok)
	res.Signals["distinct_signatures"] = len(sigGroups)
	res.Signals["distinct_prompt_tokens"] = distinctPromptTok
	repeatableSignatures, singletonSignatures, minSignatureSamples := signatureSupport(sigGroups)
	res.Signals["repeatable_signatures"] = repeatableSignatures
	res.Signals["singleton_signatures"] = singletonSignatures
	res.Signals["min_signature_samples"] = minSignatureSamples
	strongSignaturePairs := strongRepeatableSignaturePairs(sigGroups)
	res.Signals["strong_signature_pairs"] = strongSignaturePairs

	// --- timing multimodality (physics; catches normalized blends) ------------
	ttft := make([]int64, 0, len(ok))
	for _, s := range ok {
		if s.FirstChunkMs > 0 && !s.Cached {
			ttft = append(ttft, s.FirstChunkMs)
		}
	}
	ttftClusters, ttftShares := clusterTTFT(ttft)
	res.Signals["ttft_samples"] = len(ttft)
	res.Signals["ttft_clusters"] = ttftClusters
	// < 4 usable timing points can't be clustered — a blend could hide in the
	// timing here. Flag it so "single/opaque" isn't over-read (e.g. mostly-cached
	// runs). Not a verdict change: we prefer a false-negative over a false alarm.
	res.Signals["ttft_conclusive"] = len(ttft) >= 4

	// Hard signature evidence requires a repeatable pair separated by an
	// independently meaningful value: two non-zero token counts or two concrete
	// ID schemes. Fingerprints can rotate within one backend, while a repeated
	// empty vs non-empty optional field is also telemetry variance.
	blendByDiscrete := strongSignaturePairs > 0
	discreteAnomaly := len(sigGroups) >= 2 && !blendByDiscrete
	timingMultimodal := ttftClusters >= 2
	res.Signals["discrete_signature_split"] = blendByDiscrete
	res.Signals["discrete_signature_anomaly"] = discreteAnomaly
	res.Signals["timing_multimodal"] = timingMultimodal

	// --- build clusters + purity ----------------------------------------------
	wrapped := res.WrapperShare >= 0.34 // a third+ of samples leak a wrapper
	switch {
	case wrapped:
		res.Signals["blend_basis"] = "wrapper_marker"
		res.Signals["confidence"] = "high"
		res.Verdict = PurityVerdictWrapped
		res.Clusters = clustersFromWrapper(ok, wrapName, res.WrapperShare)
		res.Purity = int(math.Round((1 - res.WrapperShare) * 100)) // "pure" share = the non-wrapped part
	case namedBackendConflict:
		// Explicit, repeated backend names are more direct evidence than inferred
		// signatures and must not disappear when the relay normalizes those
		// signatures to identical values.
		res.Signals["blend_basis"] = "named_backend_conflict"
		res.Signals["confidence"] = "high"
		res.Verdict = PurityVerdictSuspectedBlend
		res.Clusters, res.Purity = clustersFromNamedBackends(named, len(ok))
	case blendByDiscrete:
		res.Signals["blend_basis"] = "discrete_signature_split"
		res.Signals["confidence"] = "high"
		res.Verdict = PurityVerdictSuspectedBlend
		res.Clusters, res.Purity = clustersFromSignatures(sigGroups, len(ok))
	case namedBackendAnomaly:
		res.Signals["blend_basis"] = "named_backend_anomaly"
		res.Signals["confidence"] = "low"
		res.Verdict = PurityVerdictSignatureVariance
		res.Clusters, res.Purity = clustersFromNamedBackends(named, len(ok))
	case discreteAnomaly:
		// Keep the variance visible and auditable, but do not claim a blend when
		// the split is unsupported or only reflects optional telemetry presence.
		res.Signals["blend_basis"] = "discrete_signature_anomaly"
		res.Signals["confidence"] = "low"
		res.Verdict = PurityVerdictSignatureVariance
		res.Clusters, res.Purity = clustersFromSignatures(sigGroups, len(ok))
	case timingMultimodal:
		// TTFT modes can come from routing, but also from ordinary queueing,
		// regional network paths, cold starts, or provider-side scheduling. Keep
		// the shape visible without claiming that it proves multiple sources.
		res.Signals["blend_basis"] = "timing_multimodality_only"
		res.Signals["confidence"] = "low"
		res.Verdict = PurityVerdictTimingVariance
		res.Clusters, res.Purity = clustersFromTiming(ttftShares, len(ok))
	case res.Transparency == "opaque":
		res.Signals["confidence"] = "not_applicable"
		res.Verdict = PurityVerdictOpaqueSingle
		res.Purity = 100
		res.Clusters = []PurityCluster{{Label: "opaque", Named: false, Share: 1, Count: len(ok)}}
	default:
		res.Signals["confidence"] = "not_applicable"
		res.Verdict = PurityVerdictSingleClean
		res.Purity = 100
		label, named1 := "single", false
		if len(named) == 1 {
			label, named1 = firstKey(named), true
		}
		res.Clusters = []PurityCluster{{Label: label, Named: named1, Share: 1, Count: len(ok)}}
	}
	return res
}

// ProbeChannelPurity maps the reusable purity analysis into the common
// health-check result contract. Detailed clusters/signals remain structured so
// the web console and PDF can render the same auditable evidence.
func ProbeChannelPurity(analysis PurityResult, failedRequests int) model.ProbeResult {
	result := model.ProbeResult{
		ProbeKey: "p20_channel_purity", Kind: model.ProbeKindChannelPurity,
		Evidence: map[string]any{
			"samples": analysis.Samples, "ok_samples": analysis.OkSamples,
			"purity": analysis.Purity, "verdict": analysis.Verdict,
			"transparency": analysis.Transparency, "wrapper_share": analysis.WrapperShare,
			"clusters": analysis.Clusters, "signals": analysis.Signals,
			"blend_basis": analysis.Signals["blend_basis"], "confidence": analysis.Signals["confidence"],
			"failed_requests": failedRequests,
		},
	}
	switch analysis.Verdict {
	case PurityVerdictSingleClean:
		result.Status = model.ProbeStatusPass
	case PurityVerdictOpaqueSingle, PurityVerdictSignatureVariance, PurityVerdictTimingVariance:
		result.Status = model.ProbeStatusWarn
	case PurityVerdictSuspectedBlend, PurityVerdictWrapped:
		result.Status = model.ProbeStatusFail
	case PurityVerdictInconclusive:
		result.Status = model.ProbeStatusError
	default:
		result.Status = model.ProbeStatusError
		result.Evidence["reason_code"] = "unknown_purity_verdict"
	}
	return result
}

// clusterTTFT splits first-token times into modes. Conservative: two modes only
// when they are clearly separated (gap ≥ 2.5× the larger within-mode spread) and
// each holds ≥2 samples — so ordinary jitter stays one cluster. Returns the
// cluster count and each cluster's share of the timing samples.
func clusterTTFT(ttft []int64) (int, []float64) {
	if len(ttft) < 4 {
		return 1, []float64{1}
	}
	xs := append([]int64(nil), ttft...)
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	// drop a single cold-start outlier (the max) when we have enough points.
	if len(xs) >= 5 {
		xs = xs[:len(xs)-1]
	}
	// find the largest adjacent gap as the candidate split.
	bestGap, bestIdx := int64(0), -1
	for i := 1; i < len(xs); i++ {
		if g := xs[i] - xs[i-1]; g > bestGap {
			bestGap, bestIdx = g, i
		}
	}
	if bestIdx < 1 {
		return 1, []float64{1}
	}
	left, right := xs[:bestIdx], xs[bestIdx:]
	if len(left) < 2 || len(right) < 2 {
		return 1, []float64{1}
	}
	spread := func(a []int64) int64 {
		if len(a) < 2 {
			return 0
		}
		return a[len(a)-1] - a[0]
	}
	within := spread(left)
	if s := spread(right); s > within {
		within = s
	}
	if within == 0 {
		within = 1
	}
	// require a decisive separation to avoid false blends from jitter.
	if float64(bestGap) < 2.5*float64(within) {
		return 1, []float64{1}
	}
	total := float64(len(xs))
	return 2, []float64{float64(len(left)) / total, float64(len(right)) / total}
}

// puritySig is the exact discrete signature of a sample — a struct key so a raw
// fingerprint containing any delimiter can never collide two backends.
type puritySig struct {
	promptTok int
	fp        string
	scheme    string
}

// signatureSupport summarizes which observed discrete signatures are
// repeatable. The minimum is exposed as evidence so operators can distinguish
// a supported split from an isolated telemetry glitch.
func signatureSupport(groups map[puritySig][]PuritySample) (repeatable, singletons, minSamples int) {
	minSamples = 0
	for _, samples := range groups {
		n := len(samples)
		if minSamples == 0 || n < minSamples {
			minSamples = n
		}
		if n >= 2 {
			repeatable++
		} else {
			singletons++
		}
	}
	return repeatable, singletons, minSamples
}

// strongRepeatableSignaturePairs counts repeatable group pairs separated by a
// value that cannot be explained solely by optional telemetry disappearing.
func strongRepeatableSignaturePairs(groups map[puritySig][]PuritySample) int {
	repeatable := make([]puritySig, 0, len(groups))
	for signature, samples := range groups {
		if len(samples) >= 2 {
			repeatable = append(repeatable, signature)
		}
	}
	pairs := 0
	for left := 0; left < len(repeatable); left++ {
		for right := left + 1; right < len(repeatable); right++ {
			if signaturesHaveStrongSeparation(repeatable[left], repeatable[right]) {
				pairs++
			}
		}
	}
	return pairs
}

func signaturesHaveStrongSeparation(left, right puritySig) bool {
	if left.promptTok > 0 && right.promptTok > 0 && left.promptTok != right.promptTok {
		return true
	}
	// Fingerprints can rotate across deployments of the same official backend.
	// Preserve a repeatable fingerprint-only split as variance, but require a
	// tokenization or concrete ID-scheme difference for a hard blend claim.
	return concreteIDScheme(left.scheme) && concreteIDScheme(right.scheme) && left.scheme != right.scheme
}

func concreteIDScheme(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none", "other", "unknown":
		return false
	default:
		return true
	}
}

func clustersFromSignatures(groups map[puritySig][]PuritySample, total int) ([]PurityCluster, int) {
	type g struct {
		samples []PuritySample
		n       int
	}
	gs := make([]g, 0, len(groups))
	for _, ss := range groups {
		gs = append(gs, g{ss, len(ss)})
	}
	sort.Slice(gs, func(i, j int) bool { return gs[i].n > gs[j].n })
	out := make([]PurityCluster, 0, len(gs))
	for i, x := range gs {
		label, nm := groupBackendLabel(x.samples, i)
		out = append(out, PurityCluster{Label: label, Named: nm, Share: float64(x.n) / float64(total), Count: x.n})
	}
	return out, int(math.Round(float64(gs[0].n) / float64(total) * 100))
}

func clustersFromNamedBackends(named map[string]int, total int) ([]PurityCluster, int) {
	type group struct {
		label string
		count int
		named bool
	}
	groups := make([]group, 0, len(named)+1)
	namedTotal := 0
	for label, count := range named {
		groups = append(groups, group{label: label, count: count, named: true})
		namedTotal += count
	}
	if unnamed := total - namedTotal; unnamed > 0 {
		groups = append(groups, group{label: "unnamed", count: unnamed, named: false})
	}
	sort.Slice(groups, func(left, right int) bool {
		if groups[left].count == groups[right].count {
			return groups[left].label < groups[right].label
		}
		return groups[left].count > groups[right].count
	})
	clusters := make([]PurityCluster, 0, len(groups))
	for _, group := range groups {
		clusters = append(clusters, PurityCluster{
			Label: group.label, Named: group.named,
			Share: float64(group.count) / float64(total), Count: group.count,
		})
	}
	return clusters, int(math.Round(float64(groups[0].count) / float64(total) * 100))
}

// groupBackendLabel names a signature cluster by the dominant NamedBackend that
// actually appears in ITS samples (not a global sort order); falls back to a
// shape label when no backend leaked a name.
func groupBackendLabel(samples []PuritySample, idx int) (string, bool) {
	counts := map[string]int{}
	best, bestN := "", 0
	for _, s := range samples {
		backend := strings.ToLower(strings.TrimSpace(s.NamedBackend))
		if backend == "" {
			continue
		}
		counts[backend]++
		if counts[backend] > bestN {
			bestN, best = counts[backend], backend
		}
	}
	if best != "" {
		return best, true
	}
	return fmt.Sprintf("簇 %c", 'A'+idx), false
}

func clustersFromTiming(shares []float64, total int) ([]PurityCluster, int) {
	out := make([]PurityCluster, 0, len(shares))
	best := 0.0
	for i, sh := range shares {
		out = append(out, PurityCluster{Label: fmt.Sprintf("时序簇 %c", 'A'+i), Named: false, Share: sh, Count: int(math.Round(sh * float64(total)))})
		if sh > best {
			best = sh
		}
	}
	return out, int(math.Round(best * 100))
}

func clustersFromWrapper(ok []PuritySample, wrapName string, share float64) []PurityCluster {
	out := []PurityCluster{{Label: wrapName, Named: true, Share: share, Count: int(math.Round(share * float64(len(ok))))}}
	if share < 1 {
		out = append(out, PurityCluster{Label: "其余", Named: false, Share: 1 - share, Count: len(ok) - out[0].Count})
	}
	return out
}

func distinctInts(ok []PuritySample) int {
	seen := map[int]bool{}
	for _, s := range ok {
		seen[s.PromptTokens] = true
	}
	return len(seen)
}

func firstKey(m map[string]int) string {
	for k := range m {
		return k
	}
	return ""
}
