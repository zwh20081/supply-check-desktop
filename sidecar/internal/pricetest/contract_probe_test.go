package pricetest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"supply-check-sdk/internal/model"
)

func TestProbeProtocolContract(t *testing.T) {
	pass := ProbeProtocolContract(ProtocolContractObs{Endpoint: "/v1/chat/completions", HTTPStatus: 200, ResponseFormat: "json", ProtocolValid: true})
	require.Equal(t, model.ProbeStatusPass, pass.Status)
	fail := ProbeProtocolContract(ProtocolContractObs{HTTPStatus: 200, ResponseFormat: "json", ProtocolValid: false})
	require.Equal(t, model.ProbeStatusFail, fail.Status)
	require.Equal(t, "malformed_response_envelope", fail.Evidence["reason_code"])
	unsupported := ProbeProtocolContract(ProtocolContractObs{})
	require.Equal(t, model.ProbeStatusSkip, unsupported.Status)
}

func TestProbeProtocolContractEvidenceIsBoundedAndRedacted(t *testing.T) {
	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	result := ProbeProtocolContract(ProtocolContractObs{
		Endpoint:   "https://vendor.example/v1?api_key=" + secret,
		HTTPStatus: 200, ResponseFormat: "json", ProtocolValid: true,
		ContentType: "application/json; note=" + secret + strings.Repeat("x", 300),
	})
	evidence := result.Evidence
	require.NotContains(t, evidence["endpoint"], secret)
	require.NotContains(t, evidence["content_type"], secret)
	require.LessOrEqual(t, len(evidence["content_type"].(string)), 200)
}

func TestProbeStreamIntegritySeparatesUnsupportedFromMalformed(t *testing.T) {
	unsupported := ProbeStreamIntegrity(StreamIntegrityObs{Requested: true, ProtocolValid: true, DataFrames: 2})
	require.Equal(t, model.ProbeStatusSkip, unsupported.Status)
	require.Equal(t, "terminal_marker_unsupported", unsupported.Evidence["reason_code"])

	pass := ProbeStreamIntegrity(StreamIntegrityObs{Requested: true, ProtocolValid: true, DataFrames: 3, TerminalObserved: true, HasContent: true})
	require.Equal(t, model.ProbeStatusPass, pass.Status)
	require.Equal(t, "unsupported", pass.Evidence["usage_capability"], "missing final usage is capability evidence, not a failure")

	fail := ProbeStreamIntegrity(StreamIntegrityObs{Requested: true, InvalidFrames: 1})
	require.Equal(t, model.ProbeStatusFail, fail.Status)
	require.Equal(t, "malformed_sse", fail.Evidence["reason_code"])
}

func TestProbeUsageReconciliationRejectsOnlyReportedContradictions(t *testing.T) {
	unsupported := ProbeUsageReconciliation(UsageReconciliationObs{PromptTokens: 9, TotalTokens: 9})
	require.Equal(t, model.ProbeStatusSkip, unsupported.Status)

	pass := ProbeUsageReconciliation(UsageReconciliationObs{Reported: true, PromptTokens: 9, CompletionTokens: 2, TotalTokens: 11, CachedTokens: 4, CostQuota: 3})
	require.Equal(t, model.ProbeStatusPass, pass.Status)

	cachedAnomaly := ProbeUsageReconciliation(UsageReconciliationObs{Reported: true, PromptTokens: 9, CompletionTokens: 2, TotalTokens: 11, CachedTokens: 10})
	require.Equal(t, model.ProbeStatusFail, cachedAnomaly.Status)
	require.Equal(t, "cached_tokens_exceed_prompt", cachedAnomaly.Evidence["reason_code"])

	anthropic := ProbeUsageReconciliation(UsageReconciliationObs{
		Reported: true, PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3,
		CachedTokens: 4354, CacheCreationTokens: 4218, CacheTokensSeparate: true,
	})
	require.Equal(t, model.ProbeStatusPass, anthropic.Status)

	totalAnomaly := ProbeUsageReconciliation(UsageReconciliationObs{Reported: true, PromptTokens: 9, CompletionTokens: 2, TotalTokens: 12})
	require.Equal(t, model.ProbeStatusFail, totalAnomaly.Status)
	require.Equal(t, "total_tokens_mismatch", totalAnomaly.Evidence["reason_code"])
}

