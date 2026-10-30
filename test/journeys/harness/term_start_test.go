package harness

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// StartTerminal must start a program on every tmux the journeys run on, and the window must keep the size it was started with whatever the control
// client reports: window-size manual. tmux 3.4 (Ubuntu 24.04, tern-ci) exits on that option set in the config file before the first session
// exists, so no journey could start there.
func TestStartTerminalRunsAProgramAtItsSizeOnThisTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	term, err := StartTerminal(TermOpts{Dir: t.TempDir(), Env: []string{"PATH=" + os.Getenv("PATH")}, Argv: []string{"sh", "-c", "echo journey-ready; sleep 30"}, Width: 100, Height: 30, Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	deadline := time.After(20 * time.Second)
	for {
		changed := term.Changed()
		screen, err := term.Screen()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(screen, "journey-ready") {
			break
		}
		select {
		case <-changed:
		case <-time.After(50 * time.Millisecond): // the program may have written before the control client attached: no output event follows
		case <-deadline:
			t.Fatalf("the program's output never appeared; screen:\n%s", screen)
		}
	}
	for command, want := range map[string]string{
		"show-options -gv window-size":                                     "manual",
		"display-message -p -t pig-jny '#{window_width}x#{window_height}'": "100x30",
	} {
		lines, err := term.command(command)
		if err != nil || strings.Join(lines, "\n") != want {
			t.Errorf("%s = %q, %v; want %q", command, lines, err, want)
		}
	}
}
