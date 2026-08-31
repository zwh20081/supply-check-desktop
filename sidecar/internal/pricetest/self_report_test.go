package pricetest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"supply-check-sdk/internal/model"
)

func TestProbeSelfReport(t *testing.T) {
	cases := []struct {
		name     string
		model    string
		reply    string
		want     string
		wantEvid string // an evidence key that must be present (or "")
	}{
		{
			name:  "clean claude names anthropic",
			model: "claude-sonnet-4-6",
			reply: "I'm Claude, a large language model made by Anthropic.",
			want:  model.ProbeStatusPass,
		},
		{
			name:  "clean gpt names openai",
			model: "gpt-5",
			reply: "I am ChatGPT, developed by OpenAI.",
			want:  model.ProbeStatusPass,
		},
		{
			name:  "clean gemini names google",
			model: "gemini-3.1-pro",
			reply: "I'm Gemini, a model built by Google DeepMind.",
			want:  model.ProbeStatusPass,
		},
		{
			name:     "kiro wrapper on claude channel warns",
			model:    "claude-sonnet-4-5-20250929",
			reply:    "I am Kiro, an AI assistant.",
			want:     model.ProbeStatusWarn,
			wantEvid: "wrapper_marker",
		},
		{
			name:     "cursor wrapper warns even if honest about claude",
			model:    "claude-opus-4-8",
			reply:    "I'm Claude, running inside Cursor to help you code.",
			want:     model.ProbeStatusWarn, // wrapper takes precedence over the honest vendor mention
			wantEvid: "wrapper_marker",
		},
		{
			name:     "vendor swap claude->openai fails",
			model:    "claude-sonnet-4-6",
			reply:    "I'm ChatGPT, a language model trained by OpenAI.",
			want:     model.ProbeStatusFail,
			wantEvid: "claimed_vendor",
		},
		{
			name:     "foreign open model on gpt channel warns",
			model:    "gpt-5",
			reply:    "I am Qwen, developed by Alibaba Cloud.",
			want:     model.ProbeStatusWarn,
			wantEvid: "foreign_model",
		},
		{
			name:  "honest comparison is not a swap",
			model: "claude-sonnet-4-6",
			reply: "I'm Claude by Anthropic, similar to ChatGPT but a different model.",
			want:  model.ProbeStatusPass, // own vendor named → the ChatGPT mention is just a comparison
		},
		{
			name:  "empty reply skips",
			model: "claude-sonnet-4-6",
			reply: "   ",
			want:  model.ProbeStatusSkip,
		},
		{
			name:  "non-big-three model has no baseline",
			model: "qwen-max",
			reply: "I am an AI assistant.",
			want:  model.ProbeStatusSkip,
		},
		{
			name:  "terse reply without vendor is not punished",
			model: "claude-sonnet-4-6",
			reply: "I'm a helpful AI assistant here to answer your questions.",
			want:  model.ProbeStatusSkip,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := ProbeSelfReport(model.ProbeSpec{}, SelfReportObs{
				RequestedModel: tc.model,
				Content:        tc.reply,
			})
			assert.Equal(t, tc.want, res.Status, "status")
			if tc.wantEvid != "" {
				assert.Contains(t, res.Evidence, tc.wantEvid, "evidence key")
			}
			// A safe reply representation is always retained for auditability.
			assert.Contains(t, res.Evidence, "reply")
		})
	}
}

func TestQuality_AmbiguousWrapperWordsRequireWrapperContext(t *testing.T) {
	benign := []string{
		"I'm Claude by Anthropic, designed to augment human capability.",
		"I'm Claude by Anthropic; a cursor marks the current position.",
		"I'm Claude by Anthropic; perplexity is a statistical measure.",
		"I'm Claude by Anthropic and aim to be a lovable assistant.",
	}
	for _, reply := range benign {
		result := ProbeSelfReport(model.ProbeSpec{}, SelfReportObs{
			RequestedModel: "claude-sonnet-4-6", Content: reply,
		})
		assert.Equal(t, model.ProbeStatusPass, result.Status, reply)
		assert.NotContains(t, result.Evidence, "wrapper_marker")
	}

	contextual := map[string]string{
		"I'm Claude by Anthropic, running inside Cursor.":        "cursor",
		"I'm Claude by Anthropic via (PoE).":                     "poe",
		"I'm Claude by Anthropic using the Windsurf IDE.":        "windsurf",
		"Cursor is a pointer; I'm Claude running inside Cursor.": "cursor",
		"I'm Claude running inside the Cursor application.":      "cursor",
		"I'm Microsoft Copilot.":                                 "microsoft copilot",
	}
	for reply, marker := range contextual {
		result := ProbeSelfReport(model.ProbeSpec{}, SelfReportObs{
			RequestedModel: "claude-sonnet-4-6", Content: reply,
		})
		assert.Equal(t, model.ProbeStatusWarn, result.Status, reply)
		assert.Equal(t, marker, result.Evidence["wrapper_marker"])
	}
}

func TestQuality_SelfReportVendorNamesRequireUnicodeWordBoundaries(t *testing.T) {
	result := ProbeSelfReport(model.ProbeSpec{}, SelfReportObs{
		RequestedModel: "gpt-5",
		Content:        "I am an AI assistant and do not want to bombard you.",
	})

	assert.NotEqual(t, model.ProbeStatusFail, result.Status)
	assert.NotContains(t, result.Evidence, "claimed_vendor")
}

