package pricetest

import (
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
	require.Contains(t, leak.Evidence["strong_markers"], "hidden_prompt")
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
