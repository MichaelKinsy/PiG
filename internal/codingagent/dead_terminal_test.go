package codingagent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

// Ports packages/coding-agent/test/suite/regressions/5080-signal-shutdown-extension-cleanup.test.ts "dead terminal errors on stdin" (Pi 1.0.3):
// EIO, EPIPE, ENOTCONN and ENOTTY mean the terminal is gone, so the process exits 129 without a crash record. Any other
// error is still a crash.

func TestIsDeadTerminalError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"read EIO", fmt.Errorf("read /dev/tty: %w", syscall.EIO), true},
		{"setRawMode EIO", &os.PathError{Op: "ioctl", Path: "/dev/pts/1", Err: syscall.EIO}, true},
		{"setRawMode ENOTTY", &os.PathError{Op: "ioctl", Path: "/dev/pts/1", Err: syscall.ENOTTY}, true},
		{"EPIPE", syscall.EPIPE, true},
		{"ENOTCONN", syscall.ENOTCONN, true},
		{"read ECONNREFUSED", syscall.ECONNREFUSED, false},
		{"EOF", io.EOF, false},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDeadTerminalError(tc.err); got != tc.want {
				t.Fatalf("isDeadTerminalError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

const deadTerminalChildEnv = "PIG_DEAD_TERMINAL_CHILD"

// TestDeadTerminalChild runs one scenario in a child process, because the emergency exit ends the process.
func TestDeadTerminalChild(t *testing.T) {
	scenario := os.Getenv(deadTerminalChildEnv)
	if scenario == "" {
		t.Skip("child process only")
	}
	agentDir := os.Getenv("PIG_DEAD_TERMINAL_AGENT_DIR")
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work", AgentDir: agentDir, AppVersion: "9.9.9"})
	var stderr strings.Builder
	switch scenario {
	case "uncaught-eio":
		m.uncaughtCrash(&os.PathError{Op: "read", Path: "/dev/tty", Err: syscall.EIO}, []byte("stack\n"), &stderr)
	case "uncaught-enotty":
		m.uncaughtCrash(fmt.Errorf("setRawMode: %w", syscall.ENOTTY), []byte("stack\n"), &stderr)
	case "uncaught-other":
		m.uncaughtCrash(errors.New("boom"), []byte("stack\n"), &stderr)
	case "exit-if-dead-eio":
		m.exitIfDeadTerminal(&os.PathError{Op: "ioctl", Path: "/dev/tty", Err: syscall.EIO})
	case "exit-if-dead-other":
		m.exitIfDeadTerminal(syscall.ECONNREFUSED)
	}
	fmt.Print("survived\n" + stderr.String())
	os.Exit(0)
}

func TestDeadTerminalExitsQuietlyWithoutRecordingACrash(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		exit     int
		records  int
	}{
		{"uncaught-eio", 129, 0},
		{"uncaught-enotty", 129, 0},
		{"exit-if-dead-eio", 129, 0},
		{"uncaught-other", 0, 1},
		{"exit-if-dead-other", 0, 0},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			agentDir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestDeadTerminalChild$")
			cmd.Env = append(os.Environ(), deadTerminalChildEnv+"="+tc.scenario, "PIG_DEAD_TERMINAL_AGENT_DIR="+agentDir, "PIG_HOME="+t.TempDir())
			output, err := cmd.CombinedOutput()
			code := 0
			if exit, ok := errors.AsType[*exec.ExitError](err); ok {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != tc.exit {
				t.Fatalf("exit code = %d, want %d\n%s", code, tc.exit, output)
			}
			if tc.exit == 129 && (strings.Contains(string(output), "survived") || strings.Contains(string(output), "uncaughtException")) {
				t.Errorf("dead terminal reported: %s", output)
			}
			if got := len(ReadCrashLog(CrashLogPath(agentDir))); got != tc.records {
				t.Errorf("crash records = %d, want %d", got, tc.records)
			}
		})
	}
}

// attachedInput counts the readers waiting on it, as Pi's test counts process.stdin "error" listeners. A reader waits until Close.
type attachedInput struct {
	attached atomic.Int32
	entered  chan struct{}
	release  chan struct{}
	enter    sync.Once
	close    sync.Once
	closes   atomic.Int32
}

func newAttachedInput() *attachedInput {
	return &attachedInput{entered: make(chan struct{}), release: make(chan struct{})}
}

func (a *attachedInput) Read([]byte) (int, error) {
	a.attached.Add(1)
	defer a.attached.Add(-1)
	a.enter.Do(func() { close(a.entered) })
	<-a.release
	return 0, io.EOF
}

func (a *attachedInput) Close() error {
	a.closes.Add(1)
	a.close.Do(func() { close(a.release) })
	return nil
}

// Ports packages/coding-agent/test/suite/regressions/5080-signal-shutdown-extension-cleanup.test.ts "dead terminal errors on stdin › handlers are removed on unregister"
// (Pi 1.0.3). Pi's registerSignalHandlers adds one process.stdin "error" listener that exits quietly on a dead terminal, and unregisterSignalHandlers removes it.
// PiG registers no stdin listener: its input pump reads the terminal and routes a dead-terminal read error to exitIfDeadTerminal. Unregistering
// is stopTerminalInput, which must leave no reader attached, so a dead-terminal error after unregistration reaches no handler.
func TestInputHandlersAreRemovedOnUnregister(t *testing.T) {
	var order []string
	m := upstreamShutdownMode(t, &order, false)
	source := newAttachedInput()
	m.startTerminalInput(t.Context(), source)
	<-source.entered
	before := source.attached.Load()
	if before != 1 {
		t.Fatalf("readers attached before unregister = %d, want 1", before)
	}
	readCh := m.inputReadCh

	if err := m.stopTerminalInput(); err != nil {
		t.Fatal(err)
	}

	if after := source.attached.Load(); after != before-1 {
		t.Fatalf("readers attached after unregister = %d, want %d", after, before-1)
	}
	if m.inputOwner != nil {
		t.Fatal("the input owner survived unregister")
	}
	if _, open := <-readCh; open {
		t.Fatal("the input pump still delivers after unregister")
	}
	if err := m.stopTerminalInput(); err != nil {
		t.Fatalf("unregistering twice: %v", err)
	}
	if got := source.closes.Load(); got != 1 {
		t.Fatalf("source closed %d times, want 1", got)
	}
}
