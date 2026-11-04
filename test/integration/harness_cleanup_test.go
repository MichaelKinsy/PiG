//go:build integration && !windows

// These tests hang tmux panes up and read process state through ps and pgrep; native Windows has neither tmux nor those commands.

package integration

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// hangupShutdownScript stands in for pig's shutdown after a terminal hangup: the hangup starts a shutdown that outlives tmux kill-session, and the shutdown ends by appending to exit.log in the agent directory, creating the directory when it is gone (internal/codingagent appendExitLog does both). The shutdown waits for the release file before it writes, so the test decides when it can finish. Arguments: ready file, release file, agent directory, finished file.
const hangupShutdownScript = `ready=$1 release=$2 agent=$3 finished=$4
trap 'until [ -e "$release" ]; do sleep 0.1; done; mkdir -p "$agent" && echo SIGHUP >> "$agent/exit.log"; : > "$finished"; exit 0' HUP
: > "$ready"
while :; do sleep 0.1; done
`

// hangupFixture is a pane process running hangupShutdownScript.
type hangupFixture struct {
	script, ready, release, finishedFile string
}

func newHangupFixture(t *testing.T) hangupFixture {
	t.Helper()
	dir := t.TempDir()
	f := hangupFixture{
		script:       filepath.Join(dir, "hangup-shutdown.sh"),
		ready:        filepath.Join(dir, "ready"),
		release:      filepath.Join(dir, "release"),
		finishedFile: filepath.Join(dir, "finished"),
	}
	if err := os.WriteFile(f.script, []byte(hangupShutdownScript), 0o600); err != nil {
		t.Fatal(err)
	}
	// A kill that never waits never releases the shutdown; release it after the checks so it does not outlive the test.
	t.Cleanup(func() {
		if err := f.letFinish(); err != nil {
			t.Errorf("release hangup shutdown: %v", err)
		}
	})
	return f
}

// command is the line that starts the fixture in a pane shell.
func (f hangupFixture) command(agentDir string) string {
	return fmt.Sprintf("sh %s %s %s %s %s\r", f.script, f.ready, f.release, agentDir, f.finishedFile)
}

func (f hangupFixture) waitReady(t *testing.T) {
	t.Helper()
	if !waitFor(startupReadyWait, func() bool {
		_, err := os.Stat(f.ready)
		return err == nil
	}) {
		t.Fatal("hangup shutdown fixture never started")
	}
}

func (f hangupFixture) letFinish() error {
	return os.WriteFile(f.release, nil, 0o600)
}

func (f hangupFixture) finished() bool {
	_, err := os.Stat(f.finishedFile)
	return err == nil
}

// TestHarnessCleanupWaitsForHangupShutdown pins the order of a harness test's cleanup. A process in the pane must have finished its hangup shutdown before the test's temporary directories are removed. Otherwise pig's exit record races the removal and the test fails with "TempDir RemoveAll cleanup: directory not empty" (TestExt_StartupWithoutConfig).
//
// The fixture is released only after the pane shell has exited, so the process that writes is a member of the pane's terminal session whose parent has gone. A wait that follows the shell's children (pgrep -P) loses it at that moment, while the fixture is still blocked and so still alive; the hook below reports that listing. Nothing here depends on timing: the fixture cannot finish until the hook has seen the shell exit.
func TestHarnessCleanupWaitsForHangupShutdown(t *testing.T) {
	requireTmux(t)
	f := newHangupFixture(t)

	var home string
	t.Run("pane", func(t *testing.T) {
		h := newHarness(t)
		// The test's first TempDir call comes after newHarness, as in a test that lets the harness choose its PIG_HOME.
		home = t.TempDir()
		listed := h.livePaneProcesses
		released := false
		h.livePaneProcesses = func(sid int) ([]int, error) {
			shellExited := processExited(sid)
			members, err := listed(sid)
			if err != nil {
				return nil, err
			}
			if len(members) == 0 && !released {
				t.Errorf("the pane's session listed no process while its shutdown still waits for the release: the listing omits members whose parent has exited")
			}
			if shellExited && !released {
				released = true
				if err := f.letFinish(); err != nil {
					return nil, err
				}
			}
			return members, nil
		}
		h.send(f.command(filepath.Join(home, "agent")))
		f.waitReady(t)
	})

	if !f.finished() {
		t.Errorf("kill returned before the pane's process finished its hangup shutdown")
	}
	if _, err := os.Stat(home); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the pane's process wrote into %s after the test's temporary directories were removed (stat error: %v)", home, err)
	}
}

// TestHarnessCleanupKillSessionWaitsForHangupShutdown is the same contract for a session a test starts itself and ends with killSession. A helper goroutine releases the shutdown once the session is gone from tmux and the pane shell has exited; only a killSession that waits for the pane's processes returns after the shutdown has finished.
func TestHarnessCleanupKillSessionWaitsForHangupShutdown(t *testing.T) {
	requireTmux(t)
	f := newHangupFixture(t)
	agentDir := filepath.Join(t.TempDir(), "agent")
	session := "pig-it-kill-" + randID()
	if out, err := tmuxCommand("new-session", "-d", "-s", session, "-x", "120", "-y", "30").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = killSession(t, session) })
	pane, err := paneSessionID(session)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := tmuxCommand("send-keys", "-t", session, "-l", f.command(agentDir)).CombinedOutput(); err != nil {
		t.Fatalf("tmux send-keys: %v\n%s", err, out)
	}
	f.waitReady(t)

	hungUp := make(chan struct{})
	var releaser sync.WaitGroup
	releaser.Go(func() {
		waitFor(paneExitWait, func() bool {
			select {
			case <-hungUp:
				return true
			default:
			}
			return tmuxCommand("has-session", "-t", session).Run() != nil && processExited(pane)
		})
		select {
		case <-hungUp:
			return
		default:
		}
		if err := f.letFinish(); err != nil {
			t.Errorf("release hangup shutdown: %v", err)
		}
	})
	killErr := killSession(t, session)
	close(hungUp)
	releaser.Wait()

	if killErr != nil {
		t.Errorf("kill-session: %v", killErr)
	}
	if !f.finished() {
		t.Errorf("killSession returned before the pane's process finished its hangup shutdown")
	}
}
