package tui

import (
	"reflect"
	"testing"
)

func TestFuzzyMatchScore_ExactPrefixScoreParity(t *testing.T) {
	got := FuzzyMatchScore("he", "help")
	want := FuzzyMatch{Matches: true, Score: -24.9}
	if got != want {
		t.Fatalf("FuzzyMatchScore(he, help) = %+v want %+v", got, want)
	}
}

func TestFuzzyMatchScore_GapPenaltyParity(t *testing.T) {
	got := FuzzyMatchScore("hp", "help")
	want := FuzzyMatch{Matches: true, Score: -10.7}
	if got != want {
		t.Fatalf("FuzzyMatchScore(hp, help) = %+v want %+v", got, want)
	}
}

func TestFuzzyMatchScore_EmptyAndTooLong(t *testing.T) {
	if got := FuzzyMatchScore("", "help"); got != (FuzzyMatch{Matches: true, Score: 0}) {
		t.Fatalf("empty query = %+v want zero match", got)
	}
	if got := FuzzyMatchScore("helpers", "help"); got != (FuzzyMatch{Matches: false, Score: 0}) {
		t.Fatalf("too-long query = %+v want no match", got)
	}
}

func TestFuzzyMatchScore_SwapsAlphaDigitHalves(t *testing.T) {
	got := FuzzyMatchScore("5opus", "opus5")
	want := FuzzyMatch{Matches: true, Score: -179}
	if got != want {
		t.Fatalf("FuzzyMatchScore(5opus, opus5) = %+v want %+v", got, want)
	}
}

func TestFuzzyMatchScore_NoSwapForMixedSymbols(t *testing.T) {
	got := FuzzyMatchScore("op-5", "5op")
	if got.Matches {
		t.Fatalf("mixed symbol query should not alpha-digit swap, got %+v", got)
	}
}

func TestFuzzyFilter_StableOrderOnTies(t *testing.T) {
	items := []string{"abc1", "abc2", "xyz"}
	got := FuzzyFilter(items, "abc", func(s string) string { return s })
	want := []string{"abc1", "abc2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FuzzyFilter stable tie order = %#v want %#v", got, want)
	}
}

func TestFuzzyFilter_TokensMustAllMatch(t *testing.T) {
	items := []string{"gpt-4o openai", "gpt-4o github-copilot", "claude anthropic"}
	got := FuzzyFilter(items, "gpt openai", func(s string) string { return s })
	want := []string{"gpt-4o openai"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FuzzyFilter token AND = %#v want %#v", got, want)
	}
}

// Mirrors upstream fuzzy.test.ts "matches slash-separated provider/model
// queries against reordered text": the query splits on `/` as well as
// whitespace.
func TestFuzzyFilter_MatchesSlashSeparatedProviderModelQueriesAgainstReorderedText(t *testing.T) {
	type model struct{ id, provider string }
	item := model{id: "gpt-5.5", provider: "openai-codex"}
	got := FuzzyFilter([]model{item}, "openai-codex/gpt-5.5", func(m model) string { return m.id + " " + m.provider })
	if !reflect.DeepEqual(got, []model{item}) {
		t.Fatalf("FuzzyFilter slash tokens = %#v want %#v", got, []model{item})
	}
}

func TestFuzzyFilter_SlashOnlyQueryReturnsItemsUnchanged(t *testing.T) {
	items := []string{"b", "a"}
	for _, query := range []string{"/", " / ", "\uFEFF"} {
		got := FuzzyFilter(items, query, func(s string) string { return s })
		if !reflect.DeepEqual(got, items) {
			t.Fatalf("FuzzyFilter(%q) = %#v want %#v", query, got, items)
		}
	}
}

func TestFuzzyFilterPreservesNELAsQueryText(t *testing.T) {
	items := []string{"alpha beta", "alpha\u0085beta", "beta alpha"}
	for _, query := range []string{"\u0085", "alpha\u0085beta"} {
		got := FuzzyFilter(items, query, func(value string) string { return value })
		if want := []string{"alpha\u0085beta"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("FuzzyFilter(%q) = %q, want %q", query, got, want)
		}
	}
	got := FuzzyFilter([]string{"beta alpha"}, "alpha\uFEFFbeta", func(value string) string { return value })
	if !reflect.DeepEqual(got, []string{"beta alpha"}) {
		t.Fatalf("BOM must remain a token separator: %q", got)
	}
}
