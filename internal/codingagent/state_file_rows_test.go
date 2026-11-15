//go:build !windows

package codingagent

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stateFileRows are the other files of the agent directory that pig rewrites as a whole: each row seeds the file, then runs the writer in a process whose writes fail after 64 bytes.
var stateFileRows = map[string]struct {
	file string
	seed string
	run  func(agentDir string)
}{
	"keybindings save": {
		file: "keybindings.json",
		seed: "{\n  \"app.model.select\": \"ctrl+l\",\n  \"app.thinking.cycle\": \"shift+tab\",\n  \"app.session.new\": \"ctrl+n\"\n}\n",
		run: func(agentDir string) {
			bindings := map[string][]KeyID{"app.model.select": {"ctrl+k"}, "app.thinking.cycle": {"ctrl+t"}, "app.session.new": {"ctrl+o"}, "app.session.tree": {"ctrl+y"}}
			_ = NewKeybindingsManagerFromBindings(bindings, filepath.Join(agentDir, "keybindings.json")).Save("")
		},
	},
	"keybindings migration": {
		file: "keybindings.json",
		seed: "{\"cursorUp\":\"up\",\"cursorDown\":\"down\",\"submit\":\"enter\",\"selectCancel\":\"escape\"}",
		run:  migrateKeybindingsConfigFile,
	},
	"apiKeys migration of settings.json": {
		file: "settings.json",
		seed: "{\"theme\":\"dark\",\"defaultProvider\":\"anthropic\",\"apiKeys\":{\"anthropic\":\"sk-test\"},\"defaultModel\":\"claude-sonnet-4-5\"}",
		run:  func(agentDir string) { _, _ = migrateAuthToAuthJSON(agentDir) },
	},
}

// A rewrite of one of these files that fails once it has begun must leave the file as it was: the in-place write truncated it first, which lost the user's keybindings or every setting.
func TestOtherStateFilesKeepTheirBytesWhenAWriteFails(t *testing.T) {
	if role := os.Getenv("STATE_TEST_ROLE"); strings.HasPrefix(role, "row:") {
		limitFileSize(t)
		stateFileRows[strings.TrimPrefix(role, "row:")].run(os.Getenv("STATE_TEST_DIR"))
		return
	}
	names := make([]string, 0, len(stateFileRows))
	for name := range stateFileRows {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		row := stateFileRows[name]
		scratch := t.TempDir()
		agentDir := filepath.Join(scratch, "agent")
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(agentDir, row.file)
		if err := os.WriteFile(path, []byte(row.seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := stateChild(t, scratch, agentDir, "row:"+name).CombinedOutput(); err != nil {
			t.Fatalf("%s: child: %v\n%s", name, err, out)
		}
		if after, _ := os.ReadFile(path); !bytes.Equal(after, []byte(row.seed)) {
			t.Errorf("%s: %s after the failed write is %d bytes %q, want the %d bytes it had", name, row.file, len(after), after, len(row.seed))
		}
		noTemporaryFiles(t, agentDir)
	}
}
