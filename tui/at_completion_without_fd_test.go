package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Upstream's @ completion is fd's fuzzy search only: without fd,
// getFuzzyFileSuggestions returns [] and getSuggestions returns null
// (autocomplete.ts:303-310, 738-752). Forced path completion (Tab) still
// lists the directory with readdirSync, fd or not.
func TestAtCompletionWithoutFdOffersNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := NewCombinedProvider(nil, dir, "")
	for _, line := range []string{"@no", "@d", "look at @no"} {
		if got := p.GetSuggestions(context.Background(), []string{line}, 0, len(line), AutocompleteSuggestionOptions{}); got != nil {
			t.Errorf("%q without fd suggested %+v, want none", line, got.Items)
		}
	}
	forced := p.GetSuggestions(context.Background(), []string{"./no"}, 0, len("./no"), AutocompleteSuggestionOptions{Force: true})
	if forced == nil || len(forced.Items) != 1 || forced.Items[0].Value != "./notes.txt" {
		t.Fatalf("Tab path completion without fd = %+v, want ./notes.txt", forced)
	}
}
