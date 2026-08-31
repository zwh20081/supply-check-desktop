package pricetest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"supply-check-sdk/internal/common"
	"supply-check-sdk/internal/model"
)

type ProtocolContractObs struct {
	Endpoint       string
	HTTPStatus     int
	ResponseFormat string
	ContentType    string
	ProtocolValid  bool
}

// ProbeProtocolContract (P10) validates the response envelope already fetched
// for P1/P3. It does not inspect model prose and never emits raw payloads.
func ProbeProtocolContract(obs ProtocolContractObs) model.ProbeResult {
	result := model.ProbeResult{ProbeKey: "p10_protocol_contract", Kind: model.ProbeKindProtocolContract}
	result.Evidence = map[string]any{
		"endpoint":        contractEvidenceText(obs.Endpoint),
		"http_status":     obs.HTTPStatus,
		"response_format": obs.ResponseFormat,
		"content_type":    contractEvidenceText(obs.ContentType),
	}
	if obs.ResponseFormat == "" {
		result.Status = model.ProbeStatusSkip
		result.Evidence["reason_code"] = "protocol_not_observable"
		return result
	}
	if obs.HTTPStatus != 0 && obs.HTTPStatus != 200 {
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "unexpected_http_status"
		return result
	}
	if !obs.ProtocolValid {
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "malformed_response_envelope"
		return result
	}
	result.Status = model.ProbeStatusPass
	return result
}

func contractEvidenceText(value string) string {
	value = common.MaskSensitiveInfo(value)
	if len(value) > 200 {
		return value[:200]
	}
	return value
}

type StreamIntegrityObs struct {
	Requested        bool
	ProtocolValid    bool
	DataFrames       int
	InvalidFrames    int
	TerminalObserved bool
	UsageReported    bool
	HasContent       bool
}

// ProbeStreamIntegrity (P11) checks the already-issued P2 SSE response. A
// provider that has no recognized terminal/final-usage capability is SKIP,
// never FAIL. Malformed JSON data frames are a real protocol failure.
func ProbeStreamIntegrity(obs StreamIntegrityObs) model.ProbeResult {
	result := model.ProbeResult{ProbeKey: "p11_stream_integrity", Kind: model.ProbeKindStreamIntegrity}
	result.Evidence = map[string]any{
		"data_frames":       obs.DataFrames,
		"invalid_frames":    obs.InvalidFrames,
		"terminal_observed": obs.TerminalObserved,
		"usage_reported":    obs.UsageReported,
		"content_observed":  obs.HasContent,
	}
	if !obs.Requested {
		result.Status = model.ProbeStatusSkip
		result.Evidence["reason_code"] = "stream_not_requested"
		return result
	}
	if obs.InvalidFrames > 0 || !obs.ProtocolValid {
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "malformed_sse"
		return result
	}
	if obs.DataFrames == 0 {
		result.Status = model.ProbeStatusSkip
		result.Evidence["reason_code"] = "sse_not_observable"
		return result
	}
	if !obs.TerminalObserved {
		result.Status = model.ProbeStatusSkip
		result.Evidence["reason_code"] = "terminal_marker_unsupported"
		return result
	}
	result.Status = model.ProbeStatusPass
	if !obs.UsageReported {
		result.Evidence["usage_capability"] = "unsupported"
	} else {
		result.Evidence["usage_capability"] = "reported"
	}
	return result
}

type UsageReconciliationObs struct {
	Reported            bool
	PromptTokens        int
	CompletionTokens    int
	TotalTokens         int
	CachedTokens        int
	CacheCreationTokens int
	// CacheTokensSeparate is true when cached input is reported alongside,
	// rather than inside, PromptTokens (Anthropic /v1/messages semantics).
	CacheTokensSeparate bool
	CostQuota           int
}

