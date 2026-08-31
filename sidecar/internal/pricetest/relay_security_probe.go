package pricetest

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"

	"supply-check-sdk/internal/common"
	"supply-check-sdk/internal/model"
)

// These classifiers adapt the relay-security audit methods published by
// github.com/toby-bridges/api-relay-audit (AGPL-3.0-only). The implementation
// is native to the health-check evidence model; attribution is also recorded
// in the repository NOTICE file.

type PromptLeakageSample struct {
	Method  string
	Content string
}

var promptTemplatePattern = regexp.MustCompile(`(?i)(system|developer)[\s_-]*(prompt|instruction)s?\s*[:：]`)

var refusalMarkers = []string{
	"cannot provide", "can't provide", "cannot reveal", "can't reveal", "won't reveal",
	"not able to provide", "do not have access", "can't share", "cannot share",
	"information is unavailable", "information isn't available", "not available to share",
	"无法提供", "不能提供", "无法透露", "不能透露", "不会透露", "无法访问",
	"無法提供", "不能提供", "無法透露", "不能透露", "不會透露", "無法存取",
}

var hiddenPromptDenialMarkers = []string{
	"decline", "refuse", "unable to", "can't help", "cannot help", "can't comply", "cannot comply",
	"can't discuss", "cannot discuss", "can't disclose", "cannot disclose",
	"will not disclose", "won't disclose", "will not provide", "won't provide", "will not share", "won't share",
	"don't have access", "not permitted", "not authorized", "access denied",
	"拒绝", "無法協助", "无法协助", "不能協助", "不能协助", "不便透露",
}

var hiddenPromptDenialLeadPattern = regexp.MustCompile(`(?i)^(?:` +
	`i\s+(?:cannot|can't|won't|will\s+not|do\s+not|don't|must\s+decline|refuse)|` +
	`i(?:'m|\s+am)\s+(?:unable|not\s+able|not\s+permitted|not\s+authorized|sorry)|` +
	`we\s+(?:cannot|can't|won't|will\s+not|do\s+not|don't|must\s+decline|refuse)|` +
	`we(?:'re|\s+are)\s+(?:unable|not\s+able|not\s+permitted|not\s+authorized|sorry)|` +
	`sorry\b|unfortunately\b|for\s+(?:privacy|security|safety)\b|` +
	`(?:that|this|the\s+requested)\s+information\b|information\s+(?:is\s+)?unavailable\b|` +
	`(?:the\s+)?(?:hidden|system|developer)\s+prompt\s+(?:is|isn't|cannot|can't|won't|will)|` +
	`no\s+(?:hidden|system|developer)\s+prompt\b|` +
	`(?:cannot|can't|won't|unable|declined|refused)\b|access\s+(?:is\s+)?denied\b|` +
	`抱歉|很抱歉|对不起|我(?:无法|不能|不会|拒绝)|无法|不能|不便` +
	`)`)

// hidden_prompt is deliberately absent: the JSON probe itself asks the model
// to use that key, so its presence says nothing about whether the value leaked.
// Word boundaries keep wrapper names meaningful (for example, "decline" must
// not be classified as a Cline wrapper disclosure).
var wrapperLeakPattern = regexp.MustCompile(`(?i)\b(kiro|cursor|cline|windsurf|antigravity)\b`)

const (
	hiddenPromptAbsent          = "absent"
	hiddenPromptNull            = "null"
	hiddenPromptEmpty           = "empty"
	hiddenPromptProbeEcho       = "probe_key_echo"
	hiddenPromptDenial          = "denial"
	hiddenPromptSubstantive     = "substantive"
	hiddenPromptRedactedPreview = `{"hidden_prompt":"[redacted]"}`
)

