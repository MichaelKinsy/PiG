//go:build parity

package runner

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPreTuiLinesCommandPrintsNumberedLines(t *testing.T) {
	if got := preTuiLinesCommand(0); got != "" {
		t.Fatalf("no lines requested: command %q, want empty", got)
	}
	out, err := exec.Command("sh", "-c", preTuiLinesCommand(5)+"true").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "PRE-TUI-LINE-1\nPRE-TUI-LINE-2\nPRE-TUI-LINE-3\nPRE-TUI-LINE-4\nPRE-TUI-LINE-5\n"
	if string(out) != want {
		t.Fatalf("lines = %q, want %q", out, want)
	}
}

func TestPreTuiLinesRequireTmuxDriver(t *testing.T) {
	sc := &Scenario{Name: "pre", Driver: "cli-mode", Covers: []string{"packages/tui/src/tui.ts"}, Tmux: TmuxDriverConfig{PreTuiLines: 3}}
	err := validate(sc)
	if err == nil || !strings.Contains(err.Error(), "pre_tui_lines") {
		t.Fatalf("pre_tui_lines on a non-tmux driver: err = %v", err)
	}
}
