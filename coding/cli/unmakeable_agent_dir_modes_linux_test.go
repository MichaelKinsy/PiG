//go:build linux

package cli

// pi: packages/coding-agent/src/main.ts

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Interactive mode on an agent directory that cannot be made ends at startup before the terminal is drawn, as Pi does: exit 1 with Node's mkdir error, for the session directory when the default session manager is used (main.ts:744) and for the project trust store's directory with --no-session. PiG used to stop with its own "open auth storage" text, or with the trust store's text where Pi names the session directory.
func TestInteractiveModeEndsWithNodesMkdirErrorWhereTheAgentDirectoryCannotBeMade(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary on a pseudo-terminal")
	}
	bin := buildPigBinaryForSignalTest(t)
	for name, row := range map[string]struct {
		extra []string
		want  string
	}{
		"no session":              {[]string{"--no-session"}, "ENOTDIR: not a directory, mkdir '"},
		"default session manager": {nil, "/sessions/--"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			agentDir, cwd := unmakeableAgentDirForCLI(t)
			master, slave := openPTY(t, 40, 200)
			t.Cleanup(func() { _ = master.Close() })
			args := append([]string{"--model", "test-faux/faux-1", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-context-files", "--offline"}, row.extra...)
			cmd := exec.CommandContext(t.Context(), bin, args...)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "HOME="+filepath.Dir(cwd), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "TERM=xterm-256color")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
			if err := cmd.Start(); err != nil {
				t.Fatalf("start pig: %v", err)
			}
			_ = slave.Close()
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			output := &ptyOutput{}
			go func() { _, _ = io.Copy(output, master) }()
			select {
			case err := <-exited:
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 {
					t.Fatalf("pig exited with %v, want exit status 1; screen: %q", err, tail(output.since(0), 1500))
				}
			case <-time.After(testbudget.Wait(t)):
				t.Fatalf("pig did not exit; screen tail: %q", tail(output.since(0), 1500))
			}
			output.waitQuiet(0, []byte("ENOTDIR"), 100*time.Millisecond, testbudget.Wait(t))
			screen := output.since(0)
			if !bytes.Contains(screen, []byte("ENOTDIR: not a directory, mkdir '")) || !bytes.Contains(screen, []byte(row.want)) || bytes.Contains(screen, []byte("auth storage")) {
				t.Fatalf("the screen lacks Node's mkdir error (%q): %q", row.want, tail(screen, 1500))
			}
		})
	}
}
