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
	"unicode"
	"unicode/utf8"

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
	// ReasoningTokens is the portion of CompletionTokens the provider spent on
	// internal reasoning/thinking that never appears in the returned text. It is
	// billed but not recountable, so it must be subtracted before comparing the
	// completion count to a recount of the visible answer. OpenAI reports it in
	// completion_tokens_details.reasoning_tokens, Anthropic in
	// output_tokens_details.thinking_tokens, Gemini in thoughtsTokenCount.
	ReasoningTokens int
	// ReasoningReported distinguishes "provider says zero" from "provider does
	// not expose the field".
	ReasoningReported bool
	// ReasoningCapable is true only for model families that can actually spend
	// unreturned reasoning tokens (o-series/GPT-5, Claude thinking, Gemini
	// thoughts). It gates the benefit of the doubt: on a model that cannot
	// reason, a large completion-vs-text gap has no innocent explanation, so
	// missing telemetry must not launder padding into a mere WARN.
	ReasoningCapable bool
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
	// Reasoning tokens are billed output that is deliberately not returned, so
	// they can never be recounted from the text. Compare only the visible part.
	visibleTokens := obs.CompletionTokens - obs.ReasoningTokens
	if visibleTokens < 0 {
		visibleTokens = 0
	}
	ratio := float64(visibleTokens) / float64(obs.LocalRecount)
	tol := valOr(spec.TolerancePct, 25) / 100
	res.Evidence = map[string]any{
		"completion_tokens":  obs.CompletionTokens,
		"reasoning_tokens":   obs.ReasoningTokens,
		"reasoning_reported": obs.ReasoningReported,
		"reasoning_capable":  obs.ReasoningCapable,
		"visible_tokens":     visibleTokens,
		"local_recount":      obs.LocalRecount,
		"ratio":              round4(ratio),
		"content_ok":         obs.ContentOK,
		"openai_family":      obs.IsOpenAIFamily,
	}
	if obs.IsOpenAIFamily {
		res.Evidence["comparison_confidence"] = "exact_tokenizer_family"
	} else {
		res.Evidence["comparison_confidence"] = "estimated_cross_tokenizer"
	}
	// On a reasoning-capable model that did not report its reasoning split, an
	// unexplained gap is genuinely ambiguous — undisclosed thinking looks exactly
	// like padding. Say "inconclusive" rather than convict. A model that cannot
	// reason gets no such excuse.
	unexplainedReasoning := ratio > 1+tol && obs.ReasoningCapable && !obs.ReasoningReported && obs.ReasoningTokens == 0
	switch {
	case unexplainedReasoning:
		res.Status = model.ProbeStatusWarn
		res.Evidence["reason_code"] = "completion_gap_without_reasoning_telemetry"
	case ratio > 1+tol && obs.IsOpenAIFamily:
		res.Status = model.ProbeStatusFail // server-side padding of completion tokens
		res.Evidence["reason_code"] = "completion_padding"
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
	{"google", []string{"gemini", "google deepmind", "google bard", "google"}},
}

// wrapperMarkers are agent / IDE / aggregator product names. If a "who are you?"
// answer contains one, the channel is almost certainly reselling access through
// that tool's subscription rather than a clean official API — a repackaged
// ("non-pure") source. Lowercase; matched as independent words/phrases.
var wrapperMarkers = []string{
	"kiro", "cursor", "cline", "roo code", "roocode", "windsurf",
	"codeium", "github copilot", "microsoft copilot", "copilot", "sourcegraph", "cody",
	"tabnine", "augment", "trae", "amazon q", "q developer",
	"supermaven", "devin", "bolt.new", "lovable", "poe", "phind",
	"you.com", "perplexity", "antigravity",
}

// These product names are also ordinary English words or personal names. A
// bare occurrence is not enough; require self-identification or hosting/tool
// context before treating it as a wrapper disclosure.
var ambiguousWrapperMarkers = map[string]bool{
	"cursor": true, "windsurf": true, "copilot": true, "cody": true,
	"augment": true, "trae": true, "devin": true, "lovable": true,
	"poe": true, "perplexity": true, "antigravity": true,
}

const redactedSelfReport = "[redacted upstream self-report]"

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

// containsBoundedMarker matches product names only when the runes immediately
// outside the marker are not part of a word. This keeps short product names
// such as "cline" and "poe" useful without treating ordinary words such as
// "decline" and "poetry" as wrapper disclosures. Boundary checks operate on
// Unicode runes so non-ASCII letters cannot accidentally create a boundary.
func containsBoundedMarker(haystack string, markers []string) (string, bool) {
	for _, marker := range markers {
		if _, found := boundedMarkerIndex(haystack, marker); found {
			return marker, true
		}
	}
	return "", false
}

func boundedMarkerIndex(haystack, marker string) (int, bool) {
	indices := boundedMarkerIndices(haystack, marker)
	if len(indices) == 0 {
		return 0, false
	}
	return indices[0], true
}

