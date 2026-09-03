package pricetest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"supply-check-sdk/internal/model"
	"supply-check-sdk/protocol"
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

func TestQuality_ToolSchemaDiagnosticsClassifyMissingWrongValueAndExtraFields(t *testing.T) {
	tests := []struct {
		name            string
		raw             string
		reason          string
		diagnosticField string
		diagnosticValue []string
	}{
		{name: "missing", raw: `{}`, reason: "tool_schema_missing_required", diagnosticField: "missing_fields", diagnosticValue: []string{"value"}},
		{name: "wrong value", raw: `{"value":"probe-no"}`, reason: "tool_schema_wrong_value", diagnosticField: "wrong_value_fields", diagnosticValue: []string{"value"}},
		{name: "extra", raw: `{"value":"probe-ok","unexpected":"sk-supersecret123456"}`, reason: "tool_schema_extra_fields", diagnosticField: "extra_fields", diagnosticValue: []string{sanitizedToolFieldName("unexpected")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ProbeToolSchemaFidelity(ToolSchemaFidelityObs{
				ToolCallObserved: true, ToolName: "healthcheck_echo", ArgumentsCaptured: true,
				ArgumentsRaw: []byte(test.raw), ArgumentsValidJSON: true,
			})
			require.Equal(t, model.ProbeStatusFail, result.Status)
			require.Equal(t, test.reason, result.Evidence["reason_code"])
			diagnostics := result.Evidence["schema_diagnostics"].(map[string]any)
			require.Equal(t, test.diagnosticValue, diagnostics[test.diagnosticField])
			encoded, err := json.Marshal(result.Evidence)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "probe-no")
			require.NotContains(t, string(encoded), "sk-supersecret")
		})
	}
}

func TestQuality_ToolSchemaEvidenceContainsExpectedContractAndSanitizedActual(t *testing.T) {
	result := ProbeToolSchemaFidelity(ToolSchemaFidelityObs{
		ToolCallObserved: true, ToolName: "healthcheck_echo", ArgumentsCaptured: true,
		ArgumentsRaw: []byte(`{"value":"probe-ok"}`), ArgumentsValidJSON: true, SchemaMatched: true,
	})
	require.Equal(t, model.ProbeStatusPass, result.Status)

	expected := result.Evidence["expected_contract"].(map[string]any)
	require.Equal(t, "healthcheck_echo", expected["tool_name"])
	arguments := expected["arguments"].(map[string]any)
	require.Equal(t, []string{"value"}, arguments["required"])
	require.Equal(t, false, arguments["additional_properties"])

	actual := result.Evidence["actual_arguments"].(map[string]any)
	require.Equal(t, "object", actual["root_type"])
	fields := actual["fields"].(map[string]any)
	value := fields["value"].(map[string]any)
	require.Equal(t, "probe-ok", value["value"])
	require.Equal(t, true, result.Evidence["provider_signal_consistent"])
}

func TestQuality_ToolSchemaEvidenceRedactsSecretFieldNames(t *testing.T) {
	secretField := "sk-field-secret123456"
	raw, err := json.Marshal(map[string]any{"value": "probe-ok", secretField: "also-secret"})
	require.NoError(t, err)
	result := ProbeToolSchemaFidelity(ToolSchemaFidelityObs{
		ToolCallObserved: true, ToolName: "healthcheck_echo", ArgumentsCaptured: true,
		ArgumentsRaw: raw, ArgumentsValidJSON: true,
	})
	require.Equal(t, "tool_schema_extra_fields", result.Evidence["reason_code"])
	diagnostics := result.Evidence["schema_diagnostics"].(map[string]any)
	extraFields := diagnostics["extra_fields"].([]string)
	require.Len(t, extraFields, 1)
	require.Equal(t, 1, diagnostics["extra_field_count"])
	require.Regexp(t, `^field_sha256_[0-9a-f]{12}$`, extraFields[0])

	encoded, err := json.Marshal(result.Evidence)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), secretField)
	require.NotContains(t, string(encoded), "also-secret")
}

