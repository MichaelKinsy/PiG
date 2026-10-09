package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ports editor.ts:2289-2298 requestAutocomplete({force: true}): a provider that refuses with shouldTriggerFileCompletion
// ends the Tab without a completion, even when the path would match a file. On the first line a slash-led word without a
// space takes the slash-command branch (editor.ts:2271 isInSlashCommandContext) and never asks the provider, so only the
// later-line case reaches the refusal.
func TestEditorTabDoesNotFileCompleteInsideLeadingSlashWord(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.ToSlash(dir) + "/"
	// A path that begins with a slash: on Windows the drive is dropped, which leaves a rooted path on the current drive.
	rooted := filepath.ToSlash(strings.TrimPrefix(dir, filepath.VolumeName(dir))) + "/"
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"leading slash and no space", rooted + "he", rooted + "he"},
		{"leading slash on a later line", "x\n" + rooted + "he", "x\n" + rooted + "he"},
		{"a space before the path", "cat " + prefix + "he", "cat " + prefix + "hello.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			editor := NewEditor()
			defer editor.AutocompleteCancel()
			editor.SetAutocomplete(NewCombinedProvider(nil, dir, ""))
			editor.SetText(tc.text)
			editor.HandleInput("\t")
			if got := editor.Text(); got != tc.want {
				t.Fatalf("after Tab text = %q, want %q", got, tc.want)
			}
		})
	}
}
