package pricetest

import (
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
	"无法提供", "不能提供", "无法透露", "不能透露", "不会透露", "无法访问",
	"無法提供", "不能提供", "無法透露", "不能透露", "不會透露", "無法存取",
}

// hidden_prompt is deliberately absent: the JSON probe itself asks the model
// to use that key, so its presence says nothing about whether the value leaked.
// Word boundaries keep wrapper names meaningful (for example, "decline" must
// not be classified as a Cline wrapper disclosure).
var wrapperLeakPattern = regexp.MustCompile(`(?i)\b(kiro|cursor|cline|windsurf|antigravity)\b`)

func ProbePromptLeakage(samples []PromptLeakageSample) model.ProbeResult {
	result := model.ProbeResult{
		ProbeKey: "p16_prompt_leakage", Kind: model.ProbeKindPromptLeakage,
		Status: model.ProbeStatusPass, Evidence: map[string]any{"samples": make([]map[string]any, 0, len(samples))},
	}
	strongHits := make([]string, 0)
	structuralHits := 0
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
		refused := containsAnyString(lower, refusalMarkers...)
		row["refused"] = refused
		row["preview"] = safePreview(content, 320)
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
	switch {
	case usable == 0:
		result.Status = model.ProbeStatusError
		result.Evidence["reason_code"] = "no_usable_response"
	case len(strongHits) > 0:
		result.Status = model.ProbeStatusFail
		result.Evidence["reason_code"] = "wrapper_or_hidden_prompt_marker"
	case structuralHits > 0:
		result.Status = model.ProbeStatusWarn
		result.Evidence["reason_code"] = "prompt_template_structure"
	default:
		result.Evidence["reason_code"] = "no_prompt_leakage_marker"
	}
	return result
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
