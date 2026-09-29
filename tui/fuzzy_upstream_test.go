package tui

import (
	"slices"
	"testing"
)

func TestUpstreamFuzzyMatch(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:6
	t.Run("empty query matches everything with score 0", func(t *testing.T) {
		if got := FuzzyMatchScore("", "anything"); got != (FuzzyMatch{Matches: true, Score: 0}) {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:12
	t.Run("query longer than text does not match", func(t *testing.T) {
		if got := FuzzyMatchScore("longquery", "short"); got.Matches {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:17
	t.Run("exact match has good score", func(t *testing.T) {
		if got := FuzzyMatchScore("test", "test"); !got.Matches || got.Score >= 0 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:23
	t.Run("characters must appear in order", func(t *testing.T) {
		if !FuzzyMatchScore("abc", "aXbXc").Matches || FuzzyMatchScore("abc", "cba").Matches {
			t.Fatal("ordered subsequence mismatch")
		}
	})
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:31
	t.Run("case insensitive matching", func(t *testing.T) {
		if !FuzzyMatchScore("ABC", "abc").Matches || !FuzzyMatchScore("abc", "ABC").Matches {
			t.Fatal("case folding mismatch")
		}
	})
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:39
	t.Run("consecutive matches score better than scattered matches", func(t *testing.T) {
		consecutive, scattered := FuzzyMatchScore("foo", "foobar"), FuzzyMatchScore("foo", "f_o_o_bar")
		if !consecutive.Matches || !scattered.Matches || consecutive.Score >= scattered.Score {
			t.Fatal(consecutive, scattered)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:48
	t.Run("word boundary matches score better", func(t *testing.T) {
		boundary, interior := FuzzyMatchScore("fb", "foo-bar"), FuzzyMatchScore("fb", "afbx")
		if !boundary.Matches || !interior.Matches || boundary.Score >= interior.Score {
			t.Fatal(boundary, interior)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:57
	t.Run("matches swapped alpha numeric tokens", func(t *testing.T) {
		if got := FuzzyMatchScore("codex52", "gpt-5.2-codex"); !got.Matches {
			t.Fatal(got)
		}
	})
}

func TestUpstreamFuzzyFilter(t *testing.T) {
	identity := func(s string) string { return s }
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:64
	t.Run("empty query returns all items unchanged", func(t *testing.T) {
		items := []string{"apple", "banana", "cherry"}
		if got := FuzzyFilter(items, "", identity); !slices.Equal(got, items) {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:70
	t.Run("filters out non-matching items", func(t *testing.T) {
		got := FuzzyFilter([]string{"apple", "banana", "cherry"}, "an", identity)
		if !slices.Contains(got, "banana") || slices.Contains(got, "apple") || slices.Contains(got, "cherry") {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:78
	t.Run("sorts results by match quality", func(t *testing.T) {
		if got := FuzzyFilter([]string{"a_p_p", "app", "application"}, "app", identity); len(got) == 0 || got[0] != "app" {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:86
	t.Run("prioritizes exact matches over longer prefix matches", func(t *testing.T) {
		if got := FuzzyFilter([]string{"clone", "cl"}, "cl", identity); !slices.Equal(got, []string{"cl", "clone"}) {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:93
	t.Run("works with custom getText function", func(t *testing.T) {
		type item struct {
			name string
			id   int
		}
		got := FuzzyFilter([]item{{"foo", 1}, {"bar", 2}, {"foobar", 3}}, "foo", func(i item) string { return i.name })
		if len(got) != 2 || !slices.ContainsFunc(got, func(i item) bool { return i.name == "foo" }) || !slices.ContainsFunc(got, func(i item) bool { return i.name == "foobar" }) {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/fuzzy.test.ts:106
	t.Run("matches slash-separated provider/model queries against reordered text", func(t *testing.T) {
		type model struct{ id, provider string }
		item := model{"gpt-5.5", "openai-codex"}
		if got := FuzzyFilter([]model{item}, "openai-codex/gpt-5.5", func(m model) string { return m.id + " " + m.provider }); !slices.Equal(got, []model{item}) {
			t.Fatal(got)
		}
	})
}
