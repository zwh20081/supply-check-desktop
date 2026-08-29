package model

// Probe contract: the definitions, statuses and verdicts shared by the probe
// implementations, the scorer and the PDF renderer.

// Probe kinds. Each maps to one function in internal/pricetest/probes.go.
const (
	ProbeKindTokenCount = "token_count" // P1: upstream prompt_tokens vs local recount
	ProbeKindLength     = "length"      // P2: max_tokens=N determinism / completion inflation
	ProbeKindIdentity   = "identity"    // P3: returned model / system_fingerprint vs requested
	ProbeKindGolden     = "golden"      // P4: deterministic golden Q&A pass-rate
	ProbeKindLatency    = "latency"     // P5: first-response + tokens/sec vs baseline
	ProbeKindCostAnchor = "cost_anchor" // P6: resolved customer price vs official anchor (no upstream call)
	// P7 v2 splits cache telemetry/accounting from freshness integrity. A legal
	// prompt-cache hit is a PASS, while contradictory token accounting and a
	// replayed stale answer are separate auditable failures.
	ProbeKindCacheAccounting      = "cache_accounting"
	ProbeKindFreshnessIntegrity   = "freshness_integrity"
	ProbeKindProviderCacheControl = "provider_cache_control"
	ProbeKindSelfReport           = "self_report" // P8: ask the model who it is — reveals wrapper/agent repackaging (Kiro/Cursor/…) or a vendor swap the API echo hides
	// P10-P12 are zero-cost ride-along contracts. They reuse the ordinary JSON
	// and streaming requests already issued by P1/P2 and remain non-scoring by
	// default until an operator explicitly adopts them as a policy signal.
	ProbeKindProtocolContract    = "protocol_contract"
	ProbeKindStreamIntegrity     = "stream_integrity"
	ProbeKindUsageReconciliation = "usage_reconciliation"
	// P13/P15 are passive ride-along contracts. P14 is an explicit opt-in
	// request because forcing a tool call can consume additional upstream quota.
	ProbeKindCancellationContract = "cancellation_contract"
	ProbeKindToolSchemaFidelity   = "tool_schema_fidelity"
	ProbeKindRateLimitContract    = "rate_limit_contract"
	// P16-P19 are relay-security probes adapted from the audit methods in
	// github.com/toby-bridges/api-relay-audit. They intentionally exercise
	// adversarial prompt paths. See NOTICE for attribution.
	ProbeKindPromptLeakage     = "prompt_leakage"
	ProbeKindInstructionPolicy = "instruction_policy"
	ProbeKindToolSubstitution  = "tool_substitution"
	ProbeKindContextIntegrity  = "context_integrity"
	// P20 folds the former standalone channel-purity sampler into the durable
	// health-check suite so it shares job progress, evidence, history and PDF
	// reporting with every other test item.
	ProbeKindChannelPurity = "channel_purity"
	// P21 measures long-context prompt-cache effectiveness across several
	// rotated prompts. It is non-scoring telemetry: cache availability affects
	// cost/latency, not the authenticity verdict.
	ProbeKindCacheRate = "cache_rate"
)

// Pricing modes the cost-anchor probe compares against.
const (
	PricingModeToken   = "token"
	PricingModePerCall = "per_call"
)

// Probe result statuses. error == inconclusive (network/timeout) and is never
// penalised; skip == not applicable (no anchor / no echo) and is neutral.
const (
	ProbeStatusPass  = "pass"
	ProbeStatusWarn  = "warn"
	ProbeStatusFail  = "fail"
	ProbeStatusSkip  = "skip"
	ProbeStatusError = "error"
)

// Per-channel verdicts derived from the trust score (see service/pricetest/score.go).
const (
	ProbeVerdictOK           = "OK"           // 清白
	ProbeVerdictSuspicious   = "SUSPICIOUS"   // 可疑
	ProbeVerdictWatered      = "WATERED"      // 兑水
	ProbeVerdictInconclusive = "INCONCLUSIVE" // 未测出:上游全程报错/不适用,啥也没测到——不是清白
)

