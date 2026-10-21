// Code generated from tui/fuzzy.go by tui/sdk_tui_copy_test.go; DO NOT EDIT.

package engine

// Ports packages/tui/src/fuzzy.ts.
// Lower scores reward consecutive and word-boundary matches; gaps and later UTF-16 positions add penalties.

import (
	"cmp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui/internal/jsstring"
)

// FuzzyMatch is the result of matching a single query against a text.
// Matches=false means the query does not appear in text in order.
type FuzzyMatch struct {
	Matches bool
	Score   float64
}

// FuzzyMatchScore matches an ordered, case-insensitive UTF-16 subsequence and returns Pi's lower-is-better score.
func FuzzyMatchScore(query, text string) FuzzyMatch {
	return NewFuzzyText(text).Match(NewFuzzyQuery(query))
}

// FuzzyText is a text prepared for matching many queries: its lowercase UTF-16 units.
type FuzzyText struct{ units []uint16 }

// NewFuzzyText prepares text for Match.
func NewFuzzyText(text string) FuzzyText {
	return FuzzyText{units: jsstring.ToUTF16(jsstring.ToLower(text))}
}

// FuzzyQuery is a query prepared for matching many texts: its lowercase UTF-16 units and those of its letter/digit swap.
type FuzzyQuery struct {
	units, swapped []uint16
	hasSwap        bool
}

// NewFuzzyQuery prepares query for Match.
func NewFuzzyQuery(query string) FuzzyQuery {
	q := jsstring.ToLower(query)
	prepared := FuzzyQuery{units: jsstring.ToUTF16(q)}
	// Letter/digit half swap (e.g. user types "5opus" wanting "opus5").
	if swapped := swapAlphaDigit(q); swapped != "" {
		prepared.swapped, prepared.hasSwap = jsstring.ToUTF16(swapped), true
	}
	return prepared
}

// Match is FuzzyMatchScore of the query and the text.
func (t FuzzyText) Match(query FuzzyQuery) FuzzyMatch {
	primary := matchQuery(query.units, t.units)
	if primary.Matches || !query.hasSwap {
		return primary
	}
	sw := matchQuery(query.swapped, t.units)
	if !sw.Matches {
		return primary
	}
	return FuzzyMatch{Matches: true, Score: sw.Score + 5}
}

func matchQuery(query, text []uint16) FuzzyMatch {
	if len(query) == 0 {
		return FuzzyMatch{Matches: true, Score: 0}
	}
	if len(query) > len(text) {
		return FuzzyMatch{Matches: false}
	}

	queryIdx := 0
	score := 0.0
	lastMatch := -1
	consecutive := 0

	for i := 0; i < len(text) && queryIdx < len(query); i++ {
		if text[i] != query[queryIdx] {
			continue
		}
		isWordBoundary := i == 0 || isBoundaryChar(rune(text[i-1]))
		if lastMatch == i-1 {
			consecutive++
			score -= float64(float64(consecutive) * 5)
		} else {
			consecutive = 0
			if lastMatch >= 0 {
				score += float64(float64(i-lastMatch-1) * 2)
			}
		}
		if isWordBoundary {
			score -= 10
		}
		// float64() forces the product to round before the add: arm64 fuses x*y+z into one FMA rounding, which JS never does.
		score += float64(float64(i) * 0.1)
		lastMatch = i
		queryIdx++
	}

	if queryIdx < len(query) {
		return FuzzyMatch{Matches: false}
	}
	if slices.Equal(query, text) {
		score -= 100
	}
	return FuzzyMatch{Matches: true, Score: score}
}

func isBoundaryChar(r rune) bool {
	switch r {
	case '-', '_', '.', '/', ':':
		return true
	}
	return jsstring.IsSpace(r)
}

// swapAlphaDigit handles the upstream convenience case: a query
// composed of letters-then-digits or digits-then-letters is retried
// with the halves swapped. Returns "" if the query doesn't match
// either pattern.
func swapAlphaDigit(q string) string {
	if q == "" {
		return ""
	}
	// letters+digits?
	splitAt := -1
	mode := 0 // 0=unknown, 1=letters-first, 2=digits-first
	for i, r := range q {
		switch mode {
		case 0:
			switch {
			case isASCIILetter(r):
				mode = 1
			case isASCIIDigit(r):
				mode = 2
			default:
				return ""
			}
		case 1:
			if isASCIIDigit(r) {
				splitAt = i
				mode = 3 // verifying tail is all digits
			} else if !isASCIILetter(r) {
				return ""
			}
		case 2:
			if isASCIILetter(r) {
				splitAt = i
				mode = 4 // verifying tail is all letters
			} else if !isASCIIDigit(r) {
				return ""
			}
		case 3:
			if !isASCIIDigit(r) {
				return ""
			}
		case 4:
			if !isASCIILetter(r) {
				return ""
			}
		}
	}
	if splitAt < 0 {
		return ""
	}
	return q[splitAt:] + q[:splitAt]
}

func isASCIILetter(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }
func isASCIIDigit(r rune) bool  { return r >= '0' && r <= '9' }

// FuzzyFilter sorts items best-match-first and drops non-matchers.
// Tokens (separated by whitespace or `/`, upstream `split(/[\s/]+/)`) AND
// together: each token must match, so "openai-codex/gpt-5.5" matches the
// reordered text "gpt-5.5 openai-codex". Empty/whitespace-only query returns
// items unchanged.
func FuzzyFilter[T any](items []T, query string, getText func(T) string) []T {
	q := strings.TrimFunc(query, jsstring.IsSpace)
	if q == "" {
		out := make([]T, len(items))
		copy(out, items)
		return out
	}
	tokens := strings.FieldsFunc(q, func(r rune) bool { return r == '/' || jsstring.IsSpace(r) })
	if len(tokens) == 0 {
		out := make([]T, len(items))
		copy(out, items)
		return out
	}

	type scored struct {
		item  T
		idx   int // for stable sort
		score float64
	}
	results := make([]scored, 0, len(items))
	for i, it := range items {
		text := getText(it)
		total := 0.0
		ok := true
		for _, tok := range tokens {
			m := FuzzyMatchScore(tok, text)
			if !m.Matches {
				ok = false
				break
			}
			total += m.Score
		}
		if ok {
			results = append(results, scored{item: it, idx: i, score: total})
		}
	}
	slices.SortStableFunc(results, func(a, b scored) int {
		if c := cmp.Compare(a.score, b.score); c != 0 {
			return c
		}
		return cmp.Compare(a.idx, b.idx)
	})
	out := make([]T, len(results))
	for i, r := range results {
		out[i] = r.item
	}
	return out
}
