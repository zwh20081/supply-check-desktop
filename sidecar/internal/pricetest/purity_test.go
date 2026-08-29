package pricetest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"supply-check-sdk/internal/model"
)

// opaque single backend: everything normalized + constant, timing unimodal.
// This is the operator's real-channel case — must read as opaque_single/100.
func opaqueSamples(n int, ttftBase int64) []PuritySample {
	out := make([]PuritySample, n)
	for i := range out {
		out[i] = PuritySample{
			FirstChunkMs: ttftBase + int64(i%3)*8, // small jitter
			TokensPerSec: 40,
			PromptTokens: 20,
			IdScheme:     "msg",
		}
	}
	return out
}

func TestAnalyzePurity_TooFewSamples(t *testing.T) {
	r := AnalyzePurity(opaqueSamples(2, 120))
	assert.Equal(t, PurityVerdictInconclusive, r.Verdict)
}

func TestAnalyzePurity_OpaqueSingle(t *testing.T) {
	r := AnalyzePurity(opaqueSamples(8, 120))
	assert.Equal(t, PurityVerdictOpaqueSingle, r.Verdict)
	assert.Equal(t, 100, r.Purity)
	assert.Equal(t, "opaque", r.Transparency)
}

func TestAnalyzePurity_JitterIsNotBlend(t *testing.T) {
	// Ordinary network jitter (120–180ms spread, no clear gap) stays one cluster.
	s := []PuritySample{}
	for _, ms := range []int64{120, 128, 135, 141, 150, 158, 166, 179} {
		s = append(s, PuritySample{FirstChunkMs: ms, PromptTokens: 20, IdScheme: "msg"})
	}
	r := AnalyzePurity(s)
	assert.NotEqual(t, PurityVerdictSuspectedBlend, r.Verdict)
}

func TestAnalyzePurity_DiscreteBlend_PromptTokenSplit(t *testing.T) {
	// A fixed prompt returning two distinct prompt_token counts ⇒ ≥2 backends
	// that tokenize differently leaked through.
	s := []PuritySample{}
	for i := 0; i < 6; i++ {
		s = append(s, PuritySample{FirstChunkMs: 120, PromptTokens: 20, IdScheme: "msg"})
	}
	for i := 0; i < 4; i++ {
		s = append(s, PuritySample{FirstChunkMs: 120, PromptTokens: 21, IdScheme: "msg"})
	}
	r := AnalyzePurity(s)
	assert.Equal(t, PurityVerdictSuspectedBlend, r.Verdict)
	assert.Equal(t, 60, r.Purity) // dominant signature is 6/10
	assert.Len(t, r.Clusters, 2)
}

func TestAnalyzePurity_TimingBlend_Bimodal(t *testing.T) {
	// Two clearly separated TTFT modes (≈120ms vs ≈900ms) ⇒ load-balanced across
	// backends with very different network paths — even though everything else is
	// normalized identically.
	s := []PuritySample{}
	for _, ms := range []int64{115, 122, 130, 118, 900, 915, 930, 908} {
		s = append(s, PuritySample{FirstChunkMs: ms, PromptTokens: 20, IdScheme: "msg"})
	}
	r := AnalyzePurity(s)
	assert.Equal(t, PurityVerdictSuspectedBlend, r.Verdict)
}

func TestAnalyzePurity_Wrapped(t *testing.T) {
	s := opaqueSamples(8, 120)
	for i := 0; i < 4; i++ { // half leak a wrapper identity
		s[i].WrapperMarker = "kiro"
	}
	r := AnalyzePurity(s)
	assert.Equal(t, PurityVerdictWrapped, r.Verdict)
	assert.InDelta(t, 0.5, r.WrapperShare, 1e-9)
}

