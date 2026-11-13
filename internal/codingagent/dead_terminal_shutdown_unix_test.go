//go:build linux || darwin

package codingagent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

const deadTerminalShutdownChildEnv = "PIG_DEAD_TERMINAL_SHUTDOWN_CHILD"

// shutdownProbeHost is a runtime host whose quit shutdown, which emits session_shutdown to the extensions, is the test's.
type shutdownProbeHost struct {
	InteractiveRuntime
	quit func()
}

func (h *shutdownProbeHost) EmitQuitShutdown() { h.quit() }

// TestDeadTerminalShutdownChild runs one dead-terminal ending in a child process, because the emergency exit ends the process.
// The shutdown appends a line to the file "shutdowns" each time it runs.
func TestDeadTerminalShutdownChild(t *testing.T) {
	scenario := os.Getenv(deadTerminalShutdownChildEnv)
	if scenario == "" {
		t.Skip("child process only")
	}
	dir := os.Getenv("PIG_DEAD_TERMINAL_AGENT_DIR")
	note := func(line string) {
		// A line that cannot be written is a line the parent misses.
		f, err := os.OpenFile(filepath.Join(dir, "shutdowns"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		_, _ = f.WriteString(line + "\n")
		_ = f.Close()
	}
	host := &shutdownProbeHost{}
	m := NewInteractiveMode(host, InteractiveModeOptions{CWD: "/work", AgentDir: dir, AppVersion: "9.9.9"})
	dead := &os.PathError{Op: "read", Path: "/dev/tty", Err: syscall.EIO}
	entered := make(chan struct{})
	switch scenario {
	case "handler":
		// A handler that takes a while: the exit waits for it.
		host.quit = func() {
			note("quit")
			time.Sleep(100 * time.Millisecond)
			note("settled")
		}
		m.exitIfDeadTerminal(dead)
	case "blocked-handler":
		host.quit = func() {
			note("quit")
			select {}
		}
		m.exitIfDeadTerminal(dead)
	case "signal-then-dead-terminal":
		// The termination signal's goroutine holds the emission in a handler that never returns; the dead terminal then joins it.
		host.quit = func() {
			note("quit")
			close(entered)
			select {}
		}
		go m.ShutdownFromSignal()
		<-entered
		m.exitIfDeadTerminal(dead)
	case "handler-writes-to-the-terminal":
		// A handler's UI call writes to the hung-up terminal: the write fails, returns and does not start a second exit.
		master, slave := openTestPTY(t)
		if err := master.Close(); err != nil {
			t.Fatal(err)
		}
		_ = m.exitOnDeadTerminalWrite()
		terminal := tui.NewProcessTerminal(slave, slave)
		host.quit = func() {
			note("quit")
			for range 3 {
				terminal.Write("frame")
			}
			note("wrote")
		}
		m.exitIfDeadTerminal(dead)
	case "write-fails-first":
		// The first sign of the dead terminal is a failed write, whose goroutine holds the terminal's output lock through the
		// exit; the handler then writes to the same terminal and cannot return.
		master, slave := openTestPTY(t)
		if err := master.Close(); err != nil {
			t.Fatal(err)
		}
		_ = m.exitOnDeadTerminalWrite()
		terminal := tui.NewProcessTerminal(slave, slave)
		host.quit = func() {
			note("quit")
			terminal.Write("frame")
			note("wrote")
		}
		terminal.Write("frame")
	}
	_, _ = os.Stdout.WriteString("survived\n")
	os.Exit(0)
}

// A terminal that goes away gives extensions session_shutdown before the exit, as Pi's SIGHUP handler does before its
// restore writes fail (interactive-mode.ts registerSignalHandlers, shutdown fromSignal, emergencyTerminalExit): the handlers
// run once, the exit waits for them for at most the second of shutdownExtensionsBriefly, and the exit stays 129 with one
// exit.log line. PiG used to exit without the event (#25 of the copiglet filing).
func TestDeadTerminalExitGivesExtensionsTheirShutdownFirst(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		seen     string
		// held is how long the exit is expected to wait at least.
		held time.Duration
	}{
		{"handler", "quit\nsettled\n", 100 * time.Millisecond},
		{"blocked-handler", "quit\n", time.Second},
		{"signal-then-dead-terminal", "quit\n", time.Second},
		{"handler-writes-to-the-terminal", "quit\nwrote\n", 0},
		{"write-fails-first", "quit\n", 0},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			dir := t.TempDir()
			// The deadline is a hang detector: without the bound a blocked handler holds the exit for good.
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDeadTerminalShutdownChild$")
			cmd.Env = append(os.Environ(), deadTerminalShutdownChildEnv+"="+tc.scenario, "PIG_DEAD_TERMINAL_AGENT_DIR="+dir, "PIG_HOME="+t.TempDir())
			start := time.Now()
			output, err := cmd.CombinedOutput()
			elapsed := time.Since(start)
			code := 0
			if exit, ok := errors.AsType[*exec.ExitError](err); ok {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != 129 || strings.Contains(string(output), "survived") {
				t.Fatalf("exit code %d after %s, want 129 without carrying on\n%s", code, elapsed, output)
			}
			seen, _ := os.ReadFile(filepath.Join(dir, "shutdowns"))
			if !strings.HasPrefix(string(seen), tc.seen) || strings.Count(string(seen), "quit\n") != 1 {
				t.Errorf("shutdowns = %q, want %q with the shutdown run once", seen, tc.seen)
			}
			if elapsed < tc.held {
				t.Errorf("the process exited after %s, want the exit to wait %s for the handler", elapsed, tc.held)
			}
			log, _ := os.ReadFile(filepath.Join(dir, "exit.log"))
			if lines := strings.Split(strings.TrimSuffix(string(log), "\n"), "\n"); len(lines) != 1 || !strings.Contains(lines[0], " terminal gone: ") || !strings.HasSuffix(lines[0], " (exit 129)") {
				t.Errorf("exit.log = %q, want one terminal-gone line with exit 129", log)
			}
		})
	}
}
