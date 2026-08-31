// Package pricetest implements the active "货源体检" probes that detect upstream
// "watering" (兑水): token inflation, completion padding, model-identity swap,
// quality degradation, latency collapse, and cost/anchor drift. The probe
// functions here are PURE — they take observations the runner gathered from a
// (billing-free) test request and return a model.ProbeResult with structured,
// auditable evidence. The runner (runner.go) wires real requests into them.
package pricetest

import (
	"math"
	"regexp"
	"strings"

	"supply-check-sdk/internal/model"
)

// GoldenAnswer is one golden-item outcome: what the model returned and whether
// it matched the expectation.
type GoldenAnswer struct {
	Prompt  string `json:"prompt"`
	Got     string `json:"got"`
	Correct bool   `json:"correct"`
}

// TokenCountObs holds the inputs for the P1 token-count probe.
type TokenCountObs struct {
	UpstreamPromptTokens int
	LocalPromptTokens    int
	// IsOpenAIFamily gates the FAIL verdict: the local tiktoken count is only
	// trustworthy for OpenAI-family models; for others a gap is likely tokenizer
	// noise, so we cap at WARN.
	IsOpenAIFamily bool
}

// ProbeTokenCount (P1): compare upstream-reported prompt_tokens to a local
// recount. A sustained, one-sided inflation flags overbilling on input.
func ProbeTokenCount(spec model.ProbeSpec, obs TokenCountObs) model.ProbeResult {
	res := model.ProbeResult{ProbeKey: "p1_token_count", Kind: model.ProbeKindTokenCount}
	if obs.LocalPromptTokens <= 0 || obs.UpstreamPromptTokens <= 0 {
		res.Status = model.ProbeStatusSkip
		res.Evidence = map[string]any{"reason": "missing token counts"}
		return res
	}
	ratio := float64(obs.UpstreamPromptTokens) / float64(obs.LocalPromptTokens)
	warnPct := valOr(spec.TolerancePct, 15)
	failPct := valOr(spec.FailPct, 30)
	overPct := (ratio - 1) * 100
	res.Evidence = map[string]any{
		"upstream_prompt_tokens": obs.UpstreamPromptTokens,
		"local_prompt_tokens":    obs.LocalPromptTokens,
		"ratio":                  round4(ratio),
		"over_pct":               round1(overPct),
		"warn_pct":               warnPct,
		"fail_pct":               failPct,
		"openai_family":          obs.IsOpenAIFamily,
	}
	if obs.IsOpenAIFamily {
		res.Evidence["comparison_confidence"] = "exact_tokenizer_family"
	} else {
		res.Evidence["comparison_confidence"] = "estimated_cross_tokenizer"
	}
	switch {
	case overPct >= failPct && obs.IsOpenAIFamily:
		res.Status = model.ProbeStatusFail
	case overPct >= warnPct:
		// Non-OpenAI families never FAIL on token count (tokenizer is an
		// estimate); they cap at WARN even past the fail band.
		res.Status = model.ProbeStatusWarn
		if !obs.IsOpenAIFamily {
			res.Evidence["reason_code"] = "non_openai_tokenizer_estimate"
		}
	default:
		res.Status = model.ProbeStatusPass
	}
	return res
}

// LengthObs holds inputs for the P2 completion-length probe.
type LengthObs struct {
	CompletionTokens int
	LocalRecount     int // local token count of the returned text
	ContentOK        bool
	// IsOpenAIFamily gates the FAIL verdict, exactly like ProbeTokenCount: the
	// local recount uses tiktoken, faithful only for OpenAI-family models. For
	// Anthropic / Gemini / others the model's real completion_tokens legitimately
	// differs from a tiktoken recount (different tokenizer), so an over-ratio is
	// likely tokenizer noise, not output padding — cap at WARN, never FAIL.
	IsOpenAIFamily bool
}

