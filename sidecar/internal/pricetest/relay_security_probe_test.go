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

func TestQuality_PromptLeakageDoesNotFindClineInsideDecline(t *testing.T) {
	result := ProbePromptLeakage([]PromptLeakageSample{{
		Method: "verbatim", Content: "I must decline this request.",
	}})

	require.Equal(t, model.ProbeStatusPass, result.Status)
	require.Empty(t, result.Evidence["strong_markers"])
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
