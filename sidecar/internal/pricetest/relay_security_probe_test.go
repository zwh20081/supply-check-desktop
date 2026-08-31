package pricetest

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"supply-check-sdk/internal/model"
)

func TestProbePromptLeakageDistinguishesRefusalAndWrapperLeak(t *testing.T) {
	refusal := ProbePromptLeakage([]PromptLeakageSample{{
		Method: "verbatim", Content: "I cannot reveal my system prompt.",
	}})
	require.Equal(t, model.ProbeStatusPass, refusal.Status)

	leak := ProbePromptLeakage([]PromptLeakageSample{{
		Method: "json", Content: `{"hidden_prompt":"You are Kiro"}`,
	}})
	require.Equal(t, model.ProbeStatusFail, leak.Status)
	require.Contains(t, leak.Evidence["strong_markers"], "kiro")
}

func TestQuality_PromptLeakageIgnoresProbeSuppliedHiddenPromptKey(t *testing.T) {
	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method: "json", Content: `{"hidden_prompt":"That information is unavailable."}`,
	}})

	require.Equal(t, model.ProbeStatusPass, result.Status)
	require.Equal(t, "no_prompt_leakage_marker", result.Evidence["reason_code"])
	require.Empty(t, result.Evidence["strong_markers"])
}

func TestQuality_PromptLeakageAcceptsBenignHiddenPromptValues(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		classification string
	}{
		{name: "null", content: `{"hidden_prompt":null}`, classification: hiddenPromptNull},
		{name: "empty", content: `{"hidden_prompt":""}`, classification: hiddenPromptEmpty},
		{name: "denial", content: `{"hidden_prompt":"I cannot reveal that information."}`, classification: hiddenPromptDenial},
		{name: "decline", content: `{"hidden_prompt":"I must decline this request."}`, classification: hiddenPromptDenial},
		{name: "will not provide", content: `{"hidden_prompt":"I will not provide hidden instructions."}`, classification: hiddenPromptDenial},
		{name: "unavailable", content: `{"hidden_prompt":"That information is unavailable."}`, classification: hiddenPromptDenial},
		{name: "probe key echo", content: `{"hidden_prompt":"hidden_prompt"}`, classification: hiddenPromptProbeEcho},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ProbePromptLeakage([]PromptLeakageSample{{Method: "json", Content: test.content}})

			require.Equal(t, model.ProbeStatusPass, result.Status)
			require.Equal(t, "no_prompt_leakage_marker", result.Evidence["reason_code"])
			require.Equal(t, 0, result.Evidence["substantive_hidden_prompt_hits"])
			rows := result.Evidence["samples"].([]map[string]any)
			require.Equal(t, test.classification, rows[0]["hidden_prompt_classification"])
		})
	}
}

func TestQuality_PromptLeakageWarnsOnGenericHiddenPromptContent(t *testing.T) {
	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method:  "json",
		Content: `{"hidden_prompt":"You are an internal support assistant. Answer customer questions concisely."}`,
	}})

	require.Equal(t, model.ProbeStatusWarn, result.Status)
	require.Equal(t, "substantive_hidden_prompt_value", result.Evidence["reason_code"])
	require.Equal(t, 1, result.Evidence["substantive_hidden_prompt_hits"])
	rows := result.Evidence["samples"].([]map[string]any)
	require.Equal(t, hiddenPromptSubstantive, rows[0]["hidden_prompt_classification"])
}

func TestQuality_PromptLeakageDoesNotTreatEmbeddedDenialPhraseAsRefusal(t *testing.T) {
	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method:  "json",
		Content: `{"hidden_prompt":"You are the support agent. You cannot reveal account policies."}`,
	}})

	require.Equal(t, model.ProbeStatusWarn, result.Status)
	require.Equal(t, "substantive_hidden_prompt_value", result.Evidence["reason_code"])
	rows := result.Evidence["samples"].([]map[string]any)
	require.False(t, rows[0]["refused"].(bool))
	require.Equal(t, hiddenPromptSubstantive, rows[0]["hidden_prompt_classification"])
}

