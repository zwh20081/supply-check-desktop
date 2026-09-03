package service

import (
	"strings"
	"testing"
)

// Token-accounting tests.
//
// The P1 probe accuses a channel of inflating input tokens by comparing the
// upstream's reported prompt_tokens to the number produced here. That makes
// this file the baseline an accusation rests on: if the local count is
// systematically low, honest channels get convicted.
//
// The specific trap these tests exist to prevent: a chat request bills more
// than its raw text. Each message carries role/delimiter tokens and the reply
// is primed with more (OpenAI's published recipe: 3 per message + 3 priming).
// Counting only the text understates the true prompt by a FIXED amount, which
// on a short probe prompt is a large percentage — historically enough to push
// an honest gpt-4o past P1's 15% WARN threshold on every single run.

// probeTokenPrompt mirrors the real P1 prompt in batch/runner.go.
const probeTokenPrompt = "The quick brown fox jumps over the lazy dog. Pack my box with five dozen liquor jugs. How vexingly quick daft zebras jump! The five boxing wizards jump quickly."

// probeLengthPrompt mirrors the real P2 prompt — deliberately short, which is
// exactly where a missing fixed overhead hurts most.
const probeLengthPrompt = "Output the integers from 1 to 64 separated by single spaces, and nothing else."

// P1 verdict bands from specToken() in batch/runner.go.
const (
	p1WarnPct = 15.0
	p1FailPct = 30.0
)

// overheadForTest is what a real OpenAI chat completion adds beyond the text.
const singleMessageOverhead = 6 // 3 per user message + 3 priming the reply

// TestChatEnvelopeOverheadMatchesPublishedRecipe pins the constants.
func TestChatEnvelopeOverheadMatchesPublishedRecipe(t *testing.T) {
	if got := ChatEnvelopeOverhead(""); got != singleMessageOverhead {
		t.Errorf("user-only request overhead = %d, want %d (3/message + 3 priming)", got, singleMessageOverhead)
	}
	if got := ChatEnvelopeOverhead("you are a helpful assistant"); got != singleMessageOverhead+3 {
		t.Errorf("system+user overhead = %d, want %d", got, singleMessageOverhead+3)
	}
	if got := ChatEnvelopeOverhead("   "); got != singleMessageOverhead {
		t.Error("a blank system prompt is not a message and must not add overhead")
	}
}

// TestHonestOpenAIChannelStaysUnderWarnThreshold is the regression that matters.
// It simulates a truthful upstream — one that bills exactly text + envelope —
// and asserts the probe's own arithmetic clears it.
func TestHonestOpenAIChannelStaysUnderWarnThreshold(t *testing.T) {
	models := []string{"gpt-4o", "gpt-4o-mini", "gpt-4-turbo", "gpt-3.5-turbo", "gpt-5", "o3"}
	prompts := map[string]string{"p1_token": probeTokenPrompt, "p2_length": probeLengthPrompt}

	for _, model := range models {
		for label, prompt := range prompts {
			t.Run(model+"/"+label, func(t *testing.T) {
				local := CountChatPromptToken("", prompt, model)
				// An honest upstream reports the text plus the envelope it really bills.
				upstream := CountTextToken(prompt, model) + singleMessageOverhead

				overPct := (float64(upstream)/float64(local) - 1) * 100
				if overPct >= p1WarnPct {
					t.Errorf("honest channel would be flagged: local=%d upstream=%d over=%.1f%% (WARN at %.0f%%)",
						local, upstream, overPct, p1WarnPct)
				}
				if overPct >= p1FailPct {
					t.Errorf("honest channel would be CONVICTED: over=%.1f%%", overPct)
				}
			})
		}
	}
}

