package tui

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// .upstream/v1.0.0/packages/tui/test/autocomplete-skill-slash.test.ts:5.
func TestUpstreamAutocompleteSkillSlash(t *testing.T) {
	commands := []SlashCommand{
		{Name: "skill:deep-research", Description: "Multi-agent deep research"},
		{Name: "skill:research-idea", Description: "Refine a raw idea into a falsifiable seed"},
		{Name: "skill:to-sidecar", Description: "Route work to a sidecar"},
		{Name: "skill:brainstorm", Description: "Generate ideas"},
		{Name: "model", Description: "Select the active model"},
	}
	suggestionsFor := func(t *testing.T, prefix string) []string {
		t.Helper()
		provider := NewCombinedProvider(commands, t.TempDir(), "")
		line := "/" + prefix
		result := provider.GetSuggestions(context.Background(), []string{line}, 0, len(line), AutocompleteSuggestionOptions{})
		if result == nil {
			t.Fatalf("expected suggestions for %q", "/"+prefix)
		}
		return autocompleteValues(result)
	}
	// autocomplete-skill-slash.test.ts:24.
	t.Run("ranks skill:research-idea first for query 'idea'", func(t *testing.T) {
		items := suggestionsFor(t, "idea")
		if items[0] != "skill:research-idea" {
			t.Fatalf("items = %q", items)
		}
		if slices.Index(items, "skill:deep-research") <= slices.Index(items, "skill:research-idea") {
			t.Fatalf("skill:deep-research must rank after skill:research-idea: %q", items)
		}
	})
	// autocomplete-skill-slash.test.ts:30.
	t.Run("keeps ordinary slash commands matching", func(t *testing.T) {
		if items := suggestionsFor(t, "mod"); !slices.Contains(items, "model") {
			t.Fatalf("items = %q", items)
		}
	})
	// .upstream/v1.0.0/packages/tui/test/autocomplete-skill-slash.test.ts:35 (#10218).
	t.Run("completes commands after leading whitespace and preserves it", func(t *testing.T) {
		provider := NewCombinedProvider([]SlashCommand{{Name: "model"}}, t.TempDir(), "")
		for _, tc := range []struct{ line, expected string }{
			{" /", " /model "},
			{"  /mod", "  /model "},
			{"\t/mod", "\t/model "},
		} {
			result := provider.GetSuggestions(context.Background(), []string{tc.line}, 0, len(tc.line), AutocompleteSuggestionOptions{})
			if result == nil {
				t.Fatalf("%q: expected suggestions", tc.line)
			}
			if want := strings.TrimLeft(tc.line, " \t"); result.Prefix != want {
				t.Fatalf("%q: prefix = %q, want %q", tc.line, result.Prefix, want)
			}
			if got := autocompleteValues(result); !slices.Equal(got, []string{"model"}) {
				t.Fatalf("%q: items = %q, want [model]", tc.line, got)
			}
			lines, _, cursorCol := provider.ApplyCompletion([]string{tc.line}, 0, len(tc.line), result.Items[0], result.Prefix)
			if lines[0] != tc.expected {
				t.Fatalf("%q: applied line = %q, want %q", tc.line, lines[0], tc.expected)
			}
			if cursorCol != len(tc.expected) {
				t.Fatalf("%q: cursorCol = %d, want %d", tc.line, cursorCol, len(tc.expected))
			}
		}
	})
	// .upstream/v1.0.0/packages/tui/test/autocomplete-skill-slash.test.ts:57 (#10218).
	t.Run("completes command arguments after leading whitespace", func(t *testing.T) {
		provider := NewCombinedProvider([]SlashCommand{{
			Name: "model",
			GetArgumentCompletions: func(prefix string) []AutocompleteItem {
				if prefix != "son" {
					t.Errorf("argument prefix = %q, want %q", prefix, "son")
				}
				return []AutocompleteItem{{Value: "sonnet", Label: "sonnet"}}
			},
		}}, t.TempDir(), "")
		line := "  /model son"
		result := provider.GetSuggestions(context.Background(), []string{line}, 0, len(line), AutocompleteSuggestionOptions{})
		if result == nil {
			t.Fatalf("%q: expected suggestions", line)
		}
		if result.Prefix != "son" {
			t.Fatalf("prefix = %q, want %q", result.Prefix, "son")
		}
		lines, _, _ := provider.ApplyCompletion([]string{line}, 0, len(line), result.Items[0], result.Prefix)
		if lines[0] != "  /model sonnet" {
			t.Fatalf("applied line = %q, want %q", lines[0], "  /model sonnet")
		}
	})
	// autocomplete-skill-slash.test.ts:80.
	t.Run("keeps explicit skill: queries working", func(t *testing.T) {
		if items := suggestionsFor(t, "skill:side"); !slices.Contains(items, "skill:to-sidecar") {
			t.Fatalf("items = %q", items)
		}
	})
	// autocomplete-skill-slash.test.ts:86 (regression test for #9944).
	t.Run("lists skills while typing the skill prefix", func(t *testing.T) {
		var got, want []string
		for _, item := range suggestionsFor(t, "skill") {
			if strings.HasPrefix(item, "skill:") {
				got = append(got, item)
			}
		}
		for _, command := range commands {
			if strings.HasPrefix(command.Name, "skill:") {
				want = append(want, command.Name)
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("skills = %q, want %q", got, want)
		}
	})
	// autocomplete-skill-slash.test.ts:94.
	t.Run("keeps fuzzy skill-prefix shorthand working", func(t *testing.T) {
		if items := suggestionsFor(t, "skbra"); !slices.Contains(items, "skill:brainstorm") {
			t.Fatalf("items = %q", items)
		}
	})
}

// .upstream/v0.99.2/packages/tui/test/autocomplete.test.ts:170.
func TestUpstreamAutocompleteAtAfterOpeningWrappers(t *testing.T) {
	_, base, _, p := newAutocompleteFixture(t, true)
	putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"README.md": "readme"}})
	for _, before := range []string{"(", "see (", "[", "`", "<", "{"} {
		line := before + "@REA"
		r := requireAutocomplete(t, p, line, len(line), false)
		if r.Prefix != "@REA" {
			t.Fatalf("%q: prefix=%q", line, r.Prefix)
		}
		want := before + "@README.md "
		assertAutocompleteApplied(t, p, line, len(line), r.Items[0], r.Prefix, want, len(want))
	}
	embedded := "foo(@REA"
	if r := upstreamSuggestions(p, embedded, len(embedded), false); r != nil {
		t.Fatalf("%q: suggestions=%+v, want none", embedded, r)
	}
}

