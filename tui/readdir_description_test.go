package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi autocomplete.ts:686-690 returns no description for ordinary directory entries; only fuzzy fd results carry a display-path description (812-813).
func TestReaddirSuggestionsDoNotInventDescriptions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &CombinedProvider{baseDir: dir}
	suggestions := provider.readdirFileSuggestions("./", false)
	if len(suggestions) != 1 || suggestions[0].Value != "@./alpha.txt" || suggestions[0].Label != "alpha.txt" || suggestions[0].Description != "" {
		t.Fatalf("suggestions=%+v", suggestions)
	}
}
