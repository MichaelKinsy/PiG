package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
)

// autocomplete.ts shouldTriggerFileCompletion: Tab must not force file completion while the text before the cursor is a slash-command name (trimmed, leading "/", no space).
func TestCombinedProviderShouldTriggerFileCompletion(t *testing.T) {
	provider := NewCombinedProvider(nil, t.TempDir(), "")
	for _, tc := range []struct {
		name  string
		lines []string
		line  int
		col   int
		want  bool
	}{
		{"slash command name", []string{"/mod"}, 0, 4, false},
		{"indented slash command name", []string{"  /mod"}, 0, 6, false},
		{"slash command argument", []string{"/model gp"}, 0, 9, true},
		{"cursor before the space", []string{"/model gp"}, 0, 6, false},
		{"plain text", []string{"hello"}, 0, 5, true},
		{"slash after text", []string{"see /usr"}, 0, 8, true},
		{"second line", []string{"/mod", "x"}, 1, 1, true},
		{"empty buffer", []string{""}, 0, 0, true},
		{"line out of range", []string{"/mod"}, 3, 0, true},
		{"cursor past the end", []string{"/mod"}, 0, 99, false},
	} {
		if got := provider.ShouldTriggerFileCompletion(tc.lines, tc.line, tc.col); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// editor.ts forceFileAutocomplete asks the provider first: Tab at a slash-command-shaped absolute path completes nothing, and the same path after other text completes the only match.
func TestEditorTabHonoursCombinedProviderShouldTriggerFileCompletion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	slashed := filepath.ToSlash(dir)
	rooted := filepath.ToSlash(strings.TrimPrefix(dir, filepath.VolumeName(dir)))
	for _, tc := range []struct{ typed, want string }{
		{rooted + "/al", rooted + "/al"},
		{"x " + slashed + "/al", "x " + slashed + "/alpha.txt"},
	} {
		synctest.Test(t, func(t *testing.T) {
			e, flush := completionUpstreamEditor(t)
			e.SetAutocomplete(NewCombinedProvider(nil, dir, ""))
			completionUpstreamType(e, tc.typed)
			flush()
			e.HandleInput("\t")
			flush()
			completionUpstreamText(t, e, tc.want)
		})
	}
}