// ProbeUsageReconciliation (P12) validates provider usage accounting from the
// shared P1/P3 request. Estimated fallback usage is explicitly unsupported and
// neutral; only provider-reported contradictions fail.
func ProbeUsageReconciliation(obs UsageReconciliationObs) model.ProbeResult {
	result := model.ProbeResult{ProbeKey: "p12_usage_reconciliation", Kind: model.ProbeKindUsageReconciliation}
	result.Evidence = map[string]any{
		"usage_reported":        obs.Reported,
		"prompt_tokens":         obs.PromptTokens,
		"completion_tokens":     obs.CompletionTokens,
		"total_tokens":          obs.TotalTokens,
		"cached_tokens":         obs.CachedTokens,
		"cache_creation_tokens": obs.CacheCreationTokens,
		"cache_tokens_separate": obs.CacheTokensSeparate,
		"cost_quota":            obs.CostQuota,
	}
	if !obs.Reported {
		result.Status = model.ProbeStatusSkip
		result.Evidence["reason_code"] = "usage_unsupported"
		return result
	}
	switch {
	case obs.PromptTokens < 0 || obs.CompletionTokens < 0 || obs.TotalTokens < 0 || obs.CachedTokens < 0 || obs.CacheCreationTokens < 0 || obs.CostQuota < 0:
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "negative_usage"
	case !obs.CacheTokensSeparate && obs.CachedTokens > obs.PromptTokens:
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "cached_tokens_exceed_prompt"
	case obs.TotalTokens > 0 && obs.TotalTokens != obs.PromptTokens+obs.CompletionTokens:
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "total_tokens_mismatch"
	default:
		result.Status = model.ProbeStatusPass
	}
	return result
}

type CancellationContractObs struct {
	CarrierObserved bool
	ContextBound    bool
}

// ProbeCancellationContract (P13) is passive: it reports whether an existing
// carrier request bound the suite context to the real upstream transport. It
// deliberately does not create or cancel a paid long-running request.
func ProbeCancellationContract(obs CancellationContractObs) model.ProbeResult {
	result := model.ProbeResult{
		ProbeKey: "p13_cancellation_contract", Kind: model.ProbeKindCancellationContract,
		Evidence: map[string]any{"carrier_observed": obs.CarrierObserved, "transport_context_bound": obs.ContextBound},
	}
	if !obs.CarrierObserved {
		result.Status = model.ProbeStatusSkip
		result.Evidence["reason_code"] = "passive_carrier_unavailable"
		return result
	}
	if !obs.ContextBound {
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "transport_context_not_bound"
		return result
	}
	result.Status = model.ProbeStatusPass
	return result
}

type ToolSchemaFidelityObs struct {
	ToolCallObserved   bool
	ToolName           string
	ArgumentsCaptured  bool
	ArgumentsRaw       []byte
	ArgumentsValidJSON bool
	SchemaMatched      bool
}

const maxToolEvidenceFields = 32

type toolArgumentDiagnosis struct {
	ValidJSON        bool
	RootType         string
	SchemaMatched    bool
	Actual           map[string]any
	MissingFields    []string
	WrongTypeFields  []string
	WrongValueFields []string
	ExtraFields      []string
	ExtraFieldCount  int
}

// ProbeToolSchemaFidelity (P14) validates a fixed, explicitly enabled tool
// request. Providers that ignore or reject tools are unsupported (SKIP), not
// guilty; a malformed tool call that was actually emitted is a contract FAIL.
func ProbeToolSchemaFidelity(obs ToolSchemaFidelityObs) model.ProbeResult {
	argumentsValid := obs.ArgumentsValidJSON
	schemaMatched := obs.SchemaMatched
	var diagnosis toolArgumentDiagnosis
	if obs.ArgumentsCaptured {
		diagnosis = diagnoseToolArguments(obs.ArgumentsRaw)
		argumentsValid = diagnosis.ValidJSON
		schemaMatched = diagnosis.SchemaMatched
	}
	result := model.ProbeResult{
		ProbeKey: "p14_tool_schema_fidelity", Kind: model.ProbeKindToolSchemaFidelity,
		Evidence: map[string]any{
			"tool_call_observed": obs.ToolCallObserved,
			"tool_name":          contractEvidenceText(obs.ToolName),
			"arguments_captured": obs.ArgumentsCaptured,
			"arguments_valid":    argumentsValid,
			"schema_matched":     schemaMatched,
			"expected_contract":  healthcheckToolContractEvidence(),
		},
	}
	if obs.ArgumentsCaptured {
		result.Evidence["actual_arguments"] = diagnosis.Actual
		result.Evidence["schema_diagnostics"] = diagnosis.evidence()
		result.Evidence["provider_signal_consistent"] = obs.ArgumentsValidJSON == diagnosis.ValidJSON && obs.SchemaMatched == diagnosis.SchemaMatched
	}
	if !obs.ToolCallObserved {
		result.Status = model.ProbeStatusSkip
		result.Evidence["reason_code"] = "tool_call_unsupported"
		return result
	}
	if obs.ToolName != "healthcheck_echo" {
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "tool_name_mismatch"
		return result
	}
	if !argumentsValid {
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "tool_arguments_malformed"
		return result
	}
	if obs.ArgumentsCaptured && diagnosis.RootType != "object" {
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "tool_arguments_not_object"
		return result
	}
	if !schemaMatched {
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = diagnosis.reasonCode(obs.ArgumentsCaptured)
		return result
	}
	result.Status = model.ProbeStatusPass
	return result
}

