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

// Pi 7c05885bc (#12133): `pi --resume` and the other startup pickers stopped their TUI and then exited with process.exit(), which dropped the terminal mode resets
// still queued in stdout (mouse tracking off, bracketed paste off, the Kitty keyboard flags), so the shell kept the picker's modes. Pi now waits for stdout before it exits.
// pig writes the terminal synchronously (tui/terminal.go), so the resets are written before the process ends. The terminal's last bytes are the contract: the picker's
// bracketed paste is turned off, and the Kitty flags it pushed are popped, after the last time either was turned on.
func TestStartupResumePickerExitLeavesTheTerminalModesReset(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary on a pseudo-terminal")
	}
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	home, dir := shortTempDir(t), t.TempDir()
	seedFirstRunDone(t, filepath.Join(home, "agent"))
	sessions := filepath.Join(home, "sessions")
	resumableSession(t, dir, sessions, "picker-exit-session")
	master, slave := openPTY(t, 40, 140)
	output := &ptyOutput{}
	cmd := exec.Command(binary, "--resume", "--session-dir", sessions)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_OFFLINE=1", "PI_OFFLINE=1", "TERM=xterm-256color", "FORCE_COLOR=0")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	_ = slave.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			<-exited
		}
	})
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(output, master)
	}()
	t.Cleanup(func() {
		_ = master.Close()
		<-copied
	})
	p := &interactivePig{t: t, master: master, output: output}
	p.waitScreen("Resume Session", "the session picker")
	p.waitScreen("from elsewhere", "the Session in the picker")
	p.send("\x03")
	select {
	case err := <-exited:
		waited = true
		if status := cmd.ProcessState.ExitCode(); status != 0 {
			t.Fatalf("pig exited with status %d (%v), want 0; screen tail: %q", status, err, printableTail(output.since(0), 600))
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("pig did not exit after ctrl+c; screen tail: %q", printableTail(output.since(0), 1500))
	}
	select {
	case <-copied:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("pseudo-terminal output did not drain after pig exited; screen tail: %q", printableTail(output.since(0), 600))
	}
	written := output.since(0)
	for _, mode := range []struct{ on, off string }{{"\x1b[?2004h", "\x1b[?2004l"}, {"\x1b[>", "\x1b[<u"}} {
		on, off := bytes.LastIndex(written, []byte(mode.on)), bytes.LastIndex(written, []byte(mode.off))
		if on < 0 {
			t.Fatalf("the picker never turned %q on: %q", mode.on, printableTail(written, 800))
		}
		if off < on {
			t.Errorf("the terminal's last bytes do not turn %q off after the picker turned it on: %q", mode.on, printableTail(written, 400))
		}
	}
}