func ProbePromptLeakage(samples []PromptLeakageSample) model.ProbeResult {
	result := model.ProbeResult{
		ProbeKey: "p16_prompt_leakage", Kind: model.ProbeKindPromptLeakage,
		Status: model.ProbeStatusPass, Evidence: map[string]any{"samples": make([]map[string]any, 0, len(samples))},
	}
	strongHits := make([]string, 0)
	structuralHits := 0
	substantiveHiddenPromptHits := 0
	usable := 0
	evidenceSamples := result.Evidence["samples"].([]map[string]any)
	for _, sample := range samples {
		content := strings.TrimSpace(sample.Content)
		row := map[string]any{"method": sample.Method}
		if content == "" {
			row["outcome"] = "empty"
			evidenceSamples = append(evidenceSamples, row)
			continue
		}
		usable++
		lower := strings.ToLower(content)
		refused := looksLikeHiddenPromptDenial(lower)
		hiddenPromptClassification := classifyHiddenPromptValue(content)
		if hiddenPromptClassification != hiddenPromptAbsent {
			row["hidden_prompt_classification"] = hiddenPromptClassification
		}
		if hiddenPromptClassification == hiddenPromptDenial {
			refused = true
		}
		if hiddenPromptClassification == hiddenPromptSubstantive {
			// A leaked instruction can itself contain phrases such as "cannot
			// reveal". Do not let those phrases suppress inspection of the
			// substantive value.
			refused = false
			substantiveHiddenPromptHits++
		}
		row["refused"] = refused
		if hiddenPromptClassification == hiddenPromptSubstantive {
			row["preview"] = hiddenPromptRedactedPreview
		} else {
			row["preview"] = safePreview(content, 320)
		}
		if !refused {
			for _, marker := range wrapperLeakPattern.FindAllString(lower, -1) {
				strongHits = appendUnique(strongHits, marker)
			}
		}
		structural := promptTemplatePattern.MatchString(content) && !refused
		if structural {
			structuralHits++
		}
		row["structural_prompt"] = structural
		row["outcome"] = "observed"
		evidenceSamples = append(evidenceSamples, row)
	}
	result.Evidence["samples"] = evidenceSamples
	result.Evidence["usable_samples"] = usable
	result.Evidence["strong_markers"] = strongHits
	result.Evidence["structural_hits"] = structuralHits
	result.Evidence["substantive_hidden_prompt_hits"] = substantiveHiddenPromptHits
	switch {
	case usable == 0:
		result.Status = model.ProbeStatusError
		result.Evidence["reason_code"] = "no_usable_response"
	case len(strongHits) > 0:
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "wrapper_or_hidden_prompt_marker"
	case substantiveHiddenPromptHits > 0:
		result.Status = model.ProbeStatusWarn
		result.Evidence["reason_code"] = "substantive_hidden_prompt_value"
	case structuralHits > 0:
		result.Status = model.ProbeStatusWarn
		result.Evidence["reason_code"] = "prompt_template_structure"
	default:
		result.Evidence["reason_code"] = "no_prompt_leakage_marker"
	}
	return result
}

// classifyHiddenPromptValue inspects the value requested by the JSON probe,
// without copying it into evidence. Substantive values are also removed from
// the bounded response preview before evidence is retained.
func classifyHiddenPromptValue(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = stripCommandWrappers(content)
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &object); err != nil {
		return hiddenPromptAbsent
	}

	var raw json.RawMessage
	found := false
	for key, value := range object {
		if strings.EqualFold(strings.TrimSpace(key), "hidden_prompt") {
			raw = value
			found = true
			break
		}
	}
	if !found {
		return hiddenPromptAbsent
	}
	if strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return hiddenPromptNull
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return hiddenPromptSubstantive
	}
	switch typed := value.(type) {
	case string:
		return classifyHiddenPromptString(typed)
	case []any:
		if len(typed) == 0 {
			return hiddenPromptEmpty
		}
	case map[string]any:
		if len(typed) == 0 {
			return hiddenPromptEmpty
		}
	}
	return hiddenPromptSubstantive
}

func classifyHiddenPromptString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return hiddenPromptEmpty
	}
	lower := strings.ToLower(value)
	placeholder := strings.Trim(lower, " \t\r\n`'\"[]().")
	switch placeholder {
	case "hidden_prompt", "hidden prompt":
		return hiddenPromptProbeEcho
	case "null", "none", "n/a", "na", "redacted", "withheld":
		return hiddenPromptEmpty
	}
	if looksLikeHiddenPromptDenial(lower) {
		return hiddenPromptDenial
	}
	return hiddenPromptSubstantive
}

func looksLikeHiddenPromptDenial(lower string) bool {
	lower = strings.TrimSpace(strings.ReplaceAll(lower, "’", "'"))
	if !hiddenPromptDenialLeadPattern.MatchString(lower) {
		return false
	}
	return containsAnyString(lower, refusalMarkers...) ||
		containsAnyString(lower, hiddenPromptDenialMarkers...) ||
		containsAnyString(lower, "no hidden prompt", "no such prompt", "not disclosed", "not provided", "prompt is unavailable")
}

