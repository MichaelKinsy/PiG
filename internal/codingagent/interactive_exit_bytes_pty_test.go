//go:build linux

package codingagent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

const (
	exitBytesChildEnv    = "PIG_TEST_EXIT_BYTES_CHILD"
	exitBytesProgressEnv = "PIG_TEST_EXIT_BYTES_PROGRESS"
	exitBytesRunEnv      = "PIG_TEST_EXIT_BYTES_RUN"
)

// exitTitleOrProgress matches a terminal title (OSC 0) or a progress indicator (OSC 9;4) sequence.
var exitTitleOrProgress = regexp.MustCompile("\x1b\\](?:0|9;4);[^\x07\x1b]*(?:\x07|\x1b\\\\)")

// When interactive mode ends, the mode itself writes one sequence of the two kinds, and only when terminal.showTerminalProgress is
// on: stop() clears the progress indicator before it stops the renderer (interactive-mode.ts:7283-7287, `if
// (this.settingsManager.getShowTerminalProgress()) this.ui.terminal.setProgress(false)`, ahead of stopInteractiveTui at :7296-7299).
// It never writes an empty title: Pi has no setTitle("") at all. shutdown() reaches stop() on a quit (ctrl+d, ctrl+c twice, /quit;
// :4500-4507) and on SIGTERM and SIGHUP (:4483-4494).
//
// Each row runs a mode with a real Session in a child on a pseudo-terminal, ends it the way the row names, and reads what the
// renderer's terminal (InteractiveModeOptions.Terminal) received from the moment the mode was ready for input. The mode has no
// runtime host, so the session title that the host's dispose() makes the mode write is not part of these bytes.
func TestExitWritesOnlyTheProgressClearThePiStopWrites(t *testing.T) {
	keys := func(data string) func(*testing.T, *exitBytesChild) {
		return func(_ *testing.T, child *exitBytesChild) { child.write(data) }
	}
	signalChild := func(sig syscall.Signal) func(*testing.T, *exitBytesChild) {
		return func(t *testing.T, child *exitBytesChild) {
			if err := child.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
		}
	}
	ends := []struct {
		name string
		// run is whether an agent run started and ended before the mode ends.
		run bool
		end func(*testing.T, *exitBytesChild)
	}{
		{"ctrl+d", false, keys("\x04")},
		{"quit command", false, func(_ *testing.T, child *exitBytesChild) {
			from := child.captured.len()
			child.write("/quit")
			child.await("the editor to show /quit", func() bool { return strings.Contains(child.captured.from(from), "/quit") })
			child.write("\r")
		}},
		{"ctrl+c twice", false, keys("\x03\x03")},
		{"ctrl+d after a run", true, keys("\x04")},
		{"SIGTERM", false, signalChild(syscall.SIGTERM)},
		{"SIGHUP", false, signalChild(syscall.SIGHUP)},
		{"SIGTERM after a run", true, signalChild(syscall.SIGTERM)},
	}
	for _, progress := range []bool{false, true} {
		for _, end := range ends {
			t.Run(fmt.Sprintf("%s with showTerminalProgress %v", end.name, progress), func(t *testing.T) {
				child := startExitBytesChild(t, progress, end.run)
				child.await("the mode to be ready for input", child.ready)
				end.end(t, child)
				written := child.finish()
				var want []string
				if progress {
					want = []string{"\x1b]9;4;0\x07"}
				}
				if got := exitTitleOrProgress.FindAllString(written, -1); !slices.Equal(got, want) {
					t.Fatalf("the renderer's terminal received the title and progress sequences %q while the mode ended, want %q\nlast bytes: %q",
						got, want, written[max(0, len(written)-300):])
				}
				if !progress {
					return
				}
				// The renderer's stop shows the cursor again; on the terminal option that is the last write of that sequence.
				clearAt, stopAt := strings.Index(written, want[0]), strings.LastIndex(written, "\x1b[?25h")
				if stopAt < 0 || clearAt > stopAt {
					t.Fatalf("the progress clear is at byte %d and the renderer's stop at %d, want the clear before the stop\nlast bytes: %q",
						clearAt, stopAt, written[max(0, len(written)-300):])
				}
			})
		}
	}
}

// exitBytesBuffer collects bytes written on one goroutine while another reads them.
type exitBytesBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *exitBytesBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *exitBytesBuffer) len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// from returns the bytes collected after the first n.
func (b *exitBytesBuffer) from(n int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf.Bytes()[n:])
}

// exitBytesChild is TestExitBytesChild running on a pseudo-terminal whose other end the test holds.
type exitBytesChild struct {
	t        *testing.T
	cmd      *exec.Cmd
	master   *os.File
	dir      string
	captured exitBytesBuffer
	copied   chan struct{}
	exited   chan struct{}
	waitErr  error
}