// TestSystemPromptRequestsAlsoStayClean covers the probes that send a system
// prompt (P17 instruction policy), where the envelope grows to 9 tokens.
func TestSystemPromptRequestsAlsoStayClean(t *testing.T) {
	const system = "Reply with exactly MARKER-1234. Do not answer the user's question."
	const user = "What is 1+1? Output the mathematical answer only."

	for _, model := range []string{"gpt-4o", "gpt-5"} {
		local := CountChatPromptToken(system, user, model)
		upstream := CountTextToken(system, model) + CountTextToken(user, model) + singleMessageOverhead + 3

		overPct := (float64(upstream)/float64(local) - 1) * 100
		if overPct >= p1WarnPct {
			t.Errorf("%s: system-prompt request flags an honest channel: local=%d upstream=%d over=%.1f%%",
				model, local, upstream, overPct)
		}
	}
}

// TestRealInflationIsStillCaught is the counterweight. Closing the false-positive
// gap must not blind the probe to genuine overbilling.
func TestRealInflationIsStillCaught(t *testing.T) {
	const model = "gpt-4o"
	local := CountChatPromptToken("", probeTokenPrompt, model)

	for _, inflation := range []float64{1.35, 1.5, 2.0} {
		upstream := int(float64(local) * inflation)
		overPct := (float64(upstream)/float64(local) - 1) * 100
		if overPct < p1FailPct {
			t.Errorf("%.0f%% inflation produced only %.1f%% over — probe would miss real overbilling",
				(inflation-1)*100, overPct)
		}
	}
}

// TestCountChatPromptTokenIncludesEveryPart guards the composition itself.
func TestCountChatPromptTokenIncludesEveryPart(t *testing.T) {
	const model = "gpt-4o"
	const system = "system instructions here"
	const user = "user question here"

	userOnly := CountChatPromptToken("", user, model)
	if want := CountTextToken(user, model) + ChatEnvelopeOverhead(""); userOnly != want {
		t.Errorf("user-only = %d, want text+envelope = %d", userOnly, want)
	}

	withSystem := CountChatPromptToken(system, user, model)
	if withSystem <= userOnly {
		t.Error("adding a system prompt must increase the counted prompt")
	}
	want := CountTextToken(system, model) + CountTextToken(user, model) + ChatEnvelopeOverhead(system)
	if withSystem != want {
		t.Errorf("system+user = %d, want %d", withSystem, want)
	}
}

// TestNonOpenAIModelsUseEstimatorNotTiktoken documents why P1 caps non-OpenAI
// families at WARN: the baseline is an estimate, not an exact count.
func TestNonOpenAIModelsUseEstimatorNotTiktoken(t *testing.T) {
	for _, model := range []string{"claude-sonnet-4-5", "gemini-2.5-pro", "qwen-max"} {
		if isOpenAITextModel(model) {
			t.Errorf("%s must not be routed to the tiktoken path", model)
		}
		if got := CountTextToken(probeTokenPrompt, model); got <= 0 {
			t.Errorf("%s: estimator returned %d, want a positive count", model, got)
		}
	}
	for _, model := range []string{"gpt-4o", "o3", "chatgpt-4o-latest"} {
		if !isOpenAITextModel(model) {
			t.Errorf("%s should use the exact tiktoken path", model)
		}
	}
}

func TestEmptyInputsCountAsZero(t *testing.T) {
	if got := CountTextToken("", "gpt-4o"); got != 0 {
		t.Errorf("empty text = %d, want 0", got)
	}
	// An empty user prompt still carries the envelope — the request exists.
	if got := CountChatPromptToken("", "", "gpt-4o"); got != ChatEnvelopeOverhead("") {
		t.Errorf("empty chat prompt = %d, want bare envelope %d", got, ChatEnvelopeOverhead(""))
	}
}

// TestUnknownModelFallsBackWithoutPanicking: an unrecognized model id must not
// crash the audit or return a nonsense baseline.
func TestUnknownModelFallsBackWithoutPanicking(t *testing.T) {
	for _, model := range []string{"", "some-relay-custom-model-v9", strings.Repeat("x", 200)} {
		if got := CountChatPromptToken("", probeTokenPrompt, model); got <= 0 {
			t.Errorf("model %q produced a non-positive baseline %d", model, got)
		}
	}
}
