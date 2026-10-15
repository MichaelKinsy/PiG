package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi builds the autocomplete provider on this.sessionManager.getCwd() (interactive-mode.ts:820), so after resuming a
// session created in another directory, path completion lists that session's directory, not the launch directory.
func TestAutocompleteProviderCompletesPathsFromSessionCwd(t *testing.T) {
	sessionCwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(sessionCwd, "session-only.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	mode, _ := newCustomEditorDispatchMode(t)
	mode.opts.AgentDir = t.TempDir()
	mode.opts.CWD = sessionCwd
	line := "./sess"
	suggestions := mode.buildAutocompleteProvider().GetSuggestions(context.Background(), []string{line}, 0, len(line), tui.AutocompleteSuggestionOptions{Force: true})
	if suggestions == nil || len(suggestions.Items) != 1 || suggestions.Items[0].Value != "./session-only.txt" {
		t.Fatalf("suggestions = %#v, want ./session-only.txt from the session cwd %s", suggestions, sessionCwd)
	}
}
