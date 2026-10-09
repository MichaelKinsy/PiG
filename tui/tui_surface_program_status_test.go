package tui

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// reportRecorder is a recording session with the program status hook. It
// logs each report, Suspend and Resume in one order.
type reportRecorder struct {
	*recordingSession
	log []string
}

func newReportRecorder() *reportRecorder {
	return &reportRecorder{recordingSession: newRecordingSession()}
}

func (r *reportRecorder) ProgramStatus(report string) { r.log = append(r.log, report) }
func (r *reportRecorder) Suspend()                    { r.log = append(r.log, "suspend") }
func (r *reportRecorder) Resume()                     { r.log = append(r.log, "resume") }

// The surface's terminal is the session: every report reaches the session's
// hook as Pi encodes it, clear included, with no support query. Suspend and
// Stop clear a status that shows first, and Resume and Start report the
// latest one again, as ProcessTerminal stop and start do (terminal.ts:467-476,
// :299).
func TestSurfaceTerminalReportsProgramStatusToTheSession(t *testing.T) {
	recorder := newReportRecorder()
	surface := NewTuiSurfaceWithSize(recorder, 80, 24, nil)
	terminal := surface.Terminal()
	if surface.Terminal() != terminal {
		t.Fatal("Terminal returns a different value each time")
	}
	if terminal.Rows() != 24 || terminal.Columns() != 80 {
		t.Fatalf("terminal size %dx%d, want the renderer's 80x24", terminal.Columns(), terminal.Rows())
	}
	idle := ProgramStatus{State: ProgramStateIdle, App: "pig"}
	working := ProgramStatus{State: ProgramStateWorking, App: "pig", Message: "Run"}
	blocked := ProgramStatus{State: ProgramStateBlocked, App: "pig", Kind: ProgramStatusKindAuth, Message: "Log in to Anthropic"}
	failed := ProgramStatus{State: ProgramStateError, App: "pig", Message: "boom"}
	done := ProgramStatus{State: ProgramStateDone, App: "pig", Message: "Run"}
	clear := ProgramStatus{State: ProgramStateClear}

	terminal.SetProgramStatus(idle)
	surface.Start()
	for _, status := range []ProgramStatus{working, blocked, failed, done, clear, working} {
		terminal.SetProgramStatus(status)
	}
	surface.Suspend()
	// Reported while suspended: shown on Resume.
	terminal.SetProgramStatus(blocked)
	surface.Resume()
	surface.Stop()
	terminal.SetProgramStatus(done)
	surface.Start()
	// A cleared status is not shown again after a restart.
	terminal.SetProgramStatus(clear)
	surface.Stop()
	surface.Start()

	f := FormatProgramStatus
	want := []string{
		f(idle), f(working), f(blocked), f(failed), f(done), f(clear), f(working),
		f(clear), "suspend",
		"resume", f(blocked),
		f(clear),
		f(done),
		f(clear),
	}
	if !slices.Equal(recorder.log, want) {
		t.Fatalf("session saw\n%q\nwant\n%q", recorder.log, want)
	}
}

// A session without the hook gets no report, and the reports go nowhere
// else: the process terminal under the surface writes no OSC 7501 byte,
// with the support query held back while a session may draw, even when
// PI_PROGRAM_STATUS=1 forces support.
func TestSurfaceTerminalWritesNoProgramStatusToTheProcessTerminal(t *testing.T) {
	for _, override := range []string{"", "1"} {
		for _, hook := range []bool{true, false} {
			t.Run("PI_PROGRAM_STATUS="+override+"/hook="+map[bool]string{true: "yes", false: "no"}[hook], func(t *testing.T) {
				t.Setenv("PI_PROGRAM_STATUS", override)
				preserveKeyboardProtocolState(t)
				var out bytes.Buffer
				saved := processTerminal
				processTerminal = NewProcessTerminalWithOutput(nil, nil, &out)
				t.Cleanup(func() { processTerminal = saved })

				SetProgramStatusElsewhere(true)
				processTerminal.queryAndEnableKittyProtocol()
				recorder := newReportRecorder()
				var surface *TuiSurface
				if hook {
					surface = NewTuiSurface(recorder, nil)
				} else {
					surface = NewTuiSurface(recorder.recordingSession, nil)
				}
				if surface.Terminal().(*surfaceTerminal).Terminal != Terminal(processTerminal) {
					t.Fatal("the surface does not draw on the process terminal")
				}
				surface.Start()
				for _, state := range []ProgramState{ProgramStateIdle, ProgramStateWorking, ProgramStateBlocked, ProgramStateError, ProgramStateDone, ProgramStateClear} {
					surface.Terminal().SetProgramStatus(ProgramStatus{State: state, App: "pig"})
				}
				surface.Suspend()
				surface.Resume()
				surface.Stop()
				processTerminal.Stop()
				if strings.Contains(out.String(), "\x1b]7501") {
					t.Fatalf("process terminal wrote OSC 7501: %q", out.String())
				}
				if got := len(recorder.log); hook && got == 0 || !hook && got != 0 {
					t.Fatalf("hook=%t: session saw %q", hook, recorder.log)
				}
				SetProgramStatusElsewhere(false)
				if out.Len() != 0 && strings.Contains(out.String(), "\x1b]7501") {
					t.Fatalf("stopped process terminal queried: %q", out.String())
				}
			})
		}
	}
}

