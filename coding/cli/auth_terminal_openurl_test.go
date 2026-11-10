//go:build !windows

package cli

// pi: packages/coding-agent/src/cli/auth-terminal.ts (openUrl: openBrowser)

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestLoginTerminalOpenURLHandsTheURLToTheBrowserLauncher runs a launcher script named as the platform's launcher (xdg-open, open) and checks it received the URL as its one argument.
func TestLoginTerminalOpenURLHandsTheURLToTheBrowserLauncher(t *testing.T) {
	launcher := "xdg-open"
	if runtime.GOOS == "darwin" {
		launcher = "open"
	}
	dir := t.TempDir()
	recorded := filepath.Join(dir, "recorded")
	script := "#!/bin/sh\nprintf '%s\\n%s' \"$#\" \"$1\" > \"$RECORD_FILE.tmp\" && mv \"$RECORD_FILE.tmp\" \"$RECORD_FILE\"\n"
	if err := os.WriteFile(filepath.Join(dir, launcher), []byte(script), 0o755); err != nil { //nolint:gosec // G306: the test launcher must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RECORD_FILE", recorded)

	(processLoginTerminal{}).OpenURL("https://example.com/login?a=1&b=2")

	deadline := time.Now().Add(30 * time.Second)
	for {
		data, err := os.ReadFile(recorded)
		if err == nil {
			if got, want := string(data), "1\nhttps://example.com/login?a=1&b=2"; got != want {
				t.Fatalf("launcher saw %q, want %q", got, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("launcher never ran")
		}
		time.Sleep(time.Millisecond)
	}
}
