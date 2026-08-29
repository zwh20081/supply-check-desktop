package pricetest

import (
	"fmt"
	"math/rand"
	"regexp"
	"strconv"

	"supply-check-sdk/internal/model"
)

// Programmatic golden generators produce randomized Q&A items whose answers are
// computed locally, so the golden battery (P4) is different on every run. This
// defeats two known evasions at once (arXiv:2603.01919 shadow-API market):
//   - an upstream that whitelists fixed probe prompts and only cheats the rest;
//   - an upstream that caches golden answers to fake a perfect pass rate — a
//     fresh prompt every run can never be a cache hit.
//
// Design follows the GSM-Symbolic / DyVal "programmatic benchmark" family
// (arXiv:2502.17521): a template with random slots, answer verifiable from the
// slots. Items are kept easy enough that any genuine chat model answers
// correctly (so a failure is a real quality signal, not puzzle difficulty).

// Golden generator kinds. Admins enable a subset via ProbeSpec.GoldenGenerators.
const (
	GoldenGenArithmetic = "arithmetic"
	GoldenGenStringOp   = "string_op"
	GoldenGenUnitConv   = "unit_convert"
)

// AllGoldenGenerators is the default draw set when none is specified.
var AllGoldenGenerators = []string{GoldenGenArithmetic, GoldenGenStringOp, GoldenGenUnitConv}

// wordBoundaryRegex matches answer as a standalone alphanumeric token. Answers
// here are always alnum, so \b works (digits/letters are \w): `\b7\b` matches
// "the answer is 7." but not "17". QuoteMeta guards any odd characters. RE2 has
// no look-around, so \b is the right tool.
func wordBoundaryRegex(answer string) string {
	return `\b` + regexp.QuoteMeta(answer) + `\b`
}

// GenerateGoldenItems returns count fresh golden items drawn from kinds, using
// the supplied rand source (the caller records the seed for audit). Every
// item's expectation is a locally-computed answer.
func GenerateGoldenItems(kinds []string, count int, rng *rand.Rand) []model.GoldenItem {
	if count <= 0 {
		count = 4
	}
	active := kinds
	if len(active) == 0 {
		active = AllGoldenGenerators
	}
	items := make([]model.GoldenItem, 0, count)
	for i := 0; i < count; i++ {
		switch active[rng.Intn(len(active))] {
		case GoldenGenStringOp:
			items = append(items, genStringOp(rng))
		case GoldenGenUnitConv:
			items = append(items, genUnitConvert(rng))
		default: // GoldenGenArithmetic
			items = append(items, genArithmetic(rng))
		}
	}
	return items
}

// genArithmetic keeps every answer < 1000 so no model formats it with a
// thousands separator (which would break the \b answer \b match).
func genArithmetic(rng *rand.Rand) model.GoldenItem {
	if rng.Intn(2) == 0 {
		// multiplication: operands 8..29 → product ≤ 841
		a := rng.Intn(22) + 8
		b := rng.Intn(22) + 8
		ans := strconv.Itoa(a * b)
		return model.GoldenItem{
			Prompt:      fmt.Sprintf("What is %d multiplied by %d? Reply with only the number, no words.", a, b),
			ExpectRegex: wordBoundaryRegex(ans),
		}
	}
	// addition: operands 100..499 → sum ≤ 998
	a := rng.Intn(400) + 100
	b := rng.Intn(400) + 100
	ans := strconv.Itoa(a + b)
	return model.GoldenItem{
		Prompt:      fmt.Sprintf("What is %d plus %d? Reply with only the number, no words.", a, b),
		ExpectRegex: wordBoundaryRegex(ans),
	}
}

// genStringOp asks the model to reverse a random uppercase token. The token is
// drawn from letters (no vowels-only ambiguity, no dictionary word) so there is
// nothing to memorize.
func genStringOp(rng *rand.Rand) model.GoldenItem {
	const letters = "ABCDEFGHJKLMNPQRSTUVWXYZ"
	n := rng.Intn(3) + 5 // 5..7 chars
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rng.Intn(len(letters))]
	}
	word := string(b)
	rev := reverseASCII(word)
	return model.GoldenItem{
		Prompt:      fmt.Sprintf("Reverse the letters of %s and reply with only the reversed string.", word),
		ExpectRegex: `(?i)` + wordBoundaryRegex(rev),
	}
}

// genUnitConvert asks a small exact unit conversion (answer < 2200, comma-free).
func genUnitConvert(rng *rand.Rand) model.GoldenItem {
	n := rng.Intn(20) + 2 // 2..21
	units := []struct {
		q      string
		factor int
	}{
		{"seconds are in %d minutes", 60},
		{"minutes are in %d hours", 60},
		{"cents are in %d dollars", 100},
	}
	u := units[rng.Intn(len(units))]
	ans := strconv.Itoa(n * u.factor)
	return model.GoldenItem{
		Prompt:      fmt.Sprintf("How many "+u.q+"? Reply with only the number.", n),
		ExpectRegex: wordBoundaryRegex(ans),
	}
}

func reverseASCII(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