// ProbeLength (P2): with max_tokens=N and a fixed-length task, assert that the
// reported completion_tokens isn't inflated beyond a local recount of the text.
func ProbeLength(spec model.ProbeSpec, obs LengthObs) model.ProbeResult {
	res := model.ProbeResult{ProbeKey: "p2_length", Kind: model.ProbeKindLength}
	if obs.CompletionTokens <= 0 || obs.LocalRecount <= 0 {
		res.Status = model.ProbeStatusSkip
		res.Evidence = map[string]any{"reason": "missing completion counts"}
		return res
	}
	ratio := float64(obs.CompletionTokens) / float64(obs.LocalRecount)
	tol := valOr(spec.TolerancePct, 25) / 100
	res.Evidence = map[string]any{
		"completion_tokens": obs.CompletionTokens,
		"local_recount":     obs.LocalRecount,
		"ratio":             round4(ratio),
		"content_ok":        obs.ContentOK,
		"openai_family":     obs.IsOpenAIFamily,
	}
	if obs.IsOpenAIFamily {
		res.Evidence["comparison_confidence"] = "exact_tokenizer_family"
	} else {
		res.Evidence["comparison_confidence"] = "estimated_cross_tokenizer"
	}
	switch {
	case ratio > 1+tol && obs.IsOpenAIFamily:
		res.Status = model.ProbeStatusFail // server-side padding of completion tokens
	case ratio > 1+tol:
		// Non-OpenAI tokenizer mismatch — flag but don't convict.
		res.Status = model.ProbeStatusWarn
		res.Evidence["reason_code"] = "non_openai_tokenizer_estimate"
	case !obs.ContentOK:
		res.Status = model.ProbeStatusWarn
	default:
		res.Status = model.ProbeStatusPass
	}
	return res
}

// IdentityObs holds inputs for the P3 model-identity probe.
type IdentityObs struct {
	RequestedModel    string
	UpstreamModel     string // raw model echoed by upstream BEFORE the relay rewrite
	SystemFingerprint string
}

// ProbeIdentity (P3): compare the model family the upstream echoes to the one we
// requested. No echo → skip (many adapters omit it); family mismatch → fail.
func ProbeIdentity(spec model.ProbeSpec, obs IdentityObs) model.ProbeResult {
	res := model.ProbeResult{ProbeKey: "p3_identity", Kind: model.ProbeKindIdentity}
	got := strings.TrimSpace(obs.UpstreamModel)
	res.Evidence = map[string]any{
		"requested":          obs.RequestedModel,
		"upstream_model":     got,
		"system_fingerprint": obs.SystemFingerprint,
	}
	if got == "" {
		res.Status = model.ProbeStatusSkip
		res.Evidence["reason"] = "upstream did not echo a model id"
		return res
	}
	if sameModelFamily(obs.RequestedModel, got) {
		res.Status = model.ProbeStatusPass
	} else {
		res.Status = model.ProbeStatusFail
		res.Evidence["requested_family"] = modelFamily(obs.RequestedModel)
		res.Evidence["upstream_family"] = modelFamily(got)
	}
	return res
}

// ProbeGolden (P4): pass-rate over a deterministic golden battery. Below 100% →
// warn; below the fail band → fail (gross quality degradation / quantization).
func ProbeGolden(spec model.ProbeSpec, answers []GoldenAnswer) model.ProbeResult {
	res := model.ProbeResult{ProbeKey: "p4_golden", Kind: model.ProbeKindGolden}
	if len(answers) == 0 {
		res.Status = model.ProbeStatusSkip
		res.Evidence = map[string]any{"reason": "no golden items"}
		return res
	}
	correct := 0
	for _, a := range answers {
		if a.Correct {
			correct++
		}
	}
	passPct := float64(correct) / float64(len(answers)) * 100
	failPct := valOr(spec.FailPct, 60)
	res.Evidence = map[string]any{
		"items":    len(answers),
		"correct":  correct,
		"pass_pct": round1(passPct),
		"fail_pct": failPct,
		"answers":  answers,
	}
	switch {
	case passPct < failPct:
		res.Status = model.ProbeStatusFail
	case passPct < 100:
		res.Status = model.ProbeStatusWarn
	default:
		res.Status = model.ProbeStatusPass
	}
	return res
}