func boundedMarkerIndices(haystack, marker string) []int {
	indices := make([]int, 0, 1)
	searchFrom := 0
	for searchFrom <= len(haystack) {
		rel := strings.Index(haystack[searchFrom:], marker)
		if rel < 0 {
			break
		}
		start := searchFrom + rel
		end := start + len(marker)
		leftBoundary := start == 0 || !isMarkerWordRune(previousRune(haystack[:start]))
		rightBoundary := end == len(haystack) || !isMarkerWordRune(nextRune(haystack[end:]))
		if leftBoundary && rightBoundary {
			indices = append(indices, start)
		}
		searchFrom = start + 1
	}
	return indices
}

func isMarkerWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_'
}

func previousRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}

func nextRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func detectSelfReportWrapper(haystack string) (string, bool) {
	for _, marker := range wrapperMarkers {
		for _, start := range boundedMarkerIndices(haystack, marker) {
			if ambiguousWrapperMarkers[marker] && !hasWrapperContext(haystack, marker, start) {
				continue
			}
			return marker, true
		}
	}
	return "", false
}

func detectIdentityClaim(haystack string, markers []string) (string, bool) {
	normalizedReply := normalizeMarkerContext(haystack)
	for _, marker := range markers {
		if normalizedReply == normalizeMarkerContext(marker) {
			return marker, true
		}
		for _, start := range boundedMarkerIndices(haystack, marker) {
			before := normalizeMarkerContext(haystack[:start])
			for _, prefix := range []string{
				"i am", "i'm", "i am a", "i am an", "i'm a", "i'm an", "we are",
				"this is", "my name is", "my model is", "my underlying model is",
				"this model is", "this assistant is", "created by", "developed by",
				"built by", "made by", "trained by", "provided by", "offered by",
				"powered by", "from", "a model from", "an assistant from",
			} {
				if identityClaimPrefixMatches(before, prefix) {
					return marker, true
				}
			}
		}
	}
	return "", false
}

func identityClaimPrefixMatches(before, prefix string) bool {
	if before == prefix {
		return true
	}
	if !strings.HasSuffix(before, " "+prefix) {
		return false
	}
	preceding := strings.TrimSpace(strings.TrimSuffix(before, " "+prefix))
	fields := strings.Fields(preceding)
	if len(fields) > 3 {
		fields = fields[len(fields)-3:]
	}
	for _, field := range fields {
		switch field {
		case "not", "never", "neither", "no":
			return false
		}
	}
	return true
}

func hasWrapperContext(haystack, marker string, start int) bool {
	before := normalizeMarkerContext(haystack[:start])
	after := normalizeMarkerContext(haystack[start+len(marker):])
	for _, prefix := range []string{
		"i am", "i'm", "we are", "inside", "within", "via", "through", "using",
		"running in", "running inside", "running on", "accessed via", "accessed through",
		"powered by", "served by", "hosted by", "inside the", "within the", "via the",
		"through the", "using the", "running in the", "running inside the", "running on the",
	} {
		if before == prefix || strings.HasSuffix(before, " "+prefix) {
			return true
		}
	}
	for _, suffix := range []string{
		"ide", "editor", "platform", "app", "application", "service", "subscription",
		"tool", "environment", "client",
	} {
		if after == suffix || strings.HasPrefix(after, suffix+" ") {
			return true
		}
	}
	return false
}

