package pricetest

import (
	"math/rand"
	"strings"
	"testing"
)

// The golden battery (P4) is the quality signal. A relay that recognises probe
// prompts can answer just those correctly (locally, or from a cheap model) while
// degrading everything else — so the battery's value depends on being hard to
// fingerprint. Randomizing only the operands leaves the surrounding sentence
// byte-stable and trivially matchable, which is what these tests guard against.

func TestGoldenPromptsVaryPhrasingNotJustNumbers(t *testing.T) {
	// Strip digits and letters-in-tokens so only the sentence skeleton remains.
	skeletons := make(map[string]struct{})
	for seed := int64(0); seed < 400; seed++ {
		for _, item := range GenerateGoldenItems(AllGoldenGenerators, 4, rand.New(rand.NewSource(seed))) {
			skeletons[promptSkeleton(item.Prompt)] = struct{}{}
		}
	}
	// 4 arithmetic + 4 string-op + 4 unit-conversion phrasings, and the unit
	// conversion nests a second template, so the real count is well above 4.
	if len(skeletons) < 10 {
		t.Errorf("only %d distinct prompt skeletons across 400 seeds — a relay could fingerprint the battery", len(skeletons))
		for skeleton := range skeletons {
			t.Logf("  %s", skeleton)
		}
	}
}

// promptSkeleton removes the randomized payload, leaving the fixed wording.
func promptSkeleton(prompt string) string {
	var builder strings.Builder
	for _, r := range prompt {
		switch {
		case r >= '0' && r <= '9':
			builder.WriteByte('#')
		case r >= 'A' && r <= 'Z':
			builder.WriteByte('$')
		default:
			builder.WriteRune(r)
		}
	}
	// Collapse runs so "###" and "##" compare equal.
	collapsed := builder.String()
	for _, token := range []string{"#", "$"} {
		for strings.Contains(collapsed, token+token) {
			collapsed = strings.ReplaceAll(collapsed, token+token, token)
		}
	}
	return collapsed
}

// TestGoldenAnswersRemainLocallyVerifiable: every generated item must carry an
// expectation, or a wrong answer would silently pass.
func TestGoldenAnswersRemainLocallyVerifiable(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		for _, item := range GenerateGoldenItems(AllGoldenGenerators, 4, rand.New(rand.NewSource(seed))) {
			if item.ExpectRegex == "" && item.ExpectExact == "" {
				t.Fatalf("seed %d produced an item with no expectation: %q", seed, item.Prompt)
			}
			if strings.TrimSpace(item.Prompt) == "" {
				t.Fatalf("seed %d produced an empty prompt", seed)
			}
			// An item whose own expectation cannot match anything is useless.
			if item.ExpectRegex != "" && !MatchGolden(item, extractExpectedToken(item.ExpectRegex)) {
				t.Errorf("seed %d: item cannot match its own expected answer\n  prompt: %s\n  regex:  %s",
					seed, item.Prompt, item.ExpectRegex)
			}
		}
	}
}

// extractExpectedToken pulls the literal answer back out of a \bANSWER\b regex.
func extractExpectedToken(expression string) string {
	trimmed := strings.TrimPrefix(expression, "(?i)")
	trimmed = strings.TrimPrefix(trimmed, `\b`)
	trimmed = strings.TrimSuffix(trimmed, `\b`)
	return strings.ReplaceAll(trimmed, `\`, "")
}

// TestGoldenPhrasingIsSeedDeterministic keeps the evidence reproducible: the
// report records the seed, so the same seed must rebuild the same battery.
func TestGoldenPhrasingIsSeedDeterministic(t *testing.T) {
	for _, seed := range []int64{1, 42, 9999} {
		first := GenerateGoldenItems(AllGoldenGenerators, 4, rand.New(rand.NewSource(seed)))
		second := GenerateGoldenItems(AllGoldenGenerators, 4, rand.New(rand.NewSource(seed)))
		if len(first) != len(second) {
			t.Fatalf("seed %d produced different item counts", seed)
		}
		for i := range first {
			if first[i].Prompt != second[i].Prompt || first[i].ExpectRegex != second[i].ExpectRegex {
				t.Errorf("seed %d item %d is not reproducible:\n  %q\n  %q",
					seed, i, first[i].Prompt, second[i].Prompt)
			}
		}
	}
}
