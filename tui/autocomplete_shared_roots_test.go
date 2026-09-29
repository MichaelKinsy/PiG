package tui

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
)

func TestAutocompleteNoFDDoesNotSubstituteDirectListing(t *testing.T) {
	// Pi 0.87.1 packages/tui/src/autocomplete.ts:746-752 returns [] when fd is absent, even when a prefix-matching local file exists.
	_, base, _, provider := newAutocompleteFixture(t, false)
	putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"README.md": "readme", "src/main.go": "package main"}})
	for _, line := range []string{"@", "@READ", "@src/ma"} {
		t.Run(line, func(t *testing.T) {
			if got := provider.GetSuggestions([]string{line}, 0, len(line)); got != nil {
				t.Fatalf("attachment suggestions without fd = %q, want none", autocompleteValues(got))
			}
		})
	}
}

func TestAutocompleteDirectPathsIncludeGitDirectory(t *testing.T) {
	// Pi 0.87.1 packages/tui/src/autocomplete.ts:623-634 filters direct readdir only by the typed prefix. The .git exclusion belongs to fd search, not direct path completion.
	_, base, _, provider := newAutocompleteFixture(t, false)
	putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{".git/config": "[core]", ".gitignore": "*.tmp"}})
	line := "./.git"
	got := provider.GetSuggestionsForce([]string{line}, 0, len(line))
	want := []string{"./.git/", "./.gitignore"}
	if !slices.Equal(autocompleteValues(got), want) {
		t.Fatalf("direct path suggestions = %q, want %q", autocompleteValues(got), want)
	}
}

func TestEditorAutocompleteSharedBoundaries(t *testing.T) {
	// Pi's awaited provider path (components/editor.ts:2390-2429) preserves no-fd emptiness and direct .git entries.
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"@READ", nil},
		{"./.git", []string{"./.git/", "./.gitignore"}},
	} {
		t.Run(tc.line, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				_, base, _, provider := newAutocompleteFixture(t, false)
				putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"README.md": "readme", ".git/config": "[core]", ".gitignore": "*.tmp"}})
				editor, flush := completionUpstreamEditor(t)
				editor.SetAutocomplete(provider)
				editor.SetText(tc.line)
				editor.HandleInput("\t")
				flush()
				got := autocompleteValues(&AutocompleteSuggestions{Items: editor.autocompleteItems})
				if editor.Text() != tc.line || !slices.Equal(got, tc.want) {
					t.Fatalf("editor text=%q items=%q, want %q items=%q", editor.Text(), got, tc.line, tc.want)
				}
			})
		})
	}
}

func TestAutocompleteConcurrentDirectQueriesOwnCollationState(t *testing.T) {
	// Pi 0.87.1 packages/tui/src/autocomplete.ts:694-701 sorts each request's suggestions locally. Independent Go callers must not share mutable collation iterators.
	_, base, _, provider := newAutocompleteFixture(t, false)
	files := map[string]string{"文档.md": "text", "éclair.md": "text", "Änderung.md": "text", "README.md": "text"}
	for i := range 64 {
		files[fmt.Sprintf("file-%02d.txt", i)] = "text"
	}
	putAutocompleteTree(t, base, autocompleteTree{files: files})
	want := autocompleteValues(provider.GetSuggestionsForce([]string{"./"}, 0, 2))
	if len(want) != len(files) {
		t.Fatalf("fixture suggestions=%d, want each of %d files", len(want), len(files))
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			<-start
			for range 16 {
				got := autocompleteValues(provider.GetSuggestionsForce([]string{"./"}, 0, 2))
				if !slices.Equal(got, want) {
					t.Errorf("worker %d changed independent query ordering: got %q, want %q", worker, got, want)
					return
				}
			}
		})
	}
	close(start)
	workers.Wait()
}