// LatencyObs holds inputs for the P5 latency/throughput probe.
//
// FirstResponseMs is the whole-request latency (always available). When the
// probe ran in stream mode and the upstream delivered distinct chunks, the
// timing-fingerprint fields carry the real streaming cadence:
//   - FirstChunkMs    — TTFT: ms to the first content chunk (0 = unavailable)
//   - InterTokenMsP50 — median gap between content chunks (0 = <2 chunks)
//   - Chunks          — number of content-bearing chunks observed
//
// A whole answer arriving in a single frame (Chunks<=1 with non-empty content)
// is a tell for faked/non-streaming delivery — often a swapped small model or an
// aggressively quantized backend that batches then dumps (arXiv:2502.20589).
type LatencyObs struct {
	FirstResponseMs      int64
	TokensPerSec         float64
	BaselineFirstMs      int64
	BaselineTokensPerSec float64

	// Streamed reports whether the probe request actually asked the upstream to
	// stream (spec.Stream). It is the sole authority for the fake-stream tell:
	// without it, a non-streamed length probe (Chunks==0, content present) would
	// masquerade as "whole answer in one frame" and trip a spurious WARN.
	Streamed bool

	// Streaming timing fingerprint (0 when the probe wasn't streamed or the
	// upstream didn't expose per-chunk timing → graceful degrade to whole-request).
	FirstChunkMs    int64
	InterTokenMsP50 float64
	Chunks          int
	HasContent      bool // did the streamed response carry any assistant text?
}

// ProbeLatency (P5): advisory only. Flags a WARN when first-response time or
// throughput is far worse than a recorded baseline, or when a streamed request
// smells like fake streaming (whole answer in one frame). Never drives a
// verdict alone. Baseline comparison is preserved; when a real TTFT is available
// it is preferred over whole-request latency for the "slow first token" check.
func ProbeLatency(spec model.ProbeSpec, obs LatencyObs) model.ProbeResult {
	res := model.ProbeResult{ProbeKey: "p5_latency", Kind: model.ProbeKindLatency}
	res.Evidence = map[string]any{
		"first_response_ms":       obs.FirstResponseMs,
		"tokens_per_sec":          round4(obs.TokensPerSec),
		"baseline_first_ms":       obs.BaselineFirstMs,
		"baseline_tokens_per_sec": round4(obs.BaselineTokensPerSec),
	}

	// TTFT / inter-token fingerprint — surfaced whenever we have chunk timing.
	streamed := obs.Chunks > 0
	if streamed {
		res.Evidence["ttft_ms"] = obs.FirstChunkMs
		res.Evidence["inter_token_p50_ms"] = round4(obs.InterTokenMsP50)
		res.Evidence["chunks"] = obs.Chunks
	}
	// Fake-streaming tell: stream requested, content present, but it all landed in
	// exactly ONE content frame → the upstream buffered then dumped instead of
	// truly streaming. Gated on obs.Streamed (a non-streamed length probe never
	// qualifies) AND on Chunks==1 specifically: Chunks==0 means we recognised no
	// content frames at all — an unparseable/unknown upstream SSE schema — which
	// degrades to "timing unavailable", never a false fake-stream accusation.
	fakeStream := obs.Streamed && obs.HasContent && obs.Chunks == 1

	if obs.BaselineFirstMs <= 0 && obs.BaselineTokensPerSec <= 0 {
		if fakeStream {
			res.Status = model.ProbeStatusWarn
			res.Evidence["suspected_fake_stream"] = true
			res.Evidence["reason"] = "streamed request delivered its whole answer in a single frame (no real inter-token cadence)"
			return res
		}
		res.Status = model.ProbeStatusSkip
		res.Evidence["reason"] = "no baseline yet"
		return res
	}

	// Prefer real TTFT over whole-request latency when available.
	firstMs := obs.FirstResponseMs
	if obs.FirstChunkMs > 0 {
		firstMs = obs.FirstChunkMs
	}
	slowFirst := obs.BaselineFirstMs > 0 && firstMs > obs.BaselineFirstMs*3
	slowTput := obs.BaselineTokensPerSec > 0 && obs.TokensPerSec > 0 && obs.TokensPerSec < obs.BaselineTokensPerSec*0.5
	if slowFirst || slowTput || fakeStream {
		res.Status = model.ProbeStatusWarn
		if fakeStream {
			res.Evidence["suspected_fake_stream"] = true
		}
	} else {
		res.Status = model.ProbeStatusPass
	}
	return res
}

