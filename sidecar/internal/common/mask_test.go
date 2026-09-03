package common

import (
	"strings"
	"testing"
)

// Evidence redaction is a privacy control, not cosmetics. Probe evidence quotes
// upstream error bodies verbatim, and relays routinely echo the submitted
// credential back inside them. That evidence lands in a PDF the user may share
// publicly as proof of fraud, so a leaked secret here is published, not merely
// logged.
//
// The bar these tests enforce: after masking, no usable credential remains, but
// enough of the prefix survives to say WHICH credential family leaked.

func TestMaskRemovesCredentialFamilies(t *testing.T) {
	// Fixtures are assembled at runtime from a prefix plus filler. The masker
	// sees a complete, realistic token, but no literal credential string exists
	// in this source file — otherwise secret scanners flag the test fixtures as
	// leaked keys and (rightly) block the push.
	fake := func(prefix string, bodyLen int) string {
		return prefix + strings.Repeat("x", bodyLen)
	}

	secrets := map[string]struct {
		raw      string
		keepStub string
	}{
		"openai user key":   {fake("sk-", 24), "sk-***"},
		"openai project":    {fake("sk-proj-", 24), "sk-***"},
		"anthropic":         {fake("sk-ant-api03-", 32), "sk-ant-***"},
		"google":            {fake("AIza", 35), "AIza***"},
		"aws access key":    {"AKIA" + strings.Repeat("Q", 16), "AKIA***"},
		"aws session key":   {"ASIA" + strings.Repeat("Q", 16), "ASIA***"},
		"github classic":    {fake("ghp_", 36), "ghp_***"},
		"github fine grain": {fake("github_pat_", 40), "github_pat_***"},
		"slack bot":         {fake("xoxb-", 24), "xoxb-***"},
	}

	for name, item := range secrets {
		t.Run(name, func(t *testing.T) {
			masked := MaskSensitiveInfo("upstream error: invalid credential " + item.raw + " rejected")
			// The token body must not survive. Check the tail, which is the part
			// an attacker needs and the part a prefix-only stub must have dropped.
			body := strings.TrimPrefix(item.raw, item.keepStub[:len(item.keepStub)-3])
			if len(body) > 6 && strings.Contains(masked, body[len(body)-6:]) {
				t.Fatalf("secret body survived masking\n  raw:    %s\n  masked: %s", item.raw, masked)
			}
			if strings.Contains(masked, item.raw) {
				t.Fatalf("credential passed through verbatim: %s", masked)
			}
			if !strings.Contains(masked, item.keepStub) {
				t.Errorf("masked output should keep the family stub %q so the reader knows what leaked, got: %s", item.keepStub, masked)
			}
		})
	}
}

func TestMaskRemovesAuthorizationHeaders(t *testing.T) {
	// Assembled at runtime for the same reason as above.
	bearerToken := strings.Repeat("a", 20) + "." + strings.Repeat("b", 10)
	basicBlob := strings.Repeat("Z", 28) + "=="
	jwt := "eyJ" + strings.Repeat("h", 16) + "." + strings.Repeat("p", 20) + "." + strings.Repeat("s", 12)

	cases := []struct{ name, raw, forbidden string }{
		{"bearer", `{"error":"bad token","header":"Authorization: Bearer ` + bearerToken + `"}`, bearerToken},
		{"basic", "Authorization: Basic " + basicBlob, basicBlob},
		{"jwt", "token " + jwt, jwt},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			masked := MaskSensitiveInfo(item.raw)
			if strings.Contains(masked, item.forbidden) {
				t.Fatalf("credential survived masking: %s", masked)
			}
		})
	}
}

func TestMaskCollapsesHostsAndAddresses(t *testing.T) {
	cases := []struct{ raw, forbidden string }{
		{"connect failed to https://relay.cheap-gpt.example.com/v1/chat", "cheap-gpt"},
		{"upstream at 203.0.113.42 refused", "203.0.113.42"},
		{"host api.internal-relay.net unreachable", "internal-relay"},
	}
	for _, item := range cases {
		masked := MaskSensitiveInfo(item.raw)
		if strings.Contains(masked, item.forbidden) {
			t.Errorf("host/address leaked: %q -> %q", item.raw, masked)
		}
	}
}

// TestMaskLeavesOrdinaryTextIntact is the counterweight: an over-eager masker
// destroys the diagnostic value of the evidence it is protecting.
func TestMaskLeavesOrdinaryTextIntact(t *testing.T) {
	for _, text := range []string{
		"context length exceeded: reduce max_tokens",
		"the model returned 42 tokens but the recount says 40",
		"rate limit reached, retry after 30 seconds",
		"上游返回了空响应",
	} {
		if masked := MaskSensitiveInfo(text); masked != text {
			t.Errorf("ordinary diagnostic text was altered:\n  in:  %s\n  out: %s", text, masked)
		}
	}
}

func TestTruncateRunesNeverSplitsCharacters(t *testing.T) {
	// A byte-slice truncation of CJK text produces mojibake in the PDF. Every
	// result must remain valid UTF-8 with the expected rune count.
	const cjk = "货源体检报告证据字段需要按字符截断而不是按字节"
	for _, limit := range []int{1, 5, 10, 200} {
		got := TruncateRunes(cjk, limit)
		runes := []rune(cjk)
		want := len(runes)
		if limit < want {
			want = limit
		}
		if gotRunes := []rune(got); len(gotRunes) != want {
			t.Errorf("TruncateRunes(limit=%d) kept %d runes, want %d", limit, len(gotRunes), want)
		}
		if !isValidUTF8(got) {
			t.Errorf("TruncateRunes(limit=%d) produced invalid UTF-8: %q", limit, got)
		}
	}

	if got := TruncateRunes("short", 100); got != "short" {
		t.Errorf("under-limit input must pass through unchanged, got %q", got)
	}
	if got := TruncateRunes("anything", 0); got != "" {
		t.Errorf("zero limit should yield empty string, got %q", got)
	}
}

func isValidUTF8(value string) bool {
	for _, r := range value {
		if r == '�' {
			return false
		}
	}
	return true
}

// TestMaskIsIdempotent guards against a second masking pass mangling the stubs
// left by the first — evidence can flow through more than one redaction point.
func TestMaskIsIdempotent(t *testing.T) {
	raw := "key sk-" + strings.Repeat("x", 24) + " at https://relay.example.com/v1"
	once := MaskSensitiveInfo(raw)
	twice := MaskSensitiveInfo(once)
	if once != twice {
		t.Errorf("masking is not idempotent:\n  once:  %s\n  twice: %s", once, twice)
	}
}
