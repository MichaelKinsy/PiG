//go:build parity

package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A step's signal goes to the program the launch shell runs, not to the shell: the shell has to stay alive to write the program's exit status, and a program
// that traps the signal and exits with a status of its own shows which of the two received it.
func TestStepSignalReachesTheProgramAndTheLaunchShellWritesItsStatus(t *testing.T) {
	skipWithoutTmux(t)
	useTmuxSocket(t, "pig-parity-signal-"+uniqueID())
	tmuxServerMu.Lock()
	err := ensureTmuxServer()
	tmuxServerMu.Unlock()
	if err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	for _, tc := range []struct {
		signal string
		status int
	}{
		{"TERM", 7},
		{"HUP", 8},
	} {
		t.Run(tc.signal, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			ready, statusPath := filepath.Join(dir, "ready"), filepath.Join(dir, "process-exit-status")
			program := fmt.Sprintf(`sh -c 'trap "kill \$!; exit %d" %s; : > %s; sleep 30 & wait'`, tc.status, tc.signal, shellQuote(ready))
			command := program + "; exit_code=$?; printf '%s\\n' \"$exit_code\" > " + shellQuote(statusPath)
			session := "parity-signal-" + uniqueID()
			registerSession(session)
			t.Cleanup(func() { killSession(session) })
			if out, err := exec.Command("tmux", tmuxArgs(createTmuxSessionArgs(session, command, TmuxDriverConfig{Width: 80, Height: 24})...)...).CombinedOutput(); err != nil {
				t.Fatalf("tmux new-session: %v: %s", err, out)
			}
			awaitFile(t, ready)
			if err := signalTmuxProgram(ctx, session, tc.signal); err != nil {
				t.Fatal(err)
			}
			if err := awaitProcessExit(ctx, statusPath, 10*time.Second); err != nil {
				t.Fatal(err)
			}
			if code, _, err := readProcessExitStatus(statusPath); err != nil || code != tc.status {
				t.Fatalf("the program ended with status %d (%v), want %d: the signal %s did not reach it", code, err, tc.status, tc.signal)
			}
		})
	}
}

// A pane whose shell has no child has no program to signal: the pane's process is the program, or the program ended. Signalling the pane's own process would
// end the wrong thing, so the step is refused.
func TestStepSignalRefusesAPaneWhoseShellHasNoProgramChild(t *testing.T) {
	skipWithoutTmux(t)
	useTmuxSocket(t, "pig-parity-signal-none-"+uniqueID())
	tmuxServerMu.Lock()
	err := ensureTmuxServer()
	tmuxServerMu.Unlock()
	if err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	session := "parity-signal-none-" + uniqueID()
	registerSession(session)
	t.Cleanup(func() { killSession(session) })
	if out, err := exec.Command("tmux", tmuxArgs(createTmuxSessionArgs(session, "exec sleep 30", TmuxDriverConfig{Width: 80, Height: 24})...)...).CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	if err := signalTmuxProgram(context.Background(), session, "TERM"); err == nil {
		t.Fatal("signalTmuxProgram succeeded for a pane whose process is not a shell with one child: the signal would end the wrong process")
	}
}

// awaitFile waits for the file the program creates when its trap is set. The wait is bounded by the test's own deadline.
func awaitFile(t *testing.T, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), processExitTimeout(t))
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s did not appear", path)
		case <-ticker.C:
		}
	}
}
