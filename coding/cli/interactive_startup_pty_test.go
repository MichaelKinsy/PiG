//go:build linux

package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's interactive startup draws the first frame, with the editor (which owns the TUI and reads tui.terminal.rows while it renders,
// editor.ts), and then echoes typed text. Both renderers hold their lock for the frame, so an editor read of the terminal that took the
// renderer lock left the pane blank and dead to input.
func TestInteractiveStartupRendersTheEditorAndAcceptsInput(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary")
	}
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			home, cwd := shortTempDir(t), t.TempDir()
			agentDir := filepath.Join(home, "agent")
			if err := os.MkdirAll(agentDir, 0o700); err != nil {
				t.Fatal(err)
			}
			settings := `{"theme":"dark","tuiMode":"` + mode + `"}`
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
				t.Fatal(err)
			}
			master, slave := openPTY(t, 40, 120)
			t.Cleanup(func() { _ = master.Close() })
			cmd := exec.Command(binary, "--model", "test-faux/faux-1", "--no-session", "--no-extensions")
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+home, "PI_HOME="+home, "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir,
				"PIG_TEST_FAUX=1", "PIG_OFFLINE=1", "TERM=xterm-256color")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
			if err := cmd.Start(); err != nil {
				t.Fatalf("start pig: %v", err)
			}
			_ = slave.Close()
			exited := make(chan struct{})
			go func() { _ = cmd.Wait(); close(exited) }()
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				<-exited
			})
			output := &ptyOutput{}
			go func() { _, _ = io.Copy(output, master) }()

			footer := []byte("faux-1")
			output.waitQuiet(0, footer, 300*time.Millisecond, testbudget.Wait(t))
			if !bytes.Contains(output.since(0), footer) {
				t.Fatalf("the first frame never drew the footer; output tail: %q", tail(output.since(0), 1500))
			}
			mark := output.mark()
			typed := []byte("startup-probe-typed")
			if _, err := master.Write(typed); err != nil {
				t.Fatal(err)
			}
			output.waitQuiet(mark, typed, 100*time.Millisecond, testbudget.Wait(t))
			if !bytes.Contains(output.since(mark), typed) {
				t.Fatalf("typed text never reached the editor; output tail: %q", tail(output.since(0), 1500))
			}
		})
	}
}