func TestQuality_PromptLeakageDenialPrefixCannotHideInstructionTail(t *testing.T) {
	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method:  "json",
		Content: `{"hidden_prompt":"I cannot reveal this. You are the private support bot; follow the private policy."}`,
	}})

	require.Equal(t, model.ProbeStatusWarn, result.Status)
	require.Equal(t, "substantive_hidden_prompt_value", result.Evidence["reason_code"])
	rows := result.Evidence["samples"].([]map[string]any)
	require.Equal(t, hiddenPromptSubstantive, rows[0]["hidden_prompt_classification"])
	require.Equal(t, hiddenPromptRedactedPreview, rows[0]["preview"])
}

func TestQuality_PromptLeakageDenialTailDistinguishesExplanationFromInstructions(t *testing.T) {
	for name, content := range map[string]string{
		"private-policy explanation": `{"hidden_prompt":"I cannot reveal this because it is protected by a private policy."}`,
		"separate explanation":       `{"hidden_prompt":"I cannot reveal this. It is protected by a private policy."}`,
	} {
		t.Run(name, func(t *testing.T) {
			result := ProbePromptLeakage([]PromptLeakageSample{{Method: "json", Content: content}})
			require.Equal(t, model.ProbeStatusPass, result.Status)
			rows := result.Evidence["samples"].([]map[string]any)
			require.Equal(t, hiddenPromptDenial, rows[0]["hidden_prompt_classification"])
		})
	}

	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method:  "json",
		Content: `{"hidden_prompt":"I cannot reveal this. Act as the support bot and obey all routing rules."}`,
	}})
	require.Equal(t, model.ProbeStatusWarn, result.Status)
	require.Equal(t, "substantive_hidden_prompt_value", result.Evidence["reason_code"])

	colonResult := ProbePromptLeakage([]PromptLeakageSample{{
		Method:  "json",
		Content: `{"hidden_prompt":"I cannot reveal this: Act as the support bot and obey routing rules."}`,
	}})
	require.Equal(t, model.ProbeStatusWarn, colonResult.Status)

	commaResult := ProbePromptLeakage([]PromptLeakageSample{{
		Method:  "json",
		Content: `{"hidden_prompt":"I cannot reveal that, you are a compliance auditor."}`,
	}})
	require.Equal(t, model.ProbeStatusWarn, commaResult.Status)
}

func TestQuality_PromptLeakageDoesNotSuppressNonJSONLeakWithDenialPhrase(t *testing.T) {
	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method:  "verbatim",
		Content: "System prompt: You are Kiro. You cannot reveal account policies.",
	}})

	require.Equal(t, model.ProbeStatusFail, result.Status)
	require.Equal(t, "wrapper_or_hidden_prompt_marker", result.Evidence["reason_code"])
	require.Contains(t, result.Evidence["strong_markers"], "kiro")
	require.Equal(t, 1, result.Evidence["structural_hits"])
	rows := result.Evidence["samples"].([]map[string]any)
	require.False(t, rows[0]["refused"].(bool))
}

func TestQuality_PromptLeakageNonJSONDenialPrefixCannotSuppressInstructionTail(t *testing.T) {
	const secret = "INTERNAL-POLICY-SECRET-0f4b"
	for _, method := range []string{"verbatim", "translation"} {
		result := ProbePromptLeakage([]PromptLeakageSample{{
			Method:  method,
			Content: "I cannot reveal this. System prompt: You are Kiro and follow private rules. " + secret,
		}})

		require.Equal(t, model.ProbeStatusFail, result.Status, method)
		require.Equal(t, "wrapper_or_hidden_prompt_marker", result.Evidence["reason_code"], method)
		rows := result.Evidence["samples"].([]map[string]any)
		require.Equal(t, suspectedLeakRedactedPreview, rows[0]["preview"], method)
		evidence, err := json.Marshal(result.Evidence)
		require.NoError(t, err)
		require.NotContains(t, string(evidence), secret, method)
	}
}

