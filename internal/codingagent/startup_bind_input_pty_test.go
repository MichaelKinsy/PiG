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
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const (
	startupBindChildEnv   = "PIG_TEST_STARTUP_BIND_CHILD"
	startupBindHandlerEnv = "PIG_TEST_STARTUP_BIND_HANDLER"
	// The values of startupBindHandlerEnv: no session_start handler, one that pends, one that returns without awaiting, both of those.
	startupBindNone                = "none"
	startupBindPending             = "pending"
	startupBindReturnsWithoutAwait = "returns-without-await"
	startupBindMixed               = "mixed"
)

// Pi's init() awaits rebindCurrentSession() after setupEditorSubmitHandler (interactive-mode.ts:1127-1141) and run() shows the startup notices, among them
// modelFallbackMessage, when init() returns (:1187-1258). Node reads stdin only between tasks: when nothing in the bind pends on I/O the await settles in
// microtasks, so a key typed ahead is not read, and no command it submits runs, before the notices. The key is held ready on the owner loop's input channel
// before the bind starts; Init must leave it unread through the bind and the notices.
func TestInitReadsNoTypedAheadKeyBeforeTheStartupNoticesWhenNoHandlerPends(t *testing.T) {
	report := runStartupBindChild(t, startupBindNone)
	if report.UnreadAfterBind == nil {
		t.Fatalf("the child never reached the end of the bind: %+v", report)
	}
	if !*report.UnreadAfterBind {
		t.Fatal("Init read a typed-ahead key while it bound a session with no session_start or resources_discover handler: a command it submitted would run before the startup notices")
	}
}

// A session_start handler registered as one that returns without awaiting (Pi's built-in MCP extension has one: extensions/mcp/index.ts:1115-1152) still has its await settle
// in microtasks, so a key typed ahead is not read before the notices and the event loop takes no turn that would paint the header's frame before init() ends. No path or name
// of an extension decides it.
func TestInitReadsNoTypedAheadKeyBeforeTheStartupNoticesWhenEveryHandlerReturnsWithoutAwaiting(t *testing.T) {
	report := runStartupBindChild(t, startupBindReturnsWithoutAwait)
	if report.UnreadAfterBind == nil {
		t.Fatalf("the child never reached the end of the bind: %+v", report)
	}
	if !*report.UnreadAfterBind {
		t.Fatal("Init served the terminal while it bound a session whose only session_start handler returns without awaiting")
	}
}

// One handler that can pend among handlers that return without awaiting makes the bind pend: Pi's event loop reads stdin while it awaits that one.
func TestInitReadsATypedAheadKeyWhenOneSessionStartHandlerPendsAmongHandlersThatReturnWithoutAwaiting(t *testing.T) {
	report := runStartupBindChild(t, startupBindMixed)
	if report.UnreadAfterBind == nil {
		t.Fatalf("the child never reached the end of the bind: %+v", report)
	}
	if *report.UnreadAfterBind {
		t.Fatal("Init left a typed-ahead key unread while a session_start handler that can pend ran next to one that returns without awaiting")
	}
}

// When a session_start handler pends, Pi's event loop reads stdin while it awaits it (the submit handler of :1127-1128 is already set), so a key typed ahead
// is read, and a /login it submits runs, before the notices: Pig serves the terminal while the handler runs.
func TestInitReadsATypedAheadKeyWhileASessionStartHandlerPends(t *testing.T) {
	report := runStartupBindChild(t, startupBindPending)
	if report.UnreadAfterBind == nil {
		t.Fatalf("the child never reached the end of the bind: %+v", report)
	}
	if *report.UnreadAfterBind {
		t.Fatal("Init did not serve the terminal while a session_start handler pended")
	}
}

type startupBindReport struct {
	Failure         string
	InitErr         string
	UnreadAfterBind *bool
}

func runStartupBindChild(t *testing.T, handler string) startupBindReport {
	t.Helper()
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
	reportPath := filepath.Join(t.TempDir(), "report.json")
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestInitStartupBindChild$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), startupBindChildEnv+"="+reportPath, startupBindHandlerEnv+"="+handler, "TERM=xterm-256color", "PIG_HOME="+t.TempDir())
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
	raw, readErr := os.ReadFile(reportPath)
	if waitErr != nil || readErr != nil || !bytes.Contains(captured.Bytes(), []byte("--- PASS: TestInitStartupBindChild")) {
		t.Fatalf("child failed: %v %v\n%s\n%q", waitErr, readErr, raw, captured.Bytes()[max(0, captured.Len()-600):])
	}
	var report startupBindReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Failure != "" || report.InitErr != "" {
		t.Fatalf("child reported %+v", report)
	}
	return report
}

// TestInitStartupBindChild is the child half of the four tests above: the real Init on a pseudo-terminal with a real Session whose runner has no session_start
// handler, one that returns without awaiting, one that returns only once the held key has been read, or both of those.
func TestInitStartupBindChild(t *testing.T) {
	reportPath := os.Getenv(startupBindChildEnv)
	if reportPath == "" {
		t.Skip("runs only as the pty child of the startup bind tests")
	}
	report := startupBindReport{}
	defer func() {
		raw, _ := json.Marshal(report)
		_ = os.WriteFile(reportPath, raw, 0o600)
	}()
	fail := func(format string, args ...any) {
		report.Failure = fmt.Sprintf(format, args...)
		t.Fatal(report.Failure)
	}
	agentDir := filepath.Join(os.Getenv("PIG_HOME"), "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
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
	var mode *icodingagent.InteractiveMode
	ext := extension.Extension{Path: "/startup-bind", Handlers: map[string][]extension.HandlerFn{"agent_start": {func(...any) (any, error) { return nil, nil }}}}
	// The handler pends until the owner loop has read the held key, as a Node handler pends on I/O while stdin is read.
	pends := func(...any) (any, error) {
		for mode.HeldStartupInputUnread() && t.Context().Err() == nil {
			runtime.Gosched()
		}
		return nil, nil
	}
	returnsAtOnce := func(...any) (any, error) { return nil, nil }
	switch os.Getenv(startupBindHandlerEnv) {
	case startupBindPending:
		ext.AddEventHandler("session_start", 1, pends)
	case startupBindReturnsWithoutAwait:
		ext.AddEventHandlerReturningWithoutAwait("session_start", 1, returnsAtOnce)
	case startupBindMixed:
		ext.AddEventHandlerReturningWithoutAwait("session_start", 1, returnsAtOnce)
		ext.AddEventHandler("session_start", 2, pends)
	}
	runner := inproc.NewRunner([]extension.Extension{ext}, cwd)
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, SessionManager: manager, Runner: runner})
	if err != nil {
		fail("%v", err)
	}
	defer func() { _ = session.Close() }()
	mode = icodingagent.NewInteractiveMode(nil, icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session, NoThemes: true, ExtensionRunner: runner,
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(),
		StartupMark: func(label string) {
			switch label {
			case "pre-emit-session-start":
				mode.HoldStartupInput("k")
			case "post-emit-session-start":
				unread := mode.HeldStartupInputUnread()
				report.UnreadAfterBind = &unread
			}
		},
	})
	if err := mode.Init(t.Context()); err != nil {
		report.InitErr = err.Error()
	}
	if err := mode.Stop(); err != nil {
		fail("Stop: %v", err)
	}
}