// .upstream/v0.99.2/packages/tui/test/autocomplete.test.ts:647.
func TestUpstreamAutocompletePathsAfterOpeningWrappers(t *testing.T) {
	// autocomplete.test.ts:647.
	t.Run("completes paths after opening wrappers like ( [ { < and backticks", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, false)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"src/main.ts": "x"}})
		for _, wrapper := range []string{"(", "[", "{", "<", "`", "((", "(`"} {
			for _, prefix := range []string{"src/ma", "./src/ma"} {
				before := "see " + wrapper
				line := before + prefix
				r := requireAutocomplete(t, p, line, len(line), true)
				if r.Prefix != prefix {
					t.Fatalf("%q: prefix=%q, want %q", line, r.Prefix, prefix)
				}
				value := strings.Replace(prefix, "src/ma", "src/main.ts", 1)
				requireAutocompleteValues(t, r, []string{value})
				assertAutocompleteApplied(t, p, line, len(line), r.Items[0], r.Prefix, before+value, len(before+value))
			}
		}
	})
	// autocomplete.test.ts:668.
	t.Run("completes quoted paths after opening wrappers", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, false)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"my dir/main.ts": "x"}})
		line := `see ("my dir/ma`
		r := requireAutocomplete(t, p, line, len(line), true)
		if r.Prefix != `"my dir/ma` {
			t.Fatalf("prefix=%q", r.Prefix)
		}
		requireAutocompleteValues(t, r, []string{`"my dir/main.ts"`})
	})
	// autocomplete.test.ts:681.
	t.Run("keeps wrappers that are closed inside the path", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, false)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"[slug]/page.tsx": "x", "(group)/layout.tsx": "x"}})
		for _, tc := range [][2]string{
			{"[slug]/pa", "[slug]/page.tsx"},
			{"(group)/la", "(group)/layout.tsx"},
			{"./[slug]/pa", "./[slug]/page.tsx"},
		} {
			line := "see " + tc[0]
			r := requireAutocomplete(t, p, line, len(line), true)
			if r.Prefix != tc[0] {
				t.Fatalf("%q: prefix=%q", line, r.Prefix)
			}
			requireAutocompleteValues(t, r, []string{tc[1]})
		}
	})
}
