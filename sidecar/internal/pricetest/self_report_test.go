package pricetest

import (
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
			// The reply snippet is always retained for auditability.
			assert.Contains(t, res.Evidence, "reply")
		})
	}
}