func TestQuality_SelfReportDistinguishesVendorClaimsFromMentions(t *testing.T) {
	benign := []string{
		"I'm an AI assistant, not ChatGPT.",
		"I'm Claude by Anthropic, unlike ChatGPT.",
		"I can compare results with OpenAI models.",
		"I am an AI assistant, not a bard; I generate prose.",
		"I was not created by OpenAI.",
	}
	for _, reply := range benign {
		result := ProbeSelfReport(model.ProbeSpec{}, SelfReportObs{
			RequestedModel: "claude-sonnet-4-6", Content: reply,
		})
		assert.NotEqual(t, model.ProbeStatusFail, result.Status, reply)
		assert.NotContains(t, result.Evidence, "claimed_vendor", reply)
	}

	swapped := ProbeSelfReport(model.ProbeSpec{}, SelfReportObs{
		RequestedModel: "claude-sonnet-4-6",
		Content:        "I am ChatGPT by OpenAI, comparable to Claude.",
	})
	assert.Equal(t, model.ProbeStatusFail, swapped.Status)
	assert.Equal(t, "openai", swapped.Evidence["claimed_vendor"])

	honest := ProbeSelfReport(model.ProbeSpec{}, SelfReportObs{
		RequestedModel: "claude-sonnet-4-6",
		Content:        "I am Claude by Anthropic, unlike ChatGPT.",
	})
	assert.Equal(t, model.ProbeStatusPass, honest.Status)
}

func TestQuality_SuspiciousSelfReportDoesNotPersistRawReply(t *testing.T) {
	const secret = "INTERNAL-SYSTEM-POLICY-72ac"
	result := ProbeSelfReport(model.ProbeSpec{}, SelfReportObs{
		RequestedModel: "claude-sonnet-4-6",
		Content:        "I'm Claude, running inside Cursor. " + secret,
	})

	assert.Equal(t, model.ProbeStatusWarn, result.Status)
	assert.Equal(t, redactedSelfReport, result.Evidence["reply"])
	assert.Equal(t, len([]rune("I'm Claude, running inside Cursor. "+secret)), result.Evidence["reply_chars"])
	encoded, err := json.Marshal(result.Evidence)
	assert.NoError(t, err)
	assert.NotContains(t, string(encoded), secret)
}

func TestQuality_SafeAndSkippedSelfReportsDoNotPersistRawReply(t *testing.T) {
	const secret = "INTERNAL-SYSTEM-POLICY-c10e"
	for name, observation := range map[string]SelfReportObs{
		"confirmed vendor": {
			RequestedModel: "claude-sonnet-4-6",
			Content:        "I am Claude by Anthropic. " + secret,
		},
		"unrecognized family": {
			RequestedModel: "custom-model",
			Content:        "A generic assistant. " + secret,
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := ProbeSelfReport(model.ProbeSpec{}, observation)
			encoded, err := json.Marshal(result.Evidence)
			assert.NoError(t, err)
			assert.NotContains(t, string(encoded), secret)
			assert.Equal(t, redactedSelfReport, result.Evidence["reply"])
			assert.Equal(t, len([]rune(strings.TrimSpace(observation.Content))), result.Evidence["reply_chars"])
		})
	}
}

func TestQuality_SelfReportWrapperMarkersRequireUnicodeWordBoundaries(t *testing.T) {
	cases := []struct {
		name          string
		reply         string
		want          string
		wantWrapper   bool
		wrapperMarker string
	}{
		{
			name:  "cline embedded in decline is not a wrapper",
			reply: "I'm Claude by Anthropic, and I decline unsafe requests.",
			want:  model.ProbeStatusPass,
		},
		{
			name:  "poe embedded in poetry is not a wrapper",
			reply: "I'm Claude by Anthropic, and I can discuss poetry.",
			want:  model.ProbeStatusPass,
		},
		{
			name:  "unicode letters do not create a false boundary",
			reply: "I'm Claude by Anthropic; 前Cline后 is merely part of this token.",
			want:  model.ProbeStatusPass,
		},
		{
			name:          "standalone cline with punctuation warns",
			reply:         "I'm Claude by Anthropic, running in CLINE.",
			want:          model.ProbeStatusWarn,
			wantWrapper:   true,
			wrapperMarker: "cline",
		},
		{
			name:          "standalone poe is case insensitive",
			reply:         "I'm Claude by Anthropic via (PoE)!",
			want:          model.ProbeStatusWarn,
			wantWrapper:   true,
			wrapperMarker: "poe",
		},
		{
			name:          "dotted product phrase remains detectable",
			reply:         "I'm Claude by Anthropic through BOLT.NEW, the coding agent.",
			want:          model.ProbeStatusWarn,
			wantWrapper:   true,
			wrapperMarker: "bolt.new",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := ProbeSelfReport(model.ProbeSpec{}, SelfReportObs{
				RequestedModel: "claude-sonnet-4-6",
				Content:        tc.reply,
			})
			assert.Equal(t, tc.want, res.Status)
			if tc.wantWrapper {
				assert.Equal(t, tc.wrapperMarker, res.Evidence["wrapper_marker"])
			} else {
				assert.NotContains(t, res.Evidence, "wrapper_marker")
			}
		})
	}
}
