package tui

// Fuzzy filter for slash-command autocomplete.
//
// Port of upstream `.upstream/current/packages/tui/src/fuzzy.ts`
// (133 LOC). The autocomplete provider needs upstream-parity matching
// (e.g. `/hlp` → `/help`, `opus anthropic` → `provider/id` arg
// completions) which the existing `select_filterable.go` substring
// matcher does not provide.
//
// Algorithm (verbatim from upstream):
//   - All query characters must appear in `text` in order (not
//     necessarily consecutive). Lower score = better match.
//   - Reward consecutive matches (-5 * run length) and word-boundary
//     matches (-10). Penalize gaps (+2 per skipped char) and a tiny
//     positional drift (+0.1 per char-index).
//   - If primary pass fails, retry with letter/digit halves swapped
//     (e.g. "5opus" ↔ "opus5") and add +5 penalty.
//   - `FuzzyFilter` splits the query on runs of whitespace and `/` into
//     AND tokens; each token must match independently.

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
)

// FuzzyMatch is the result of matching a single query against a text.
// Matches=false means the query does not appear in text in order.
type FuzzyMatch struct {
	Matches bool
	Score   float64
}

// FuzzyMatch performs a single-pass fuzzy match: returns Matches=true
// iff every char of `query` appears in `text` in order (case-insensitive).
// Score is lower-is-better.
func FuzzyMatchScore(query, text string) FuzzyMatch {
	q := strings.ToLower(query)
	t := strings.ToLower(text)

	primary := matchQuery(q, t)
	if primary.Matches {
		return primary
	}

	// Letter/digit half swap (e.g. user types "5opus" wanting "opus5").
	swapped := swapAlphaDigit(q)
	if swapped == "" {
		return primary
	}
	sw := matchQuery(swapped, t)
	if !sw.Matches {
		return primary
	}
	return FuzzyMatch{Matches: true, Score: sw.Score + 5}
}

func matchQuery(query, text string) FuzzyMatch {
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
			score -= float64(consecutive) * 5
		} else {
			consecutive = 0
			if lastMatch >= 0 {
				score += float64(i-lastMatch-1) * 2
			}
		}
		if isWordBoundary {
			score -= 10
		}
		score += float64(i) * 0.1
		lastMatch = i
		queryIdx++
	}

	if queryIdx < len(query) {
		return FuzzyMatch{Matches: false}
	}
	if query == text {
		score -= 100
	}
	return FuzzyMatch{Matches: true, Score: score}
}

func isBoundaryChar(r rune) bool {
	switch r {
	case '-', '_', '.', '/', ':':
		return true
	}
	return unicode.IsSpace(r)
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

// isJSWhitespace reports the characters JavaScript's `\s` class and
// String.prototype.trim treat as whitespace: Unicode White_Space without
// U+0085 NEXT LINE, plus the U+FEFF byte-order mark.
func isJSWhitespace(r rune) bool {
	return r == '\uFEFF' || (r != '\u0085' && unicode.IsSpace(r))
}

func isASCIILetter(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }
func isASCIIDigit(r rune) bool  { return r >= '0' && r <= '9' }

// FuzzyFilter sorts items best-match-first and drops non-matchers.
// Tokens (separated by whitespace or `/`, upstream `split(/[\s/]+/)`) AND
// together: each token must match, so "openai-codex/gpt-5.5" matches the
// reordered text "gpt-5.5 openai-codex". Empty/whitespace-only query returns
// items unchanged.
func FuzzyFilter[T any](items []T, query string, getText func(T) string) []T {
	q := strings.TrimFunc(query, isJSWhitespace)
	if q == "" {
		out := make([]T, len(items))
		copy(out, items)
		return out
	}
	tokens := strings.FieldsFunc(q, func(r rune) bool { return r == '/' || isJSWhitespace(r) })
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