// GoldenItem is one deterministic golden Q&A item (P4). The model answer is
// matched exactly (after trimming) against ExpectExact, or against ExpectRegex
// when set.
type GoldenItem struct {
	Prompt      string `json:"prompt"`
	ExpectExact string `json:"expect_exact,omitempty"`
	ExpectRegex string `json:"expect_regex,omitempty"`
}

// ProbeSpec is the frozen input for one probe. Optional fields are pointers so
// "unset" is distinguishable from a zero value (AGENTS.md pointer-DTO rule).
type ProbeSpec struct {
	Prompt       string             `json:"prompt,omitempty"`
	MaxTokens    int                `json:"max_tokens,omitempty"`
	Stream       bool               `json:"stream,omitempty"`
	ExpectTokens *int               `json:"expect_tokens,omitempty"`
	TolerancePct *float64           `json:"tolerance_pct,omitempty"`
	FailPct      *float64           `json:"fail_pct,omitempty"`
	GoldenItems  []GoldenItem       `json:"golden_items,omitempty"`
	FamilyBands  map[string]float64 `json:"family_bands,omitempty"` // model-family → P1 tolerance band
	// GoldenGenerators, when set, makes the golden battery generate a fresh
	// randomized set of items on every run (defeats fixed-prompt whitelisting +
	// answer caching). Values are generator kinds from service/pricetest/golden.go
	// (arithmetic / string_op / unit_convert). GoldenCount is how many to draw
	// (defaults to len(GoldenItems) or 4). When GoldenGenerators is empty the
	// static GoldenItems are used verbatim (backward compatible).
	GoldenGenerators []string `json:"golden_generators,omitempty"`
	GoldenCount      int      `json:"golden_count,omitempty"`
	// ContextChars controls the bounded long-context payload used by the opt-in
	// context-integrity probe. It is clamped by the runner.
	ContextChars int `json:"context_chars,omitempty"`
	// SampleCount controls repeated observations for channel purity and rotated
	// prompt count for cache-rate analysis. Each runner applies its own bounds.
	SampleCount int `json:"sample_count,omitempty"`
	// LoopCount controls repeated warm passes for cache-rate analysis. The
	// runner bounds it so a saved definition cannot create an unbounded job.
	LoopCount int `json:"loop_count,omitempty"`
}

type ProbeResult struct {
	ProbeKey  string         `json:"probe_key"`
	Kind      string         `json:"kind"`
	Status    string         `json:"status"`
	Evidence  map[string]any `json:"evidence,omitempty"`
	LatencyMs int64          `json:"latency_ms,omitempty"`
}

// ProbeResultList is a JSON-TEXT array of ProbeResult.
type ProbeResultList []ProbeResult

type ProbeDefinition struct {
	Id          int       `json:"id"`
	Key         string    `json:"key"`
	DisplayName string    `json:"display_name"`
	Kind        string    `json:"kind"`
	Enabled     bool      `json:"enabled"`
	Weight      int       `json:"weight"`
	Spec        ProbeSpec `json:"spec"`
	Version     string    `json:"version"`
	CreatedAt   int64     `json:"created_at"`
	UpdatedAt   int64     `json:"updated_at"`
}

// ProbeRun is one execution of a suite against one (channel, model).
type ProbeRun struct {
	Id             int             `json:"id"`
	ChannelId      int             `json:"channel_id"`
	ChannelName    string          `json:"channel_name"`
	Model          string          `json:"model"`
	PriceGroupID   int             `json:"price_group_id"`
	ChannelGroupID int             `json:"channel_group_id"`
	TrustScore     int             `json:"trust_score"`
	Verdict        string          `json:"verdict"`
	Results        ProbeResultList `json:"results"`
	UpstreamCost   int             `json:"upstream_cost"` // quota units this run cost the system ledger
	TriggeredBy    int             `json:"triggered_by"`
	CreatedAt      int64           `json:"created_at"`
}
