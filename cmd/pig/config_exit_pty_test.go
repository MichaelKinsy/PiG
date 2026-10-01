//go:build linux

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's ConfigSelectorComponent maps tui.select.cancel (Escape) to onCancel and
// matchesKey(data, "ctrl+c") to onExit (config-selector.ts:487-493); both end
// the process with status 0 after ui.stop() restores the terminal
// (cli/config-selector.ts:41-49). A terminal that has enabled the Kitty
// keyboard protocol reports Ctrl+C as CSI-u, never as byte 0x03.
// Regression for issue #89: Ctrl+C left `pig config` running.
func TestConfigSelectorExitKeysRestoreTerminal(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name  string
		kitty bool
		key   string
	}{
		{"escape", false, "\x1b"},
		{"escape-kitty", true, "\x1b[27u"},
		{"ctrl-c-legacy", false, "\x03"},
		{"ctrl-c-kitty", true, "\x1b[99;5u"},
		{"ctrl-c-kitty-press-event", true, "\x1b[99;5:1u"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runConfigExitKey(t, binary, tc.kitty, tc.key)
		})
	}
}

func runConfigExitKey(t *testing.T, binary string, kitty bool, key string) {
	home, agentDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "prompts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "prompts", "alpha.md"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	master, slave := openPTY(t, 35, 100)
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	cmd := exec.CommandContext(t.Context(), binary, "config")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "HOME="+home, "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+agentDir)
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	var waitErr error
	exited := make(chan struct{})
	go func() {
		waitErr = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	output := &ptyOutput{}
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, master)
		close(copied)
	}()
	t.Cleanup(func() {
		_ = master.Close()
		<-copied
	})
	output.waitQuiet(0, []byte("alpha.md"), 100*time.Millisecond, testbudget.Wait(t))
	if !bytes.Contains(output.since(0), []byte("alpha.md")) {
		t.Fatalf("config selector did not render: %q", output.since(0))
	}
	if kitty {
		// A Kitty-capable terminal answers the flags query; from then on it reports keys as CSI-u.
		if _, err := master.Write([]byte("\x1b[?1u")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	mark := output.mark()
	if _, err := master.Write([]byte(key)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("%q did not exit pig config", key)
	}
	<-copied
	if waitErr != nil {
		t.Fatalf("exit = %v, want status 0; stderr = %q", waitErr, stderr.String())
	}
	tail := output.since(mark)
	for _, want := range []string{"\x1b[?25h", "\x1b[?2004l"} {
		if !bytes.Contains(tail, []byte(want)) {
			t.Errorf("shutdown output %q lacks terminal restore %q", tail, want)
		}
	}
	if kitty && !bytes.Contains(tail, []byte("\x1b[<u")) {
		t.Errorf("shutdown output %q does not pop the Kitty keyboard flags", tail)
	}
	if park, show := bytes.Index(tail, []byte("\r\n")), bytes.Index(tail, []byte("\x1b[?25h")); park < 0 || show < park {
		t.Errorf("shutdown output %q does not park the cursor below the selector", tail)
	}
	termios, err := unix.IoctlGetTermios(int(master.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if termios.Lflag&(unix.ICANON|unix.ECHO) != unix.ICANON|unix.ECHO {
		t.Errorf("terminal left raw: lflag=%#x", termios.Lflag)
	}
}
