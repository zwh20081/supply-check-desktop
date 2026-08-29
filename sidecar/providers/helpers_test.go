package providers

import "testing"

func TestStripVersionSuffix(t *testing.T) {
	if got := stripVersionSuffix("https://api.anthropic.com/v1/", "v1"); got != "https://api.anthropic.com" {
		t.Fatalf("unexpected Anthropic SDK base URL: %s", got)
	}
}

func TestSplitGoogleBaseURL(t *testing.T) {
	base, version := splitGoogleBaseURL("https://generativelanguage.googleapis.com/v1beta")
	if base != "https://generativelanguage.googleapis.com" || version != "v1beta" {
		t.Fatalf("unexpected Google SDK endpoint: %s %s", base, version)
	}
}