// SelfReportObs holds inputs for the P8 source-purity / self-report probe.
type SelfReportObs struct {
	RequestedModel string
	Content        string // the model's free-text answer to "who are you?"
}

// vendorProfile ties a big-three vendor to the identity tokens its models use
// when asked who they are.
type vendorProfile struct {
	key  string   // canonical vendor key
	self []string // lowercase tokens that confirm this vendor's identity
}

var bigVendors = []vendorProfile{
	{"anthropic", []string{"claude", "anthropic"}},
	{"openai", []string{"openai", "chatgpt"}},
	{"google", []string{"gemini", "google deepmind", "bard"}},
}

// wrapperMarkers are agent / IDE / aggregator product names. If a "who are you?"
// answer contains one, the channel is almost certainly reselling access through
// that tool's subscription rather than a clean official API — a repackaged
// ("non-pure") source. Lowercase; matched as substrings.
var wrapperMarkers = []string{
	"kiro", "cursor", "cline", "roo code", "roocode", "windsurf",
	"codeium", "github copilot", "copilot", "sourcegraph", "cody",
	"tabnine", "augment", "trae", "amazon q", "q developer",
	"supermaven", "devin", "bolt.new", "lovable", "poe", "phind",
	"you.com", "perplexity",
}

// otherModelNames are self-identifications belonging to non-big-three model
// families. On a channel that requested a big-three model, leaking one of these
// is a swap tell — but self-report text is noisier than the API model echo, so
// it only warns.
var otherModelNames = []string{
	"qwen", "llama", "mistral", "mixtral", "deepseek", "chatglm",
	"glm-", "yi-", "baichuan", "ernie", "doubao", "gemma", "grok",
	"command r", "cohere",
}

// expectedVendor maps a requested model id to its big-three vendor profile, or
// nil when the family has no purity baseline here.
func expectedVendor(modelName string) *vendorProfile {
	n := strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case strings.HasPrefix(n, "claude"):
		return &bigVendors[0]
	case strings.HasPrefix(n, "gpt") || strings.HasPrefix(n, "chatgpt") ||
		strings.HasPrefix(n, "o1") || strings.HasPrefix(n, "o3") || strings.HasPrefix(n, "o4"):
		return &bigVendors[1]
	case strings.HasPrefix(n, "gemini"):
		return &bigVendors[2]
	}
	return nil
}

func containsAny(haystack string, needles []string) (string, bool) {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return n, true
		}
	}
	return "", false
}