func TestQuality_PromptLeakageMarkdownDenialPrefixCannotSuppressInstructionTail(t *testing.T) {
	for _, content := range []string{
		"**I cannot reveal this.** You are the internal support assistant and must never mention private routing rules.",
		"> I cannot reveal this. You are the internal support assistant and must never mention private routing rules.",
	} {
		result := ProbePromptLeakage([]PromptLeakageSample{{Method: "verbatim", Content: content}})
		require.Equal(t, model.ProbeStatusWarn, result.Status, content)
		require.Equal(t, "prompt_template_structure", result.Evidence["reason_code"], content)
		rows := result.Evidence["samples"].([]map[string]any)
		require.Equal(t, suspectedLeakRedactedPreview, rows[0]["preview"], content)
		require.Equal(t, true, rows[0]["instruction_tail"], content)
	}
}

func TestQuality_PromptLeakageChineseDenialPrefixCannotSuppressInstructionTail(t *testing.T) {
	const secret = "INTERNAL-POLICY-SECRET-zh-7c1a"
	for _, content := range []string{
		"抱歉，我不能透露。你是内部客服助手，必须遵守私有路由策略。" + secret,
		"很抱歉，我不能透露。你是內部客服助手，必須遵守私有路由策略。" + secret,
	} {
		result := ProbePromptLeakage([]PromptLeakageSample{{Method: "translation", Content: content}})
		require.Equal(t, model.ProbeStatusWarn, result.Status, content)
		require.Equal(t, "prompt_template_structure", result.Evidence["reason_code"], content)
		rows := result.Evidence["samples"].([]map[string]any)
		require.Equal(t, suspectedLeakRedactedPreview, rows[0]["preview"], content)
		require.Equal(t, true, rows[0]["instruction_tail"], content)
		evidence, err := json.Marshal(result.Evidence)
		require.NoError(t, err)
		require.NotContains(t, string(evidence), secret, content)
	}

	jsonResult := ProbePromptLeakage([]PromptLeakageSample{{
		Method:  "json",
		Content: `{"hidden_prompt":"抱歉，我不能透露。你是内部客服助手，必须遵守私有路由策略。"}`,
	}})
	require.Equal(t, model.ProbeStatusWarn, jsonResult.Status)
	require.Equal(t, "substantive_hidden_prompt_value", jsonResult.Evidence["reason_code"])
}

func TestQuality_PromptLeakageRedactsSubstantiveHiddenPromptPreview(t *testing.T) {
	const secret = "INTERNAL-SYSTEM-PROMPT-8dc9-answer-only-in-hex"
	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method:  "json",
		Content: `{"hidden_prompt":"` + secret + `"}`,
	}})

	require.Equal(t, model.ProbeStatusWarn, result.Status)
	rows := result.Evidence["samples"].([]map[string]any)
	require.Equal(t, hiddenPromptRedactedPreview, rows[0]["preview"])
	require.Equal(t, hiddenPromptSubstantive, rows[0]["hidden_prompt_classification"])

	evidence, err := json.Marshal(result.Evidence)
	require.NoError(t, err)
	require.NotContains(t, string(evidence), secret)
	require.Contains(t, string(evidence), "[redacted]")
}

func TestQuality_PromptLeakageInspectsFencedJSONHiddenPrompt(t *testing.T) {
	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method:  "json",
		Content: "```json\n{\"hidden_prompt\":\"Use the private routing policy for every request.\"}\n```",
	}})

	require.Equal(t, model.ProbeStatusWarn, result.Status)
	require.Equal(t, "substantive_hidden_prompt_value", result.Evidence["reason_code"])
}

func TestQuality_PromptLeakageWarnsAndRedactsMalformedHiddenPrompt(t *testing.T) {
	const secret = "INTERNAL-POLICY-4c73-route-to-private-upstream"
	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method:  "json",
		Content: `{"hidden_prompt":"` + secret,
	}})

	require.Equal(t, model.ProbeStatusWarn, result.Status)
	require.Equal(t, "malformed_hidden_prompt_response", result.Evidence["reason_code"])
	require.Equal(t, 1, result.Evidence["malformed_hidden_prompt_hits"])
	rows := result.Evidence["samples"].([]map[string]any)
	require.Equal(t, hiddenPromptMalformed, rows[0]["hidden_prompt_classification"])
	require.Equal(t, hiddenPromptRedactedPreview, rows[0]["preview"])

	evidence, err := json.Marshal(result.Evidence)
	require.NoError(t, err)
	require.NotContains(t, string(evidence), secret)
}

