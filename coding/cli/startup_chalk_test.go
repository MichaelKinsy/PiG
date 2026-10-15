package cli

import (
	"bytes"
	"strings"
	"testing"
)

// main.ts prints `chalk.yellow("Session found in different project: <cwd>")` before promptConfirm's plain "[y/N]" question, and list-models.ts prints chalk.yellow("Warning: errors loading models.json:\n<error>"), which chalk styles line by line.
func TestStartupNoticesStyleLikeChalk(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	var out bytes.Buffer
	if _, err := confirmCrossProjectSession(strings.NewReader("n\n"), &out, "/other"); err != nil {
		t.Fatal(err)
	}
	if want := "\x1b[33mSession found in different project: /other\x1b[39m\nFork this session into current directory? [y/N] "; out.String() != want {
		t.Errorf("cross-project prompt = %q, want %q", out.String(), want)
	}
	if got, want := chalkYellow("Warning: errors loading models.json:\nFailed\n\nFile: x"), "\x1b[33mWarning: errors loading models.json:\x1b[39m\n\x1b[33mFailed\x1b[39m\n\x1b[33m\x1b[39m\n\x1b[33mFile: x\x1b[39m"; got != want {
		t.Errorf("models.json warning = %q, want %q", got, want)
	}
}