// ProbeSelfReport (P8): ask the model who it is and read the answer, not the API
// metadata. The relay rewrites the echoed model id, so P3 (identity echo) can't
// see a Claude subscription served through Kiro/Cursor — but the wrapper's
// injected system prompt makes the model call itself "Kiro", which this probe
// catches. Precedence: a wrong big-vendor claim (swap) FAILs; a wrapper/agent
// name or a foreign model name WARNs (non-pure source); the correct vendor
// PASSes; anything unidentifiable SKIPs (never punish a terse honest reply).
func ProbeSelfReport(spec model.ProbeSpec, obs SelfReportObs) model.ProbeResult {
	res := model.ProbeResult{ProbeKey: "p8_self_report", Kind: model.ProbeKindSelfReport}
	content := strings.TrimSpace(obs.Content)
	snippet := content
	if r := []rune(snippet); len(r) > 240 {
		snippet = string(r[:240]) + "…"
	}
	res.Evidence = map[string]any{"requested": obs.RequestedModel, "reply": snippet}
	if content == "" {
		res.Status = model.ProbeStatusSkip
		res.Evidence["reason"] = "empty self-report reply"
		return res
	}
	lc := strings.ToLower(content)

	vp := expectedVendor(obs.RequestedModel)
	selfConfirmed := false
	if vp != nil {
		res.Evidence["expected_vendor"] = vp.key
		_, selfConfirmed = containsAny(lc, vp.self)
		res.Evidence["self_confirmed"] = selfConfirmed
	}

	// A wrong big-three vendor claim, only when our own vendor isn't also named
	// ("I'm Claude, like ChatGPT" mentions ChatGPT but is honest → not a swap).
	wrongVendor := ""
	if vp != nil && !selfConfirmed {
		for i := range bigVendors {
			if bigVendors[i].key == vp.key {
				continue
			}
			if _, ok := containsAny(lc, bigVendors[i].self); ok {
				wrongVendor = bigVendors[i].key
				break
			}
		}
	}
	marker, hasMarker := containsAny(lc, wrapperMarkers)
	foreignModel, hasForeign := "", false
	if vp != nil && !selfConfirmed && wrongVendor == "" {
		foreignModel, hasForeign = containsAny(lc, otherModelNames)
	}

	switch {
	case wrongVendor != "":
		res.Status = model.ProbeStatusFail
		res.Evidence["claimed_vendor"] = wrongVendor
		res.Evidence["reason"] = "self-report names " + wrongVendor + ", not the requested vendor — possible model swap"
	case hasMarker:
		res.Status = model.ProbeStatusWarn
		res.Evidence["wrapper_marker"] = marker
		res.Evidence["reason"] = "self-report reveals the wrapper/agent \"" + marker + "\" — source is a repackaged subscription, not a clean API"
	case hasForeign:
		res.Status = model.ProbeStatusWarn
		res.Evidence["foreign_model"] = foreignModel
		res.Evidence["reason"] = "self-report names \"" + foreignModel + "\", a different model family than requested"
	case selfConfirmed:
		res.Status = model.ProbeStatusPass
	case vp == nil:
		res.Status = model.ProbeStatusSkip
		res.Evidence["reason"] = "no purity baseline for this model family"
	default:
		res.Status = model.ProbeStatusSkip
		res.Evidence["reason"] = "self-report did not clearly identify a vendor"
	}
	return res
}

// CostAnchorObs holds inputs for the P6 cost-vs-anchor probe (no upstream call).
type CostAnchorObs struct {
	Mode            string
	ResolvedInput   float64
	ResolvedOutput  float64
	ResolvedPerCall float64
	AnchorInput     float64
	AnchorOutput    float64
	AnchorPerCall   float64
	AnchorFound     bool
}

