package tui

import "github.com/MichaelKinsy/PiG/extensions/sdk/tui/internal/engine"

// FuzzyMatchResult is pi-tui's FuzzyMatch: whether every query character appears in order, and the score, lower being better.
type FuzzyMatchResult struct {
	Matches bool
	Score   float64
}

// FuzzyMatch is pi-tui's fuzzyMatch.
func FuzzyMatch(query, text string) FuzzyMatchResult {
	r := engine.FuzzyMatchScore(query, text)
	return FuzzyMatchResult{Matches: r.Matches, Score: r.Score}
}

// FuzzyFilter is pi-tui's fuzzyFilter: the items whose text matches every whitespace- or slash-separated token of query, best first.
// A blank query returns items unchanged.
func FuzzyFilter[T any](items []T, query string, getText func(T) string) []T {
	return engine.FuzzyFilter(items, query, getText)
}