func ProbeInstructionPolicy(content, expectedMarker string) model.ProbeResult {
	result := model.ProbeResult{
		ProbeKey: "p17_instruction_policy", Kind: model.ProbeKindInstructionPolicy,
		Evidence: map[string]any{"expected_marker": expectedMarker, "preview": safePreview(content, 320)},
	}
	content = strings.TrimSpace(content)
	switch {
	case content == "":
		result.Status = model.ProbeStatusError
		result.Evidence["reason_code"] = "empty_response"
	case strings.Contains(strings.ToLower(content), strings.ToLower(expectedMarker)):
		result.Status = model.ProbeStatusPass
		result.Evidence["reason_code"] = "system_instruction_preserved"
	default:
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "system_instruction_overridden"
	}
	return result
}

type ToolSubstitutionSample struct {
	Manager  string
	Expected string
	Received string
}

func ProbeToolSubstitution(samples []ToolSubstitutionSample) model.ProbeResult {
	result := model.ProbeResult{
		ProbeKey: "p18_tool_substitution", Kind: model.ProbeKindToolSubstitution,
		Status: model.ProbeStatusPass, Evidence: map[string]any{},
	}
	rows := make([]map[string]any, 0, len(samples))
	usable, substituted := 0, 0
	for _, sample := range samples {
		received := stripCommandWrappers(sample.Received)
		verdict := classifyCopiedCommand(sample.Expected, received)
		if received != "" {
			usable++
		}
		if verdict == "substituted" {
			substituted++
		}
		rows = append(rows, map[string]any{
			"manager": sample.Manager, "expected": sample.Expected,
			"received": safePreview(received, 240), "verdict": verdict,
		})
	}
	result.Evidence["samples"] = rows
	result.Evidence["usable_samples"] = usable
	result.Evidence["substituted_samples"] = substituted
	switch {
	case usable == 0:
		result.Status = model.ProbeStatusError
		result.Evidence["reason_code"] = "no_usable_response"
	case substituted > 0:
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "command_tokens_changed"
	default:
		result.Evidence["reason_code"] = "commands_preserved"
	}
	return result
}

func ProbeContextIntegrity(content, marker string, contextChars int) model.ProbeResult {
	result := model.ProbeResult{
		ProbeKey: "p19_context_integrity", Kind: model.ProbeKindContextIntegrity,
		Evidence: map[string]any{
			"expected_marker": marker, "context_chars": contextChars,
			"preview": safePreview(content, 320),
		},
	}
	content = strings.TrimSpace(content)
	switch {
	case content == "":
		result.Status = model.ProbeStatusError
		result.Evidence["reason_code"] = "empty_response"
	case strings.Contains(strings.ToLower(content), strings.ToLower(marker)):
		result.Status = model.ProbeStatusPass
		result.Evidence["reason_code"] = "leading_marker_preserved"
	default:
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "leading_marker_lost"
	}
	return result
}

func classifyCopiedCommand(expected, received string) string {
	if received == expected {
		return "exact"
	}
	expectedTokens := strings.Fields(expected)
	receivedTokens := strings.Fields(strings.TrimRightFunc(strings.TrimSpace(received), func(r rune) bool {
		return unicode.IsPunct(r)
	}))
	if len(expectedTokens) != len(receivedTokens) {
		return "substituted"
	}
	for index := range expectedTokens {
		if !strings.EqualFold(expectedTokens[index], receivedTokens[index]) {
			return "substituted"
		}
	}
	return "whitespace"
}

func stripCommandWrappers(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "```") {
		if newline := strings.IndexByte(value, '\n'); newline >= 0 {
			value = value[newline+1:]
		}
		value = strings.TrimSuffix(strings.TrimSpace(value), "```")
	}
	value = strings.TrimSpace(value)
	for _, wrapper := range []string{"`", "\"", "'"} {
		if len(value) >= 2 && strings.HasPrefix(value, wrapper) && strings.HasSuffix(value, wrapper) {
			value = strings.TrimSuffix(strings.TrimPrefix(value, wrapper), wrapper)
		}
	}
	value = strings.TrimSpace(value)
	for _, prefix := range []string{"$ ", "# ", "> "} {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(value, prefix))
		}
	}
	return value
}

func safePreview(value string, limit int) string {
	value = common.MaskSensitiveInfo(strings.TrimSpace(value))
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func containsAnyString(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