// ProbeCostAnchor (P6): compare the resolved customer price to the official list
// anchor. A discount-only reseller should never price a model ABOVE official
// list; if it does, that's a pricing anomaly the operator should see. No anchor
// for the model → skip. Pure config math; makes no upstream call.
func ProbeCostAnchor(spec model.ProbeSpec, obs CostAnchorObs) model.ProbeResult {
	res := model.ProbeResult{ProbeKey: "p6_cost_anchor", Kind: model.ProbeKindCostAnchor}
	if !obs.AnchorFound {
		res.Status = model.ProbeStatusSkip
		res.Evidence = map[string]any{"reason": "no official anchor for this model"}
		return res
	}
	const tol = 1.02 // 2% slack for rounding
	type cmp struct {
		name             string
		resolved, anchor float64
	}
	var checks []cmp
	if obs.Mode == model.PricingModePerCall {
		checks = []cmp{{"per_call", obs.ResolvedPerCall, obs.AnchorPerCall}}
	} else {
		checks = []cmp{{"input", obs.ResolvedInput, obs.AnchorInput}, {"output", obs.ResolvedOutput, obs.AnchorOutput}}
	}
	overcharge := false
	ev := map[string]any{"mode": obs.Mode}
	for _, c := range checks {
		ev[c.name+"_resolved"] = round4(c.resolved)
		ev[c.name+"_anchor"] = round4(c.anchor)
		if c.anchor > 0 && c.resolved > c.anchor*tol {
			overcharge = true
		}
	}
	res.Evidence = ev
	if overcharge {
		res.Status = model.ProbeStatusFail
		res.Evidence["reason"] = "customer price exceeds official list (markup above anchor)"
	} else {
		res.Status = model.ProbeStatusPass
	}
	return res
}

// --- helpers ---------------------------------------------------------------

var dateMarker = regexp.MustCompile(`-20\d{2}`)

// modelFamily normalises a model id to a coarse family token (e.g.
// "gpt-5-2026-01-01" → "gpt-5", "claude-3-opus-20240229" →
// "claude-opus", "gemini-3.1-pro-preview" → "gemini-3.1"). Heuristic,
// used only for the identity probe's family comparison.
func modelFamily(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, "_", "-")
	// Bedrock inference profiles qualify Anthropic model IDs with a region and
	// provider (for example, "global.anthropic.claude-haiku-4-5-...").  The
	// qualifier describes routing, not a different model family.
	if i := strings.Index(n, "anthropic."); i >= 0 {
		candidate := n[i+len("anthropic."):]
		if strings.HasPrefix(candidate, "claude-") {
			n = candidate
		}
	}
	// Claude IDs have used both "claude-opus-..." and
	// "claude-3-opus-..." forms. The flavor, rather than its position, is the
	// stable family discriminator; generation and dated version tokens are not.
	// Keep this aligned with the desktop engine's model_family behavior.
	if strings.HasPrefix(n, "claude") {
		for _, part := range strings.Split(n, "-") {
			switch part {
			case "opus", "sonnet", "haiku":
				return "claude-" + part
			}
		}
		return "claude"
	}
	if loc := dateMarker.FindStringIndex(n); loc != nil && loc[0] > 0 {
		n = n[:loc[0]]
	}
	for _, sep := range []string{":", "@"} {
		if i := strings.Index(n, sep); i > 0 {
			n = n[:i]
		}
	}
	parts := strings.Split(n, "-")
	if len(parts) >= 2 {
		return parts[0] + "-" + parts[1]
	}
	return n
}

func sameModelFamily(a, b string) bool {
	return modelFamily(a) == modelFamily(b)
}

// MatchGolden reports whether a golden item's expectation is satisfied by the
// model's answer. ExpectRegex (if set) is matched case-as-authored against the
// trimmed answer; otherwise ExpectExact must appear (case-insensitive substring)
// in the answer. An item with neither expectation is treated as "any non-empty
// answer passes".
func MatchGolden(item model.GoldenItem, got string) bool {
	g := strings.TrimSpace(got)
	if item.ExpectRegex != "" {
		re, err := regexp.Compile(item.ExpectRegex)
		if err != nil {
			return false
		}
		return re.MatchString(g)
	}
	if item.ExpectExact != "" {
		return strings.Contains(strings.ToLower(g), strings.ToLower(strings.TrimSpace(item.ExpectExact)))
	}
	return g != ""
}

func valOr(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