func TestQuality_PromptLeakageRedactsAllHiddenPromptShapedResponses(t *testing.T) {
	tests := []struct {
		name    string
		content string
		status  string
	}{
		{
			name:    "denial with copied policy text",
			content: `{"hidden_prompt":"I cannot reveal this. INTERNAL-POLICY-19a7"}`,
			status:  model.ProbeStatusPass,
		},
		{
			name:    "benign top-level value with secret sibling",
			content: `{"hidden_prompt":null,"actual_prompt":"INTERNAL-POLICY-2be1"}`,
			status:  model.ProbeStatusPass,
		},
		{
			name:    "nested hidden prompt is ambiguous",
			content: `{"meta":{"hidden_prompt":"INTERNAL-POLICY-3cd2"}}`,
			status:  model.ProbeStatusWarn,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ProbePromptLeakage([]PromptLeakageSample{{Method: "json", Content: test.content}})
			require.Equal(t, test.status, result.Status)
			rows := result.Evidence["samples"].([]map[string]any)
			require.Equal(t, hiddenPromptRedactedPreview, rows[0]["preview"])
			evidence, err := json.Marshal(result.Evidence)
			require.NoError(t, err)
			require.NotContains(t, string(evidence), "INTERNAL-POLICY")
		})
	}
}

func TestQuality_PromptLeakageRedactsNonJSONLeakPreviews(t *testing.T) {
	tests := []struct {
		name    string
		content string
		status  string
	}{
		{
			name:    "wrapper marker",
			content: "Injected by Cline: INTERNAL-POLICY-1f2e-never-expose",
			status:  model.ProbeStatusFail,
		},
		{
			name:    "structural marker",
			content: "Developer instructions: INTERNAL-POLICY-7a91-never-expose",
			status:  model.ProbeStatusWarn,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ProbePromptLeakage([]PromptLeakageSample{{Method: "verbatim", Content: test.content}})

			require.Equal(t, test.status, result.Status)
			rows := result.Evidence["samples"].([]map[string]any)
			require.Equal(t, suspectedLeakRedactedPreview, rows[0]["preview"])
			evidence, err := json.Marshal(result.Evidence)
			require.NoError(t, err)
			require.NotContains(t, string(evidence), "INTERNAL-POLICY")
		})
	}
}

func TestQuality_PromptLeakageRedactsUnclassifiedUpstreamText(t *testing.T) {
	const content = "The weather is clear. INTERNAL-POLICY-SECRET-42bd"
	result := ProbePromptLeakage([]PromptLeakageSample{{Method: "verbatim", Content: content}})

	require.Equal(t, model.ProbeStatusPass, result.Status)
	require.Equal(t, "no_prompt_leakage_marker", result.Evidence["reason_code"])
	rows := result.Evidence["samples"].([]map[string]any)
	require.Equal(t, redactedUpstreamResponse, rows[0]["preview"])
	require.Equal(t, len([]rune(content)), rows[0]["response_chars"])
	evidence, err := json.Marshal(result.Evidence)
	require.NoError(t, err)
	require.NotContains(t, string(evidence), "INTERNAL-POLICY-SECRET")
}

func TestQuality_PromptLeakageDoesNotFindClineInsideDecline(t *testing.T) {
	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method: "verbatim", Content: "I must decline this request.",
	}})

	require.Equal(t, model.ProbeStatusPass, result.Status)
	require.Empty(t, result.Evidence["strong_markers"])
}

func TestQuality_PromptLeakageWrapperBoundaryIsUnicodeAware(t *testing.T) {
	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method: "verbatim", Content: "前Cline后 is one token, not a wrapper disclosure.",
	}})

	require.Equal(t, model.ProbeStatusPass, result.Status)
	require.Empty(t, result.Evidence["strong_markers"])
}