func TestAnalyzePurity_NamedSingleBackend(t *testing.T) {
	// A channel that DOES leak a backend name (e.g. x-amzn headers) on every
	// sample, consistently → transparent single_clean, named.
	s := opaqueSamples(6, 120)
	for i := range s {
		s[i].NamedBackend = "bedrock"
	}
	r := AnalyzePurity(s)
	assert.Equal(t, "transparent", r.Transparency)
	assert.Equal(t, PurityVerdictSingleClean, r.Verdict)
	assert.True(t, r.Clusters[0].Named)
	assert.Equal(t, "bedrock", r.Clusters[0].Label)
}

func TestAnalyzePurity_SignatureStructKeyNoCollision(t *testing.T) {
	// A delimited string key ("20|abc|def|msg") would MERGE these two distinct
	// groups and hide the blend; the struct key must keep them separate.
	s := []PuritySample{}
	for i := 0; i < 5; i++ {
		s = append(s, PuritySample{FirstChunkMs: 120, PromptTokens: 20, SysFingerprint: "abc|def", IdScheme: "msg"})
	}
	for i := 0; i < 5; i++ {
		s = append(s, PuritySample{FirstChunkMs: 120, PromptTokens: 20, SysFingerprint: "abc", IdScheme: "def|msg"})
	}
	r := AnalyzePurity(s)
	assert.Equal(t, PurityVerdictSuspectedBlend, r.Verdict)
	assert.Equal(t, 2, r.Signals["distinct_signatures"])
}

func TestAnalyzePurity_NamedBlendLabelsByActualGroup(t *testing.T) {
	// Two signature groups, each dominated by a different NAMED backend → each
	// cluster must be labeled by the backend actually in it, not a global order.
	s := []PuritySample{}
	for i := 0; i < 5; i++ {
		s = append(s, PuritySample{FirstChunkMs: 120, PromptTokens: 20, IdScheme: "msg", NamedBackend: "bedrock"})
	}
	for i := 0; i < 4; i++ {
		s = append(s, PuritySample{FirstChunkMs: 120, PromptTokens: 21, IdScheme: "msg", NamedBackend: "vertex"})
	}
	r := AnalyzePurity(s)
	assert.Equal(t, PurityVerdictSuspectedBlend, r.Verdict)
	labeled := map[string]bool{}
	for _, c := range r.Clusters {
		labeled[c.Label] = c.Named
	}
	assert.True(t, labeled["bedrock"], "a cluster labeled by its actual bedrock samples")
	assert.True(t, labeled["vertex"], "a cluster labeled by its actual vertex samples")
}

func TestAnalyzePurity_CachedExcludedFromTiming(t *testing.T) {
	// Cached samples have skewed (fast) TTFT — they must not create a false
	// second timing cluster.
	s := opaqueSamples(6, 400)
	s = append(s,
		PuritySample{FirstChunkMs: 5, PromptTokens: 20, IdScheme: "msg", Cached: true},
		PuritySample{FirstChunkMs: 6, PromptTokens: 20, IdScheme: "msg", Cached: true},
	)
	r := AnalyzePurity(s)
	assert.NotEqual(t, PurityVerdictSuspectedBlend, r.Verdict)
}

func TestProbeChannelPurityMapsAnalysisToHealthCheckStatus(t *testing.T) {
	tests := []struct {
		verdict string
		status  string
	}{
		{PurityVerdictSingleClean, model.ProbeStatusPass},
		{PurityVerdictOpaqueSingle, model.ProbeStatusWarn},
		{PurityVerdictSuspectedBlend, model.ProbeStatusFail},
		{PurityVerdictWrapped, model.ProbeStatusFail},
		{PurityVerdictInconclusive, model.ProbeStatusError},
	}
	for _, test := range tests {
		result := ProbeChannelPurity(PurityResult{
			Samples: 10, OkSamples: 9, Purity: 80, Verdict: test.verdict,
			Clusters: []PurityCluster{{Label: "opaque", Share: 0.8, Count: 8}},
			Signals:  map[string]any{"distinct_signatures": 2},
		}, 1)
		require.Equal(t, test.status, result.Status)
		require.Equal(t, 1, result.Evidence["failed_requests"])
	}
}