// Once no session draws, the process terminal shows the program status
// again: in raw mode it sends the support query it held back, with its own
// DA sentinel, and reports once the terminal answers; with PI_PROGRAM_STATUS=1
// it reports the latest status at once. A terminal that never held it back
// writes nothing, so a run without a frontend is unchanged.
func TestProcessTerminalQueriesProgramStatusOnceNoSessionShowsIt(t *testing.T) {
	const (
		working = "\x1b]7501;state=working:app=pig\x1b\\"
		reply   = "\x1b]7501;?\x1b\\"
		da      = "\x1b[?62;4;52c"
	)
	run := func(t *testing.T, override string, fn func(*testing.T, *terminalNegotiationHarness)) {
		t.Setenv("PI_PROGRAM_STATUS", override)
		synctest.Test(t, func(t *testing.T) { fn(t, newTerminalNegotiationHarness(t)) })
	}
	t.Run("query", func(t *testing.T) {
		run(t, "", func(t *testing.T, h *terminalNegotiationHarness) {
			h.terminal.setProgramStatusElsewhere(true)
			h.terminal.Stop()
			h.terminal.queryAndEnableKittyProtocol()
			if last := h.writes[len(h.writes)-1]; last != "\x1b[>7u\x1b[?u\x1b[c" {
				t.Fatalf("raw mode under a session wrote %q", last)
			}
			Terminal(h.terminal).SetProgramStatus(ProgramStatus{State: ProgramStateWorking, App: "pig"})
			h.send(reply)
			h.writeCount(t, working, 0)
			h.terminal.setProgramStatusElsewhere(false)
			if last := h.writes[len(h.writes)-1]; last != "\x1b]7501;?\x1b\\\x1b[c" {
				t.Fatalf("held-back query = %q", last)
			}
			h.send(reply)
			h.writeCount(t, working, 1)
			for range 3 {
				h.send(da)
			}
			h.noInput(t)
		})
	})
	t.Run("PI_PROGRAM_STATUS=1", func(t *testing.T) {
		run(t, "1", func(t *testing.T, h *terminalNegotiationHarness) {
			h.terminal.setProgramStatusElsewhere(true)
			Terminal(h.terminal).SetProgramStatus(ProgramStatus{State: ProgramStateWorking, App: "pig"})
			h.writeCount(t, working, 0)
			h.terminal.setProgramStatusElsewhere(false)
			if last := h.writes[len(h.writes)-1]; last != working {
				t.Fatalf("last write = %q, want the latest status", last)
			}
		})
	})
	t.Run("never held back", func(t *testing.T) {
		run(t, "", func(t *testing.T, h *terminalNegotiationHarness) {
			before := len(h.writes)
			h.terminal.setProgramStatusElsewhere(false)
			if len(h.writes) != before {
				t.Fatalf("wrote %q", h.writes[before:])
			}
		})
	})
}

// The editor asks its TUI for the terminal while it renders (editor.ts:536
// reads this.tui.terminal.rows), and the surface renders it while it holds
// its render lock. The first frame must not wait for Terminal, even when
// nothing asked for the terminal before Start.
func TestSurfaceRendersAnEditorBeforeAnythingAsksForItsTerminal(t *testing.T) {
	recorder := newRecordingSession()
	surface := NewTuiSurface(recorder, nil)
	editor := NewEditorWithTUI(surface, EditorTheme{}, EditorOptions{})
	editor.SetText("hello\nworld")
	surface.SetHooks(SurfaceHooks{Editor: func() *Editor { return editor }})
	surface.SetLayout(NewContainer(), editor)
	done := make(chan struct{})
	go func() {
		defer close(done)
		surface.Start()
	}()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the first frame waits for the surface's terminal")
	}
	if len(recorder.frames) == 0 {
		t.Fatal("Start drew no frame")
	}
}
