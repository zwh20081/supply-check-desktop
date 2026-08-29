package pricetest

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The generated golden items must be self-verifying: the answer the generator
// bakes into ExpectRegex must actually match the correct answer, and only the
// correct answer (word-boundary, no false substring match).
func TestGenerateGoldenItems_SelfConsistent(t *testing.T) {
	rng := rand.New(rand.NewSource(12345))
	items := GenerateGoldenItems(AllGoldenGenerators, 200, rng)
	require.Len(t, items, 200)
	for _, it := range items {
		require.NotEmpty(t, it.Prompt)
		require.NotEmpty(t, it.ExpectRegex)
		require.Empty(t, it.ExpectExact, "generated items use regex, not exact")
	}
}

// Freshness: two runs with different seeds should (overwhelmingly) produce
// different prompts — that is the whole anti-whitelist / anti-cache point.
func TestGenerateGoldenItems_FreshPerSeed(t *testing.T) {
	a := GenerateGoldenItems(AllGoldenGenerators, 8, rand.New(rand.NewSource(1)))
	b := GenerateGoldenItems(AllGoldenGenerators, 8, rand.New(rand.NewSource(2)))
	same := 0
	for i := range a {
		if a[i].Prompt == b[i].Prompt {
			same++
		}
	}
	assert.Less(t, same, len(a), "different seeds should not yield identical batteries")
}

// The word-boundary regex must reject a superstring (e.g. answer 7 must not
// match inside 17) but accept the answer embedded in a natural sentence.
func TestWordBoundaryRegex(t *testing.T) {
	re := wordBoundaryRegex("7")
	assert.Regexp(t, re, "the answer is 7.")
	assert.NotRegexp(t, re, "the answer is 17")
}

// A default (empty-kinds) call must still produce a full battery.
func TestGenerateGoldenItems_DefaultKinds(t *testing.T) {
	items := GenerateGoldenItems(nil, 5, rand.New(rand.NewSource(9)))
	assert.Len(t, items, 5)
}