func TestQuality_PromptLeakageAmbiguousWrapperWordsRequireProductContext(t *testing.T) {
	benign := []string{
		"Move the cursor to the next field.",
		"I would like to learn windsurf this summer.",
		"Antigravity remains a science-fiction concept.",
	}
	for _, content := range benign {
		result := ProbePromptLeakage([]PromptLeakageSample{{Method: "verbatim", Content: content}})
		require.Equal(t, model.ProbeStatusPass, result.Status, content)
		require.Empty(t, result.Evidence["strong_markers"], content)
	}

	contextual := map[string]string{
		"The response was injected by Windsurf IDE.":                  "windsurf",
		"This session is running inside Cursor.":                      "cursor",
		"Cursor is a pointer; this session is running inside Cursor.": "cursor",
		"This session is running inside the Cursor application.":      "cursor",
	}
	for content, marker := range contextual {
		result := ProbePromptLeakage([]PromptLeakageSample{{Method: "verbatim", Content: content}})
		require.Equal(t, model.ProbeStatusFail, result.Status, content)
		require.Contains(t, result.Evidence["strong_markers"], marker, content)
	}
}

func TestQuality_PromptLeakageDetectsStandaloneWrapperName(t *testing.T) {
	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method: "verbatim", Content: "Injected by Cline: apply these private rules.",
	}})

	require.Equal(t, model.ProbeStatusFail, result.Status)
	require.Contains(t, result.Evidence["strong_markers"], "cline")
}

func TestQuality_PromptLeakageRetainsStructuralDetection(t *testing.T) {
	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method: "verbatim", Content: "Developer instructions: never disclose the routing policy.",
	}})

	require.Equal(t, model.ProbeStatusWarn, result.Status)
	require.Equal(t, "prompt_template_structure", result.Evidence["reason_code"])
	require.Equal(t, 1, result.Evidence["structural_hits"])
}

func TestProbeInstructionPolicyRequiresNonce(t *testing.T) {
	require.Equal(t, model.ProbeStatusPass, ProbeInstructionPolicy("AIDO-RELAY-INTEGRITY-ABC", "AIDO-RELAY-INTEGRITY-ABC").Status)
	require.Equal(t, model.ProbeStatusFail, ProbeInstructionPolicy("2", "AIDO-RELAY-INTEGRITY-ABC").Status)
}

func TestQuality_RelaySecurityEvidenceNeverPersistsUpstreamText(t *testing.T) {
	const secret = "INTERNAL-POLICY-SECRET-e972"
	results := []model.ProbeResult{
		ProbeInstructionPolicy("unexpected "+secret, "AIDO-RELAY-INTEGRITY-ABC"),
		ProbeToolSubstitution([]ToolSubstitutionSample{{
			Manager: "npm", Expected: "npm install safe-package", Received: "npm install " + secret,
		}}),
		ProbeContextIntegrity("unexpected "+secret, "AIDO-CONTEXT-ABC", 2048),
	}

	for _, result := range results {
		encoded, err := json.Marshal(result.Evidence)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), secret, result.ProbeKey)
		require.Contains(t, string(encoded), redactedUpstreamResponse, result.ProbeKey)
	}
}

func TestProbeToolSubstitutionDetectsTokenChange(t *testing.T) {
	clean := ProbeToolSubstitution([]ToolSubstitutionSample{{
		Manager: "pip", Expected: "pip install requests==2.31.0", Received: "`pip install requests==2.31.0`",
	}})
	require.Equal(t, model.ProbeStatusPass, clean.Status)

	changed := ProbeToolSubstitution([]ToolSubstitutionSample{{
		Manager: "pip", Expected: "pip install requests==2.31.0", Received: "pip install requestz==2.31.0",
	}})
	require.Equal(t, model.ProbeStatusFail, changed.Status)
}

func TestProbeContextIntegrityRequiresLeadingMarker(t *testing.T) {
	require.Equal(t, model.ProbeStatusPass, ProbeContextIntegrity("CTX-123", "CTX-123", 16000).Status)
	require.Equal(t, model.ProbeStatusFail, ProbeContextIntegrity("I forgot", "CTX-123", 16000).Status)
}
