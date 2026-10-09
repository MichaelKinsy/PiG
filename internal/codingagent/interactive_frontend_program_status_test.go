//go:build linux || darwin

package codingagent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

const (
	frontendStatusChildEnv = "PIG_TEST_FRONTEND_STATUS_CHILD"
	frontendStatusHookEnv  = "PIG_TEST_FRONTEND_STATUS_HOOK"
)

// statusFrontendSession is a fake session with the program status hook. It
// records each report among its calls as "status <report>", quoted, in the
// order Suspend, Resume and Apply arrive.
type statusFrontendSession struct {
	*fakeFrontendSession
}

func (s statusFrontendSession) ProgramStatus(report string) {
	s.record(fmt.Sprintf("status %q", report))
}

// statusFrontend opens a statusFrontendSession.
type statusFrontend struct{ session statusFrontendSession }

func (f *statusFrontend) Open(frontend.Env) (frontend.Session, error) { return f.session, nil }

// TestFrontendSessionShowsTheProgramStatus drives the real path in a child
// process whose stdin and stdout are a pty, as Run does: the status is held
// for the frontend before raw mode starts, and the session draws on the
// process terminal. A session with the hook hears every status the reporter
// sends, in order: idle, a working run that fails, a working run that
// succeeds, an extension dialog and a login that wait for the user, the
// clear before a job-control stop with the latest status after it, and the
// clear before Close. With or without the hook, and with PI_PROGRAM_STATUS=1
// forcing support, no OSC 7501 byte reaches the terminal: neither the
// support query nor a report.
func TestFrontendSessionShowsTheProgramStatus(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hook     bool
		override string
	}{
		{"hook", true, ""},
		{"hook, PI_PROGRAM_STATUS=1", true, "1"},
		{"no hook", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, slave := openTestPTY(t)
			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestFrontendProgramStatusChild$", "-test.count=1", "-test.v")
			cmd.Env = append(os.Environ(), frontendStatusChildEnv+"=1", frontendStatusHookEnv+"="+fmt.Sprint(tc.hook), "PI_PROGRAM_STATUS="+tc.override, "TERM=xterm-256color")
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
			if waitErr != nil || !bytes.Contains(captured.Bytes(), []byte("--- PASS: TestFrontendProgramStatusChild")) {
				t.Fatalf("child: %v\n%s", waitErr, captured.Bytes())
			}
			if i := bytes.Index(captured.Bytes(), []byte("\x1b]7501")); i >= 0 {
				t.Fatalf("OSC 7501 reached the terminal at byte %d: %q", i, captured.Bytes()[i:min(len(captured.Bytes()), i+40)])
			}
		})
	}
}

