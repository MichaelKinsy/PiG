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
	"slices"
	"strings"
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
)

// Pi's init() awaits rebindCurrentSession() after setupEditorSubmitHandler (interactive-mode.ts:1127-1141) and run() shows the startup notices, among them
// modelFallbackMessage, when init() returns (:1187-1258). Node reads stdin only between tasks: when nothing in the bind pends on I/O the await settles in
// microtasks, so a key typed ahead is not read, and no command it submits runs, before the notices. The key is held ready on the owner loop's input channel
// before the bind starts; Init must leave it unread through the bind and the notices.
func TestInitReadsNoTypedAheadKeyBeforeTheStartupNoticesWhenNoHandlerPends(t *testing.T) {
	report := runStartupBindChild(t, false)
	if report.UnreadAfterBind == nil {
		t.Fatalf("the child never reached the end of the bind: %+v", report)
	}
	if !*report.UnreadAfterBind {
		t.Fatal("Init read a typed-ahead key while it bound a session with no session_start or resources_discover handler: a command it submitted would run before the startup notices")
	}
}

// When a session_start handler pends, Pi's event loop reads stdin while it awaits it (the submit handler of :1127-1128 is already set), so a key typed ahead
// is read, and a /login it submits runs, before the notices: Pig serves the terminal while the handler runs.
func TestInitReadsATypedAheadKeyWhileASessionStartHandlerPends(t *testing.T) {
	report := runStartupBindChild(t, true)
	if report.UnreadAfterBind == nil {
		t.Fatalf("the child never reached the end of the bind: %+v", report)
	}
	if *report.UnreadAfterBind {
		t.Fatal("Init did not serve the terminal while a session_start handler pended")
	}
}

// Pi's editor has no autocomplete provider until setupAutocompleteProvider runs, and that runs only after `await this.session.bindExtensions(...)` returns
// (interactive-mode.ts:2167; the editor's autocompleteProvider is unset until then, editor.ts:328, and every completion entry point returns on it: handleTabCompletion
// :2277, the slash and @ triggers :2298, :2382). Text a user types while a session_start handler pends therefore opens no menu. Init builds the editor and
// binds the Session; the provider is on the editor after the bind and not before it, whether or not a handler pends.
func TestInitInstallsTheAutocompleteProviderWhenTheBindEnds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler bool
	}{{"no handler pends", false}, {"a session_start handler pends", true}} {
		t.Run(tc.name, func(t *testing.T) {
			report := runStartupBindChild(t, tc.handler)
			if report.BeforeBind == nil || report.AfterBind == nil {
				t.Fatalf("the child did not report both ends of the bind: %+v", report)
			}
			if report.BeforeBind.Installed {
				t.Fatalf("the editor had an autocomplete provider offering %q for /fo before the Session was bound; Pi's has none until bindExtensions returns", report.BeforeBind.Offers)
			}
			if !report.AfterBind.Installed || !slices.Contains(report.AfterBind.Offers, "fork") {
				t.Fatalf("after the bind the editor's provider offers %q for /fo (installed %v); want the built-in commands, fork among them", report.AfterBind.Offers, report.AfterBind.Installed)
			}
		})
	}
}

// Pi's addAutocompleteProvider runs setupAutocompleteProvider, which builds the base provider itself (interactive-mode.ts:2800-2801, :854), so a provider an
// extension adds from session_start gives the editor the built-in commands plus its own entries before the bind ends. Text typed then opens a popup of
// both; with the editor holding no provider until the bind's setup, the chain must not stand on nothing.
func TestAProviderAddedFromSessionStartStandsOnTheBuiltInCommandsBeforeTheBindEnds(t *testing.T) {
	report := runStartupBindChildMode(t, "2")
	if !strings.Contains(report.Popup, "fork") || !strings.Contains(report.Popup, "focus-added") {
		t.Fatalf("the popup for /fo typed while session_start pends shows:\n%s\nwant the built-in fork and the extension's focus-added", report.Popup)
	}
}

// startupBindProvider is what the editor's autocomplete provider answered at one end of the bind.
type startupBindProvider struct {
	Installed bool
	Offers    []string
}

type startupBindReport struct {
	Failure         string
	InitErr         string
	UnreadAfterBind *bool
	BeforeBind      *startupBindProvider
	AfterBind       *startupBindProvider
	Popup           string
}

func runStartupBindChild(t *testing.T, handler bool) startupBindReport {
	t.Helper()
	if handler {
		return runStartupBindChildMode(t, "1")
	}
	return runStartupBindChildMode(t, "0")
}

// runStartupBindChildMode runs the child with its session_start handler absent ("0"), pending until the held key is read ("1"), or adding an autocomplete provider and typing "/fo" ("2").
func runStartupBindChildMode(t *testing.T, mode string) startupBindReport {
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
	cmd.Env = append(os.Environ(), startupBindChildEnv+"="+reportPath, startupBindHandlerEnv+"="+mode, "TERM=xterm-256color", "PIG_HOME="+t.TempDir())
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

// TestInitStartupBindChild is the child half of the two tests above: the real Init on a pseudo-terminal with a real Session whose runner has no session_start
// handler, or one that returns only once the held key has been read.
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
	handlers := map[string][]extension.HandlerFn{"agent_start": {func(...any) (any, error) { return nil, nil }}}
	if os.Getenv(startupBindHandlerEnv) == "1" {
		handlers["session_start"] = []extension.HandlerFn{func(...any) (any, error) {
			// The handler pends until the owner loop has read the held key, as a Node handler pends on I/O while stdin is read.
			for mode.HeldStartupInputUnread() && t.Context().Err() == nil {
				runtime.Gosched()
			}
			return nil, nil
		}}
	}
	if os.Getenv(startupBindHandlerEnv) == "2" {
		handlers["session_start"] = []extension.HandlerFn{func(args ...any) (any, error) {
			ui, err := extension.FromContext(args[1].(context.Context)).UI()
			if err != nil {
				return nil, err
			}
			err = ui.AddAutocompleteProvider(func(_ context.Context, current *extension.AutocompleteProvider) (*extension.AutocompleteProvider, error) {
				wrapped := *current
				wrapped.GetSuggestions = func(ctx context.Context, lines []string, line, col int, force bool) (*extension.AutocompleteSuggestions, error) {
					result, err := current.GetSuggestions(ctx, lines, line, col, force)
					if err != nil {
						return nil, err
					}
					if result == nil {
						result = &extension.AutocompleteSuggestions{Prefix: lines[line]}
					}
					result.Items = append(result.Items, extension.AutocompleteItem{Value: "focus-added", Label: "focus-added"})
					return result, nil
				}
				return &wrapped, nil
			})
			if err != nil {
				return nil, err
			}
			waitCtx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			report.Popup = mode.EditorPopupAfterTyping(waitCtx, "/fo")
			return nil, nil
		}}
	}
	runner := inproc.NewRunner([]extension.Extension{{Path: "/startup-bind", Handlers: handlers}}, cwd)
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
				if os.Getenv(startupBindHandlerEnv) != "2" {
					mode.HoldStartupInput("k")
				}
				installed, offers := mode.EditorAutocompleteOffers("/fo")
				report.BeforeBind = &startupBindProvider{Installed: installed, Offers: offers}
			case "post-emit-session-start":
				unread := mode.HeldStartupInputUnread()
				report.UnreadAfterBind = &unread
				installed, offers := mode.EditorAutocompleteOffers("/fo")
				report.AfterBind = &startupBindProvider{Installed: installed, Offers: offers}
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
