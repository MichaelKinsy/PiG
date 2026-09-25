//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var tmuxSocket = fmt.Sprintf("pig-test-%d-%s", os.Getpid(), randID())
var tmuxHomeRoot string

// Every command, including cleanup, addresses only this process's server.
// Each new pane starts with isolated home and trust state; tests may supply
// their own fixture homes when launching the program within that pane.
func tmuxCommand(args ...string) *exec.Cmd {
	if len(args) > 0 && args[0] == "new-session" {
		home, err := os.MkdirTemp(tmuxHomeRoot, "pane-home-")
		if err != nil {
			cmd := exec.Command("tmux")
			cmd.Err = err
			return cmd
		}
		args = append([]string{
			"new-session", "-e", "HOME=" + home, "-e", "PIG_HOME=" + filepath.Join(home, ".pig"),
			"-e", "PIG_CODING_AGENT_DIR=", "-e", "PIG_CODING_AGENT_SESSION_DIR=",
			"-e", "PI_CODING_AGENT_DIR=", "-e", "PI_CODING_AGENT_SESSION_DIR=",
			"-e", "HERDR_ENV=", "-e", "HERDR_KITTY_GRAPHICS=",
		}, args[1:]...)
	}
	return exec.Command("tmux", append([]string{"-L", tmuxSocket, "-f", "/dev/null"}, args...)...)
}

func TestTmuxServerSurvivesSessionCleanup(t *testing.T) {
	t.Run("owned-session", func(t *testing.T) {
		newHarness(t)
	})
	if out, err := tmuxCommand("list-sessions").CombinedOutput(); err != nil {
		t.Fatalf("tmux server disappeared after the test session closed: %v\n%s", err, out)
	}
}

func TestTmuxSessionsHaveSeparateHomes(t *testing.T) {
	for _, key := range []string{"PIG_CODING_AGENT_DIR", "PIG_CODING_AGENT_SESSION_DIR", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "HERDR_ENV", "HERDR_KITTY_GRAPHICS"} {
		t.Setenv(key, t.TempDir())
	}
	homes := make(map[string]bool)
	for range 2 {
		session := "pig-isolation-" + randID()
		if out, err := tmuxCommand("new-session", "-d", "-s", session, "sleep 30").CombinedOutput(); err != nil {
			t.Fatalf("create pane: %v: %s", err, out)
		}
		t.Cleanup(func() { _ = tmuxCommand("kill-session", "-t", session).Run() })
		for _, key := range []string{"HOME", "PIG_HOME"} {
			out, err := tmuxCommand("show-environment", "-t", session, key).Output()
			if err != nil {
				t.Fatal(err)
			}
			value := strings.TrimSpace(strings.TrimPrefix(string(out), key+"="))
			if !strings.HasPrefix(value, tmuxHomeRoot+string(os.PathSeparator)) || homes[value] {
				t.Fatalf("%s is not an independent fixture home: %q", key, value)
			}
			homes[value] = true
		}
		// Agent/session overrides take precedence over HOME and PIG_HOME. Herdr's outer graphics forwarding does not describe this detached tmux pane.
		for _, key := range []string{"PIG_CODING_AGENT_DIR", "PIG_CODING_AGENT_SESSION_DIR", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "HERDR_ENV", "HERDR_KITTY_GRAPHICS"} {
			out, err := tmuxCommand("show-environment", "-t", session, key).Output()
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(out)) != key+"=" {
				t.Fatalf("pane inherits ambient agent state: %s", out)
			}
		}
	}
}
