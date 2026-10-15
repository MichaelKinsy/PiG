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

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
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

// Pi starts the TUI and paints the editor and footer first, then queries the terminal's colors and only after that adds the startup
// header, because the header bakes theme colors into its text (interactive-mode.ts init(): ui.start(), themeController.applyFromSettings(),
// await waitForTerminalColors(), then the header container children). A terminal that answers no color query therefore shows the editor at
// once and the header 100 ms later. PiG painted both in one frame after that wait, so the first frame of a silent terminal took 114 ms.
// This test never answers a query and reads the startup trace from the same stream as the frames, so the order of the trace line that ends the wait and the first appearance of the footer shows whether startup painted before it waited.
func TestInteractiveStartupPaintsTheEditorBeforeTheHeaderWhenTheTerminalIsSilent(t *testing.T) {
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
			settings := `{"theme":"system","tuiMode":"` + mode + `"}`
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
				t.Fatal(err)
			}
			master, slave := openPTY(t, 40, 120)
			t.Cleanup(func() { _ = master.Close() })
			cmd := exec.Command(binary, "--model", "test-faux/faux-1", "--no-session", "--no-extensions")
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+home, "PI_HOME="+home, "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir,
				"PIG_TEST_FAUX=1", "PIG_OFFLINE=1", "TERM=xterm-256color", "PIG_STARTUP_TRACE=1")
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

			footer, header, ready := []byte("faux-1"), []byte("show full startup help"), []byte("terminal-colors-ready")
			output.waitQuiet(0, header, 300*time.Millisecond, testbudget.Wait(t))
			text := tools.StripANSI(output.since(0))
			footerAt, readyAt, headerAt := bytes.Index(text, footer), bytes.Index(text, ready), bytes.Index(text, header)
			if readyAt < 0 || headerAt < 0 {
				t.Fatalf("startup never reached the header (colors ready at %d, header at %d); output tail: %q", readyAt, headerAt, tail(output.since(0), 1500))
			}
			if footerAt < 0 || footerAt > readyAt {
				t.Fatalf("startup waited for the terminal's colors before it painted the editor frame (footer at %d, colors ready at %d); output tail: %q", footerAt, readyAt, tail(text, 1500))
			}
			if headerAt < readyAt {
				t.Fatalf("the startup header was painted before the terminal's colors were ready (header at %d, colors ready at %d)", headerAt, readyAt)
			}
		})
	}
}