func normalizeMarkerContext(value string) string {
	value = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			return unicode.ToLower(r)
		case r == '\'', r == '’':
			return '\''
		default:
			return ' '
		}
	}, value)
	return strings.Join(strings.Fields(value), " ")
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
	res.Evidence = map[string]any{
		"requested":   obs.RequestedModel,
		"reply":       redactedSelfReport,
		"reply_chars": len([]rune(content)),
	}
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
		_, selfConfirmed = detectIdentityClaim(lc, vp.self)
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
			if _, ok := detectIdentityClaim(lc, bigVendors[i].self); ok {
				wrongVendor = bigVendors[i].key
				break
			}
		}
	}
	marker, hasMarker := detectSelfReportWrapper(lc)
	foreignModel, hasForeign := "", false
	if vp != nil && !selfConfirmed && wrongVendor == "" {
		foreignModel, hasForeign = detectIdentityClaim(lc, otherModelNames)
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

var (
	versionSuffix       = regexp.MustCompile(`-v\d+$`)
	bedrockRevision     = regexp.MustCompile(`-v\d+:\d+$`)
	compactDateSuffix   = regexp.MustCompile(`-(20\d{6})$`)
	dashedDateSuffix    = regexp.MustCompile(`-(20\d{2}-\d{2}-\d{2})$`)
	previewSuffix       = regexp.MustCompile(`-preview(?:-(?:\d{2}-\d{2}|20\d{6}|20\d{2}-\d{2}-\d{2}))?$`)
	geminiVersionSuffix = regexp.MustCompile(`-(\d{3})$`)
	snapshotToken       = regexp.MustCompile(`^(?:20\d{6}|20\d{2}-\d{2}-\d{2})$`)
)

type canonicalModelIdentity struct {
	base     string
	snapshot string
}

var bedrockAnthropicPrefixes = []string{
	"global.anthropic.", "us.anthropic.", "eu.anthropic.", "apac.anthropic.",
	"global-anthropic.", "us-anthropic.", "eu-anthropic.", "apac-anthropic.",
}

// modelFamily returns a canonical identity containing both the model generation
// and its service tier/variant. Snapshot dates, preview labels and Bedrock
// routing qualifiers are deployment metadata, while tokens such as Claude's
// generation, Gemini's pro/flash tier and GPT's mini tier are identity-bearing.
// Keep this aligned with the desktop engine's model_family behavior.
func modelFamily(name string) string {
	identity := parseModelIdentity(name)
	if identity.snapshot != "" {
		return identity.base + "@" + identity.snapshot
	}
	return identity.base
}

func parseModelIdentity(name string) canonicalModelIdentity {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, "_", "-")
	n = strings.TrimPrefix(n, "models/")
	// Bedrock inference profiles qualify Anthropic model IDs with a region and
	// provider (for example, "global.anthropic.claude-haiku-4-5-...").  The
	// qualifier describes routing, not a different model family.
	bedrockQualified := false
	for _, prefix := range bedrockAnthropicPrefixes {
		candidate, matched := strings.CutPrefix(n, prefix)
		if !matched {
			continue
		}
		if strings.HasPrefix(candidate, "claude-") {
			n = candidate
			bedrockQualified = true
			break
		}
	}
	// OpenAI fine-tuned deployment IDs are opaque identifiers. Their job name
	// may legitimately end in a date- or preview-shaped token, so no generic
	// snapshot normalization is safe.
	if strings.HasPrefix(n, "ft:") {
		return canonicalModelIdentity{base: n}
	}

	// Strip only known Claude provider metadata. Treat arbitrary :/@/-vN
	// suffixes as identity-bearing so another family cannot exploit a broad
	// truncation rule (for example, gpt-5:mini must not equal gpt-5).
	if strings.HasPrefix(n, "claude-") {
		switch {
		case bedrockRevision.MatchString(n):
			n = bedrockRevision.ReplaceAllString(n, "")
		case bedrockQualified:
			n = versionSuffix.ReplaceAllString(n, "")
		}
		if i := strings.LastIndex(n, "@"); i > 0 && snapshotToken.MatchString(n[i+1:]) {
			n = n[:i]
		}
	}

	// Preview is a deployment channel label only when it is a complete suffix;
	// strings such as "preview2" are identity-bearing and must remain intact.
	previewStripped := false
	if strings.HasPrefix(n, "gemini-") {
		withoutPreview := previewSuffix.ReplaceAllString(n, "")
		previewStripped = withoutPreview != n
		n = withoutPreview
	}
	metadataStripped := previewStripped
	snapshot := ""
	if !previewStripped {
		if match := compactDateSuffix.FindStringIndex(n); match != nil {
			n = n[:match[0]]
			metadataStripped = true
		} else if match := dashedDateSuffix.FindStringIndex(n); match != nil {
			n = n[:match[0]]
			metadataStripped = true
		} else if strings.HasPrefix(n, "gemini-") {
			// Google exposes three-digit immutable Gemini revisions. An unpinned
			// alias may resolve to one, but two explicitly different revisions are
			// not the same requested model identity.
			if match := geminiVersionSuffix.FindStringSubmatchIndex(n); match != nil {
				snapshot = n[match[2]:match[3]]
				n = n[:match[0]]
				metadataStripped = true
			}
		}
	}
	if !metadataStripped {
		n = strings.TrimSuffix(n, "-latest")
	}

	// Claude has used both generation-first (claude-3-haiku) and flavor-first
	// (claude-haiku-4-5) spellings. Canonicalize ordering without dropping the
	// generation: 3/haiku and 4-5/haiku are deliberately different identities.
	if strings.HasPrefix(n, "claude-") {
		parts := strings.Split(strings.TrimPrefix(n, "claude-"), "-")
		flavor := ""
		versions := make([]string, 0, 2)
		modifiers := make([]string, 0, 1)
		for _, part := range parts {
			switch part {
			case "opus", "sonnet", "haiku":
				flavor = part
			default:
				if isNumericVersionPart(part) {
					versions = append(versions, part)
				} else if part != "" {
					modifiers = append(modifiers, part)
				}
			}
		}
		canonical := []string{"claude"}
		canonical = append(canonical, versions...)
		if flavor != "" {
			canonical = append(canonical, flavor)
		}
		canonical = append(canonical, modifiers...)
		n = strings.Join(canonical, "-")
	}
	return canonicalModelIdentity{base: n, snapshot: snapshot}
}

func isNumericVersionPart(part string) bool {
	if part == "" {
		return false
	}
	hasDigit := false
	for _, r := range part {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
		if r >= '0' && r <= '9' {
			hasDigit = true
		}
	}
	return hasDigit
}

func sameModelFamily(a, b string) bool {
	left, right := parseModelIdentity(a), parseModelIdentity(b)
	if left.base != right.base {
		return false
	}
	return left.snapshot == "" || right.snapshot == "" || left.snapshot == right.snapshot
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