func startExitBytesChild(t *testing.T, progress, run bool) *exitBytesChild {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
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
	t.Cleanup(cancel)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := &exitBytesChild{t: t, master: master, dir: t.TempDir(), copied: make(chan struct{}), exited: make(chan struct{})}
	child.cmd = exec.CommandContext(ctx, executable, "-test.run=^TestExitBytesChild$", "-test.count=1")
	child.cmd.Env = append(os.Environ(), exitBytesChildEnv+"="+child.dir, exitBytesProgressEnv+"="+strconv.FormatBool(progress),
		exitBytesRunEnv+"="+strconv.FormatBool(run), "TERM=xterm-256color", "PIG_HOME="+t.TempDir())
	child.cmd.Stdin, child.cmd.Stdout, child.cmd.Stderr = slave, slave, slave
	child.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := child.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(child.copied)
		_, _ = io.Copy(&child.captured, master)
	}()
	go func() {
		defer close(child.exited)
		child.waitErr = child.cmd.Wait()
	}()
	return child
}

func (c *exitBytesChild) write(data string) {
	c.t.Helper()
	if _, err := c.master.WriteString(data); err != nil {
		c.t.Fatal(err)
	}
}

// ready is whether the child's mode reached its input loop.
func (c *exitBytesChild) ready() bool {
	_, err := os.Stat(filepath.Join(c.dir, "ready"))
	return err == nil
}

// await returns once done holds. It fails the test when the child ends first or the wait budget runs out.
func (c *exitBytesChild) await(what string, done func() bool) {
	c.t.Helper()
	deadline := time.Now().Add(testbudget.Wait(c.t))
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !done() {
		select {
		case <-c.exited:
			c.t.Fatalf("the child ended (%v) while the test waited for %s\n%q", c.waitErr, what, c.tail())
		case now := <-tick.C:
			if now.After(deadline) {
				c.t.Fatalf("timed out waiting for %s\n%q", what, c.tail())
			}
		}
	}
}

func (c *exitBytesChild) tail() string {
	return c.captured.from(max(0, c.captured.len()-600))
}

// finish waits for the child to end and returns what the renderer's terminal received after the mode was ready.
func (c *exitBytesChild) finish() string {
	c.t.Helper()
	<-c.exited
	select {
	case <-c.copied:
	case <-time.After(5 * time.Second):
		_ = c.master.Close()
		<-c.copied
	}
	raw, readErr := os.ReadFile(filepath.Join(c.dir, "report.json"))
	var report exitBytesReport
	if c.waitErr != nil || readErr != nil || json.Unmarshal(raw, &report) != nil || report.Failure != "" {
		c.t.Fatalf("child failed: %v %v %s\n%q", c.waitErr, readErr, report.Failure, c.tail())
	}
	return string(report.Written)
}

// exitBytesReport is what the child leaves for the test.
type exitBytesReport struct {
	Failure string
	// Written is what the renderer's terminal received after the mode was ready for input.
	Written []byte
}

// TestExitBytesChild is the child half of TestExitWritesOnlyTheProgressClearThePiStopWrites: Run on the pseudo-terminal, with the
// renderer on a terminal option that also keeps what it writes.
func TestExitBytesChild(t *testing.T) {
	dir := os.Getenv(exitBytesChildEnv)
	if dir == "" {
		t.Skip("runs only as the pty child of TestExitWritesOnlyTheProgressClearThePiStopWrites")
	}
	var report exitBytesReport
	defer func() {
		raw, _ := json.Marshal(report)
		_ = os.WriteFile(filepath.Join(dir, "report.json"), raw, 0o600)
	}()
	fail := func(format string, args ...any) {
		report.Failure = fmt.Sprintf(format, args...)
		t.Fatal(report.Failure)
	}
	agentDir := filepath.Join(os.Getenv("PIG_HOME"), "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		fail("%v", err)
	}
	settings := `{"terminal":{"showTerminalProgress":` + os.Getenv(exitBytesProgressEnv) + `}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
		fail("%v", err)
	}
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		fail("%v", err)
	}
	manager, err := coding.NewInMemorySessionManager(cwd)
	if err != nil {
		fail("%v", err)
	}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, SessionManager: manager})
	if err != nil {
		fail("%v", err)
	}
	defer func() { _ = session.Close() }()

	var written exitBytesBuffer
	terminal := tui.NewProcessTerminalWithOutput(os.Stdin, os.Stdout, io.MultiWriter(os.Stdout, &written))
	var mode *icodingagent.InteractiveMode
	readyAt := -1
	mode = icodingagent.NewInteractiveMode(nil, icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session, NoThemes: true,
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(), Terminal: terminal,
		// The mark is made on the owner loop's goroutine, right before the loop reads input.
		StartupMark: func(label string) {
			if label != "interactive-ready" {
				return
			}
			if os.Getenv(exitBytesRunEnv) == "true" {
				mode.HandleAgentEventsOnOwnerLoop(agent.TurnStartEvent{}, agent.AgentEndEvent{})
			}
			readyAt = written.len()
			if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0o600); err != nil {
				report.Failure = err.Error()
			}
		},
	})
	// The process's termination hook: session_shutdown first, then the root context ends (coding/cli/main.go, the hook set with
	// setTerminationShutdownHook).
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	go func() {
		select {
		case <-signals:
			mode.ShutdownFromSignal()
			cancel()
		case <-ctx.Done():
		}
	}()
	// A run that a signal ends returns the context's error; what the terminal received is the subject.
	_ = mode.Run(ctx)
	if readyAt < 0 {
		fail("the mode ended before it was ready for input")
	}
	report.Written = []byte(written.from(readyAt))
	if report.Failure != "" {
		t.Fatal(report.Failure)
	}
}