func healthcheckToolContractEvidence() map[string]any {
	return map[string]any{
		"tool_name": "healthcheck_echo",
		"arguments": map[string]any{
			"type":     "object",
			"required": []string{"value"},
			"properties": map[string]any{
				"value": map[string]any{"type": "string", "const": "probe-ok"},
			},
			"additional_properties": false,
		},
	}
}

func diagnoseToolArguments(raw []byte) toolArgumentDiagnosis {
	diagnosis := toolArgumentDiagnosis{
		MissingFields: []string{}, WrongTypeFields: []string{},
		WrongValueFields: []string{}, ExtraFields: []string{},
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		diagnosis.RootType = "invalid_json"
		diagnosis.Actual = map[string]any{
			"captured": true, "valid_json": false, "byte_length": len(raw),
		}
		return diagnosis
	}

	diagnosis.ValidJSON = true
	diagnosis.RootType = toolJSONType(decoded)
	diagnosis.Actual = map[string]any{
		"captured": true, "valid_json": true, "root_type": diagnosis.RootType,
	}
	args, ok := decoded.(map[string]any)
	if !ok {
		diagnosis.Actual["value_summary"] = sanitizedToolValue(decoded)
		return diagnosis
	}

	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fields := make(map[string]any, min(len(keys), maxToolEvidenceFields))
	for i, key := range keys {
		if i >= maxToolEvidenceFields {
			break
		}
		fields[sanitizedToolFieldName(key)] = sanitizedToolValue(args[key])
	}
	diagnosis.Actual["field_count"] = len(keys)
	diagnosis.Actual["fields"] = fields
	if omitted := len(keys) - len(fields); omitted > 0 {
		diagnosis.Actual["fields_omitted"] = omitted
	}

	value, exists := args["value"]
	if !exists {
		diagnosis.MissingFields = []string{"value"}
	} else if text, ok := value.(string); !ok {
		diagnosis.WrongTypeFields = []string{"value"}
	} else if text != "probe-ok" {
		diagnosis.WrongValueFields = []string{"value"}
	}
	for _, key := range keys {
		if key == "value" {
			continue
		}
		diagnosis.ExtraFieldCount++
		if len(diagnosis.ExtraFields) < maxToolEvidenceFields {
			diagnosis.ExtraFields = append(diagnosis.ExtraFields, sanitizedToolFieldName(key))
		}
	}
	diagnosis.SchemaMatched = len(diagnosis.MissingFields) == 0 && len(diagnosis.WrongTypeFields) == 0 &&
		len(diagnosis.WrongValueFields) == 0 && diagnosis.ExtraFieldCount == 0 && len(keys) == 1
	return diagnosis
}

func (diagnosis toolArgumentDiagnosis) evidence() map[string]any {
	issueCodes := make([]string, 0, 5)
	if diagnosis.ValidJSON && diagnosis.RootType != "object" {
		issueCodes = append(issueCodes, "root_type_mismatch")
	}
	if len(diagnosis.MissingFields) > 0 {
		issueCodes = append(issueCodes, "missing_required")
	}
	if len(diagnosis.WrongTypeFields) > 0 {
		issueCodes = append(issueCodes, "wrong_type")
	}
	if len(diagnosis.WrongValueFields) > 0 {
		issueCodes = append(issueCodes, "wrong_value")
	}
	if diagnosis.ExtraFieldCount > 0 {
		issueCodes = append(issueCodes, "unexpected_field")
	}
	return map[string]any{
		"issue_codes":        issueCodes,
		"root_type":          diagnosis.RootType,
		"missing_fields":     diagnosis.MissingFields,
		"wrong_type_fields":  diagnosis.WrongTypeFields,
		"wrong_value_fields": diagnosis.WrongValueFields,
		"extra_fields":       diagnosis.ExtraFields,
		"extra_field_count":  diagnosis.ExtraFieldCount,
	}
}

