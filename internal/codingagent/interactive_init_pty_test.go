//go:build linux

package codingagent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const initAfterTuiChildEnv = "PIG_TEST_INIT_AFTER_TUI_CHILD"

// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:998-999. init() sets isInitialized as soon as
// the TUI started, so an init() that throws after that point still counts as done: a second init() returns at once, and
// stop() (:7102-7105) stops the TUI and clears isInitialized. Init needs a terminal for raw mode, so the check runs in a
// child on a pseudo-terminal.
func TestInitFailingAfterTheTuiStartsStaysInitialized(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	defer func() { _ = master.Close() }()
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock pseudo-terminal: %v", err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("pseudo-terminal number: %v", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pseudo-terminal slave: %v", err)
	}
	defer func() { _ = slave.Close() }()
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 30, Col: 100}); err != nil {
		t.Fatalf("set pseudo-terminal size: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestInitFailingAfterTheTuiStartsChild$", "-test.count=1", "-test.v")
	report := t.TempDir() + "/report"
	cmd.Env = append(os.Environ(), initAfterTuiChildEnv+"="+report, "TERM=xterm-256color", "PIG_HOME="+t.TempDir())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	var captured bytes.Buffer
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(&captured, master)
	}()
	waitErr := cmd.Wait()
	select {
	case <-copied:
	case <-time.After(5 * time.Second):
		_ = master.Close()
		<-copied
	}
	if waitErr != nil || !bytes.Contains(captured.Bytes(), []byte("--- PASS: TestInitFailingAfterTheTuiStartsChild")) {
		failure, _ := os.ReadFile(report)
		t.Fatalf("child failed: %v: %s\n%q", waitErr, failure, tailBytes(captured.Bytes(), 400))
	}
}

func tailBytes(b []byte, n int) []byte {
	if len(b) > n {
		return b[len(b)-n:]
	}
	return b
}

// TestInitFailingAfterTheTuiStartsChild is the child half of TestInitFailingAfterTheTuiStartsStaysInitialized.
func TestInitFailingAfterTheTuiStartsChild(t *testing.T) {
	report := os.Getenv(initAfterTuiChildEnv)
	if report == "" {
		t.Skip("runs only as the pty child of TestInitFailingAfterTheTuiStartsStaysInitialized")
	}
	// The terminal UI owns the pseudo-terminal, so the child reports its failure through a file.
	t.Cleanup(func() {
		if t.Failed() {
			_ = os.WriteFile(report, []byte(childFailure), 0o600)
		}
	})
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), NoThemes: true})
	// Without a Session, Init fails after it built the TUI, where Pi's init() would throw from a later step.
	var first []string
	m.opts.StartupMark = func(label string) { first = append(first, label) }
	if err := m.Init(t.Context()); err == nil || !slices.Contains(first, "tui-layout-built") {
		childFatalf(t, "first Init = %v after marks %q, want a failure after the TUI started", err, first)
	}
	if !m.isInitialized {
		childFatalf(t, "an Init that failed after the TUI started is not initialized")
	}
	var again []string
	m.opts.StartupMark = func(label string) { again = append(again, label) }
	if err := m.Init(t.Context()); err != nil || again != nil {
		childFatalf(t, "second Init = %v after marks %q, want an immediate return", err, again)
	}
	if err := m.Stop(); err != nil {
		childFatalf(t, "%v", err)
	}
	if m.isInitialized || !m.runEnded.Load() {
		childFatalf(t, "Stop left isInitialized=%v runEnded=%v", m.isInitialized, m.runEnded.Load())
	}
}

// childFailure is the last failure message of the pty child.
var childFailure string

func childFatalf(t *testing.T, format string, args ...any) {
	t.Helper()
	childFailure = fmt.Sprintf(format, args...)
	t.Fatal(childFailure)
}