func TestQuality_ToolSchemaDiagnosticsDistinguishWrongTypeAndRootType(t *testing.T) {
	wrongType := ProbeToolSchemaFidelity(ToolSchemaFidelityObs{
		ToolCallObserved: true, ToolName: "healthcheck_echo", ArgumentsCaptured: true,
		ArgumentsRaw: []byte(`{"value":42}`), ArgumentsValidJSON: true,
	})
	require.Equal(t, "tool_schema_wrong_type", wrongType.Evidence["reason_code"])
	diagnostics := wrongType.Evidence["schema_diagnostics"].(map[string]any)
	require.Equal(t, []string{"value"}, diagnostics["wrong_type_fields"])

	wrongRoot := ProbeToolSchemaFidelity(ToolSchemaFidelityObs{
		ToolCallObserved: true, ToolName: "healthcheck_echo", ArgumentsCaptured: true,
		ArgumentsRaw: []byte(`["probe-ok"]`), ArgumentsValidJSON: true,
	})
	require.Equal(t, "tool_arguments_not_object", wrongRoot.Evidence["reason_code"])
	diagnostics = wrongRoot.Evidence["schema_diagnostics"].(map[string]any)
	require.Equal(t, []string{"root_type_mismatch"}, diagnostics["issue_codes"])
}

func TestQuality_ToolArgumentsRawNeverCrossesProtocolJSONBoundary(t *testing.T) {
	secret := `{"value":"sk-supersecret123456"}`
	encoded, err := json.Marshal(protocol.Observation{
		ToolCallObserved: true, ToolArgumentsCaptured: true, ToolArgumentsRaw: []byte(secret),
	})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "sk-supersecret")
	require.NotContains(t, string(encoded), "toolArgumentsRaw")
	require.Contains(t, string(encoded), `"toolArgumentsCaptured":true`)
}

func TestQuality_ObservedBlankToolNameIsNameMismatch(t *testing.T) {
	result := ProbeToolSchemaFidelity(ToolSchemaFidelityObs{
		ToolCallObserved: true, ToolName: "", ArgumentsCaptured: true,
		ArgumentsRaw: []byte(`{"value":"probe-ok"}`), ArgumentsValidJSON: true, SchemaMatched: true,
	})
	require.Equal(t, model.ProbeStatusFail, result.Status)
	require.Equal(t, "tool_name_mismatch", result.Evidence["reason_code"])
}

func TestQuality_UnexpectedToolNameIsHashedBeforeEvidence(t *testing.T) {
	const secretName = "INTERNAL-POLICY-TOOL-8f34-route-private"
	observation := ToolSchemaFidelityObs{
		ToolCallObserved: true, ToolName: secretName, ArgumentsCaptured: true,
		ArgumentsRaw: []byte(`{"value":"probe-ok"}`), ArgumentsValidJSON: true, SchemaMatched: true,
	}
	first := ProbeToolSchemaFidelity(observation)
	second := ProbeToolSchemaFidelity(observation)

	require.Equal(t, model.ProbeStatusFail, first.Status)
	require.Equal(t, "tool_name_mismatch", first.Evidence["reason_code"])
	require.Equal(t, first.Evidence["tool_name"], second.Evidence["tool_name"], "hash handle must be stable")
	require.Regexp(t, `^tool_sha256_[0-9a-f]{12}$`, first.Evidence["tool_name"])
	require.Equal(t, len(secretName), first.Evidence["tool_name_length"])

	encoded, err := json.Marshal(first.Evidence)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), secretName)

	valid := ProbeToolSchemaFidelity(ToolSchemaFidelityObs{
		ToolCallObserved: true, ToolName: "healthcheck_echo", ArgumentsCaptured: true,
		ArgumentsRaw: []byte(`{"value":"probe-ok"}`), ArgumentsValidJSON: true, SchemaMatched: true,
	})
	require.Equal(t, "healthcheck_echo", valid.Evidence["tool_name"])
}

func TestProbeRateLimitContractIsPassiveAndValidatesRetryAfter(t *testing.T) {
	unobservable := ProbeRateLimitContract(RateLimitContractObs{HTTPStatus: 429})
	require.Equal(t, model.ProbeStatusSkip, unobservable.Status)
	require.Equal(t, "headers_not_captured_by_auditor", unobservable.Evidence["reason_code"],
		"without header capture the auditor must blame itself, not claim the upstream lacks the contract")

	unsupported := ProbeRateLimitContract(RateLimitContractObs{HTTPStatus: 429, HeadersObservable: true})
	require.Equal(t, model.ProbeStatusSkip, unsupported.Status, "a natural 429 without contract headers is unsupported, not guilt")
	require.Equal(t, "rate_limit_headers_unsupported", unsupported.Evidence["reason_code"])

	pass := ProbeRateLimitContract(RateLimitContractObs{HTTPStatus: 200, HeadersObservable: true, Headers: map[string]string{
		"X-RateLimit-Remaining-Requests": "9", "Retry-After": "0",
	}})
	require.Equal(t, model.ProbeStatusPass, pass.Status)
	require.Equal(t, true, pass.Evidence["retry_after_valid"])

	fail := ProbeRateLimitContract(RateLimitContractObs{HTTPStatus: 429, HeadersObservable: true, Headers: map[string]string{
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