func (diagnosis toolArgumentDiagnosis) reasonCode(captured bool) string {
	if !captured {
		return "tool_schema_mismatch"
	}
	switch {
	case len(diagnosis.MissingFields) > 0:
		return "tool_schema_missing_required"
	case len(diagnosis.WrongTypeFields) > 0:
		return "tool_schema_wrong_type"
	case len(diagnosis.WrongValueFields) > 0:
		return "tool_schema_wrong_value"
	case diagnosis.ExtraFieldCount > 0:
		return "tool_schema_extra_fields"
	default:
		return "tool_schema_mismatch"
	}
}

func toolJSONType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	default:
		return "unknown"
	}
}

func sanitizedToolValue(value any) map[string]any {
	summary := map[string]any{"type": toolJSONType(value)}
	switch typed := value.(type) {
	case string:
		summary["length"] = utf8.RuneCountInString(typed)
		if typed == "probe-ok" {
			summary["value"] = typed
		} else {
			summary["value"] = "[redacted]"
		}
	case []any:
		summary["item_count"] = len(typed)
	case map[string]any:
		summary["field_count"] = len(typed)
	case nil:
		// Type is sufficient.
	default:
		summary["value"] = "[redacted]"
	}
	return summary
}

func sanitizedToolFieldName(value string) string {
	if value == "value" {
		return value
	}
	// Unknown keys are untrusted values too: a malicious or confused model can
	// place a credential in a JSON field name. Keep a stable correlation handle
	// without persisting the key itself.
	digest := sha256.Sum256([]byte(value))
	return "field_sha256_" + hex.EncodeToString(digest[:6])
}

type RateLimitContractObs struct {
	HTTPStatus int
	Headers    map[string]string
}

// ProbeRateLimitContract (P15) consumes headers from an already-issued request
// or a naturally occurring error. It never attempts to manufacture a 429.
func ProbeRateLimitContract(obs RateLimitContractObs) model.ProbeResult {
	headerNames := make([]string, 0, len(obs.Headers))
	retryAfter := ""
	for name, value := range obs.Headers {
		if !isRateLimitHeader(name) {
			continue
		}
		headerNames = append(headerNames, strings.ToLower(name))
		if strings.EqualFold(name, "Retry-After") {
			retryAfter = strings.TrimSpace(value)
		}
	}
	sort.Strings(headerNames)
	result := model.ProbeResult{
		ProbeKey: "p15_rate_limit_contract", Kind: model.ProbeKindRateLimitContract,
		Evidence: map[string]any{
			"http_status":  obs.HTTPStatus,
			"header_count": len(headerNames),
			"header_names": headerNames,
		},
	}
	if len(headerNames) == 0 {
		result.Status = model.ProbeStatusSkip
		result.Evidence["reason_code"] = "rate_limit_headers_unsupported"
		return result
	}
	if retryAfter != "" {
		valid := validRetryAfter(retryAfter)
		result.Evidence["retry_after_present"] = true
		result.Evidence["retry_after_valid"] = valid
		if !valid {
			result.Status = model.ProbeStatusFail
			result.Evidence["reason_code"] = "retry_after_malformed"
			return result
		}
	}
	result.Status = model.ProbeStatusPass
	return result
}

func isRateLimitHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "retry-after" || strings.HasPrefix(name, "x-ratelimit-") ||
		strings.HasPrefix(name, "ratelimit-") || strings.HasPrefix(name, "anthropic-ratelimit-")
}

func validRetryAfter(value string) bool {
	if seconds, err := strconv.Atoi(value); err == nil {
		return seconds >= 0
	}
	_, err := http.ParseTime(value)
	return err == nil
}