// TestFrontendProgramStatusChild is the child half of
// TestFrontendSessionShowsTheProgramStatus.
func TestFrontendProgramStatusChild(t *testing.T) {
	if os.Getenv(frontendStatusChildEnv) == "" {
		t.Skip("runs only as the pty child of TestFrontendSessionShowsTheProgramStatus")
	}
	hook := os.Getenv(frontendStatusHookEnv) == "true"
	session := &fakeFrontendSession{}
	var fe frontend.Frontend = &fakeFrontend{session: session}
	if hook {
		fe = &statusFrontend{session: statusFrontendSession{session}}
	}
	var out bytes.Buffer
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{Frontend: fe, TuiMode: "regular", CWD: t.TempDir()}, &out)
	m.agent = mustNewAgent(agent.AgentOptions{})
	// The session draws on the process terminal, the pty, as in a run.
	m.rendererOut = nil
	m.holdProgramStatusForFrontend()
	restore, drain, err := tui.EnterRawModeWithDrain()
	if err != nil {
		t.Fatal(err)
	}
	m.rawRestore, m.rawDrain = restore, drain
	m.openFrontend()
	if m.surface == nil {
		t.Fatal("no surface renderer")
	}
	m.mountInteractiveTui(true)
	reader := newInteractiveTerminalReader(t.Context(), os.Stdin)
	m.inputReader = reader
	t.Cleanup(func() {
		reader.pause()
		if m.rawRestore != nil {
			m.rawRestore()
		}
	})

	m.programStatusReporter().Report()
	// A run that fails, then one that succeeds.
	m.handleAgentEvent(agent.AgentStartEvent{})
	m.handleAgentEvent(assistantEnd(ai.StopReasonError, "429 rate limited\nretry later"))
	m.handleAgentEvent(agent.AgentSettledEvent{})
	m.handleAgentEvent(agent.AgentStartEvent{})
	m.handleAgentEvent(assistantEnd(ai.StopReasonStop, ""))
	m.handleAgentEvent(agent.AgentSettledEvent{})
	// An extension dialog, then a login, wait for the user.
	ui := &ExtUIContext{m: m}
	if _, err := runExtensionDialogProbe(t, m, func() (string, error) {
		return ui.Select(context.Background(), "Pick one", []string{"a"}, extension.ExtensionUIDialogOptions{})
	}, []string{"\r"}); err != nil {
		t.Fatal(err)
	}
	m.reportLoginBlocked("Anthropic")()
	// A job-control stop and continuation, which leaves raw mode and enters
	// it again.
	ops := m.suspendOperations()
	ops.stop()
	if err := ops.start(); err != nil {
		t.Fatal(err)
	}
	m.closeFrontend()

	var got []string
	for _, call := range session.calls {
		if call != "apply" {
			got = append(got, call)
		}
	}
	if !hook {
		if want := []string{"suspend", "resume"}; !slices.Equal(got, want) {
			t.Fatalf("session calls = %q, want %q", got, want)
		}
	} else {
		report := func(status tui.ProgramStatus) string {
			status.App = AppName
			if status.State == tui.ProgramStateClear {
				status.App = ""
			}
			return fmt.Sprintf("status %q", tui.FormatProgramStatus(status))
		}
		done := report(tui.ProgramStatus{State: tui.ProgramStateDone})
		clear := report(tui.ProgramStatus{State: tui.ProgramStateClear})
		want := []string{
			report(tui.ProgramStatus{State: tui.ProgramStateIdle}),
			report(tui.ProgramStatus{State: tui.ProgramStateWorking}),
			report(tui.ProgramStatus{State: tui.ProgramStateError, Message: "429 rate limited"}),
			report(tui.ProgramStatus{State: tui.ProgramStateWorking}),
			done,
			report(tui.ProgramStatus{State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindQuestion, Message: "Pick one"}),
			done,
			report(tui.ProgramStatus{State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindAuth, Message: "Log in to Anthropic"}),
			done,
			clear, "suspend",
			"resume", done,
			clear,
		}
		if !slices.Equal(got, want) {
			t.Fatalf("session calls\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
	if session.closed != 1 {
		t.Fatalf("closed %d times", session.closed)
	}
	if strings.Contains(out.String(), "\x1b]7501") {
		t.Fatalf("the renderer's output got OSC 7501: %q", out.String())
	}
}

// pig additive (D91): once a session stops drawing mid-run, the status is
// cleared for it before Close and reported again to the terminal that
// replaces it.
func TestFrontendFallbackHandsTheProgramStatusBack(t *testing.T) {
	session := &fakeFrontendSession{}
	m, _ := newFrontendProbe(t, &statusFrontend{session: statusFrontendSession{session}}, "regular")
	process := &programStatusTerminal{}
	m.programStatus = NewProgramStatusReporter(func() tui.Terminal {
		if m.surface != nil {
			return m.tuiInst.Terminal()
		}
		return process
	}, func() string { return "" })
	m.programStatus.HandleEvent(agent.AgentStartEvent{})
	m.leaveFrontend("test")
	var got []string
	for _, call := range session.calls {
		if call != "apply" {
			got = append(got, call)
		}
	}
	working := fmt.Sprintf("status %q", tui.FormatProgramStatus(tui.ProgramStatus{State: tui.ProgramStateWorking, App: AppName}))
	clear := fmt.Sprintf("status %q", tui.FormatProgramStatus(tui.ProgramStatus{State: tui.ProgramStateClear}))
	if want := []string{working, clear}; !slices.Equal(got, want) || session.closed != 1 {
		t.Fatalf("session calls = %q (closed %d), want %q then Close", got, session.closed, want)
	}
	if len(process.reports) != 1 || process.reports[0].State != tui.ProgramStateWorking {
		t.Fatalf("replacing terminal got %+v, want working", process.reports)
	}
}

const frontendStatusReleaseEnv = "PIG_TEST_FRONTEND_STATUS_RELEASE"

// frontendStatusReleaseMark is what the child writes to the pty after raw
// mode starts and before the release under test.
const frontendStatusReleaseMark = "<release>"

// TestProcessTerminalShowsTheProgramStatusOnceNoSessionDraws drives the real
// path in a pty child: raw mode starts with the OSC 7501 support query held
// back for the frontend, and the process terminal writes it to the terminal
// once the frontend declines the run, fails to open, or its session falls
// back mid-run. With PI_PROGRAM_STATUS=1 there is no query, and after a
// fallback the reporter's current status reaches the terminal until the
// renderer stops.
func TestProcessTerminalShowsTheProgramStatusOnceNoSessionDraws(t *testing.T) {
	const (
		query   = "\x1b]7501;?\x1b\\"
		working = "\x1b]7501;state=working:app=" + AppName + "\x1b\\"
		clear   = "\x1b]7501;state=clear\x1b\\"
	)
	for _, tc := range []struct {
		release, override string
		want              []string
	}{
		{"decline", "", []string{query}},
		{"error", "", []string{query}},
		{"fallback", "", []string{query}},
		// Stop clears the status that shows, as ProcessTerminal stop does.
		{"fallback", "1", []string{working, clear}},
	} {
		t.Run(tc.release+"/PI_PROGRAM_STATUS="+tc.override, func(t *testing.T) {
			master, slave := openTestPTY(t)
			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestProcessTerminalProgramStatusReleaseChild$", "-test.count=1", "-test.v")
			cmd.Env = append(os.Environ(), frontendStatusReleaseEnv+"="+tc.release, "PI_PROGRAM_STATUS="+tc.override, "TERM=xterm-256color")
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
			output := captured.String()
			if waitErr != nil || !strings.Contains(output, "--- PASS: TestProcessTerminalProgramStatusReleaseChild") {
				t.Fatalf("child: %v\n%q", waitErr, output)
			}
			before, after, ok := strings.Cut(output, frontendStatusReleaseMark)
			if !ok {
				t.Fatalf("no release mark in %q", output)
			}
			if strings.Contains(before, "\x1b]7501") {
				t.Fatalf("OSC 7501 before the release: %q", before)
			}
			var got []string
			for rest := after; ; {
				i := strings.Index(rest, "\x1b]7501")
				if i < 0 {
					break
				}
				end := strings.Index(rest[i:], "\x1b\\")
				if end < 0 {
					t.Fatalf("unterminated OSC 7501 in %q", rest[i:])
				}
				got = append(got, rest[i:i+end+2])
				rest = rest[i+end+2:]
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("OSC 7501 after the release = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestProcessTerminalProgramStatusReleaseChild is the child half of
// TestProcessTerminalShowsTheProgramStatusOnceNoSessionDraws.
func TestProcessTerminalProgramStatusReleaseChild(t *testing.T) {
	release := os.Getenv(frontendStatusReleaseEnv)
	if release == "" {
		t.Skip("runs only as the pty child of TestProcessTerminalShowsTheProgramStatusOnceNoSessionDraws")
	}
	fe := &fakeFrontend{}
	switch release {
	case "error":
		fe.err = fmt.Errorf("no terminal support")
	case "fallback":
		fe.session = &fakeFrontendSession{}
	}
	var out bytes.Buffer
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{Frontend: fe, TuiMode: "regular", CWD: t.TempDir()}, &out)
	m.agent = mustNewAgent(agent.AgentOptions{})
	// The renderer that replaces a session draws on the process terminal,
	// the pty, as in a run.
	m.rendererOut = nil
	m.holdProgramStatusForFrontend()
	restore, drain, err := tui.EnterRawModeWithDrain()
	if err != nil {
		t.Fatal(err)
	}
	m.rawRestore, m.rawDrain = restore, drain
	t.Cleanup(func() {
		if m.rawRestore != nil {
			m.rawRestore()
		}
	})
	if release == "fallback" {
		m.openFrontend()
		if m.surface == nil {
			t.Fatal("no surface renderer")
		}
		m.mountInteractiveTui(true)
		m.handleAgentEvent(agent.AgentStartEvent{})
	}
	if _, err := os.Stdout.WriteString(frontendStatusReleaseMark); err != nil {
		t.Fatal(err)
	}
	if release == "fallback" {
		m.leaveFrontend("test")
		m.teardownCurrentTui()
		m.stopInteractiveTui()
	} else {
		m.openFrontend()
		if m.surface != nil {
			t.Fatal("a declined run has a surface renderer")
		}
	}
}