func TestContractProbeScoreAndVerdictSemantics(t *testing.T) {
	unsupported := []model.ProbeResult{
		ProbeProtocolContract(ProtocolContractObs{HTTPStatus: 200, ResponseFormat: "json", ProtocolValid: true}),
		ProbeUsageReconciliation(UsageReconciliationObs{}),
	}
	score, verdict := Score(unsupported, map[string]int{
		model.ProbeKindProtocolContract:    50,
		model.ProbeKindUsageReconciliation: 50,
	})
	require.Equal(t, 100, score, "zero-scoring contract probes ignore legacy/admin positive weights")
	require.Equal(t, model.ProbeVerdictOK, verdict)

	anomaly := []model.ProbeResult{
		ProbeProtocolContract(ProtocolContractObs{HTTPStatus: 200, ResponseFormat: "json", ProtocolValid: true}),
		ProbeUsageReconciliation(UsageReconciliationObs{Reported: true, PromptTokens: 2, CachedTokens: 3, TotalTokens: 2}),
	}
	score, verdict = Score(anomaly, nil)
	require.Equal(t, 100, score)
	require.Equal(t, model.ProbeVerdictSuspicious, verdict, "a reported accounting contradiction is visible even at zero numeric weight")
}

func TestProbeCancellationContractIsPassiveAndStrictWhenObserved(t *testing.T) {
	unsupported := ProbeCancellationContract(CancellationContractObs{})
	require.Equal(t, model.ProbeStatusSkip, unsupported.Status)
	require.Equal(t, "passive_carrier_unavailable", unsupported.Evidence["reason_code"])

	pass := ProbeCancellationContract(CancellationContractObs{CarrierObserved: true, ContextBound: true})
	require.Equal(t, model.ProbeStatusPass, pass.Status)

	fail := ProbeCancellationContract(CancellationContractObs{CarrierObserved: true})
	require.Equal(t, model.ProbeStatusFail, fail.Status)
	require.Equal(t, "transport_context_not_bound", fail.Evidence["reason_code"])
}

func TestProbeToolSchemaFidelitySeparatesUnsupportedFromMalformed(t *testing.T) {
	unsupported := ProbeToolSchemaFidelity(ToolSchemaFidelityObs{})
	require.Equal(t, model.ProbeStatusSkip, unsupported.Status)

	pass := ProbeToolSchemaFidelity(ToolSchemaFidelityObs{
		ToolCallObserved: true, ToolName: "healthcheck_echo", ArgumentsValidJSON: true, SchemaMatched: true,
	})
	require.Equal(t, model.ProbeStatusPass, pass.Status)

	malformed := ProbeToolSchemaFidelity(ToolSchemaFidelityObs{
		ToolCallObserved: true, ToolName: "healthcheck_echo",
	})
	require.Equal(t, model.ProbeStatusFail, malformed.Status)
	require.Equal(t, "tool_arguments_malformed", malformed.Evidence["reason_code"])
}

func TestProbeRateLimitContractIsPassiveAndValidatesRetryAfter(t *testing.T) {
	unsupported := ProbeRateLimitContract(RateLimitContractObs{HTTPStatus: 429})
	require.Equal(t, model.ProbeStatusSkip, unsupported.Status, "a natural 429 without contract headers is unsupported, not guilt")

	pass := ProbeRateLimitContract(RateLimitContractObs{HTTPStatus: 200, Headers: map[string]string{
		"X-RateLimit-Remaining-Requests": "9", "Retry-After": "0",
	}})
	require.Equal(t, model.ProbeStatusPass, pass.Status)
	require.Equal(t, true, pass.Evidence["retry_after_valid"])

	fail := ProbeRateLimitContract(RateLimitContractObs{HTTPStatus: 429, Headers: map[string]string{
		"Retry-After": "not-a-delay",
	}})
	require.Equal(t, model.ProbeStatusFail, fail.Status)
	require.Equal(t, "retry_after_malformed", fail.Evidence["reason_code"])
}

func TestP13P15RemainNeutralWithStalePositiveWeights(t *testing.T) {
	results := []model.ProbeResult{
		ProbeCancellationContract(CancellationContractObs{}),
		ProbeRateLimitContract(RateLimitContractObs{}),
	}
	score, verdict := Score(results, map[string]int{
		model.ProbeKindCancellationContract: 100,
		model.ProbeKindRateLimitContract:    100,
	})
	require.Equal(t, 100, score)
	require.Equal(t, model.ProbeVerdictInconclusive, verdict)
}
