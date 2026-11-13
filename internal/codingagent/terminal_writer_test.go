package codingagent

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's interactive mode writes the terminal title and the progress indicator on the terminal its renderer draws on, and nowhere else:
// every write is `this.ui.terminal.setTitle` or `this.ui.terminal.setProgress` (interactive-mode.ts:1173-1179 updateTerminalTitle, which
// resetExtensionUI calls at :2613 when a /reload begins; :3552, :3780, :3802, :3822 and :7286 for the progress). The tests below hold a
// mode whose renderer's terminal is not the process terminal and read what that terminal received; the last one reads the stdout of a
// process that ran them.

// The session's title goes to the renderer's terminal, whatever terminal that is.
func TestSessionTitleIsWrittenOnTheRenderersTerminal(t *testing.T) {
	var out strings.Builder
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work/project"})
	m.tuiInst = tui.NewTuiMainScreen(tui.NewProcessTerminalWithOutput(nil, nil, &out), nil, "")
	m.updateTerminalTitle()
	if want := "\x1b]0;" + tui.BuildTerminalTitle("", "/work/project") + "\x07"; out.String() != want {
		t.Fatalf("the renderer's terminal received %q, want the session's title %q", out.String(), want)
	}
}

// sizedTerminal is a terminal that writes to a writer and reports a fixed size, since it has no terminal device to ask.
type sizedTerminal struct{ *tui.ProcessTerminal }

func (sizedTerminal) Columns() int { return 100 }
func (sizedTerminal) Rows() int    { return 30 }

// setTerminalTitle and setTerminalProgress are the mode's only writers of the title and the progress indicator. They write on the
// renderer's terminal, and a mode that has no renderer yet writes nothing.
func TestTitleAndProgressWritersUseTheRenderersTerminal(t *testing.T) {
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work/project"})
	m.setTerminalTitle("before the renderer")
	m.setTerminalProgress(true)
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	terminal := recordTerminalCalls(t, m)
	m.setTerminalTitle("a title")
	m.setTerminalTitle("")
	m.setTerminalProgress(true)
	m.setTerminalProgress(false)
	if titles, want := terminal.takeTitles(), []string{"a title", ""}; !slices.Equal(titles, want) {
		t.Errorf("the renderer's terminal received the titles %q, want %q", titles, want)
	}
	if progress, want := terminal.takeProgress(), []bool{true, false}; !slices.Equal(progress, want) {
		t.Errorf("the renderer's terminal received the progress calls %v, want %v", progress, want)
	}
}

// terminalOptionMode is a mode whose renderer draws on InteractiveModeOptions.Terminal (interactive-mode.ts:447 `terminal?: Terminal`,
// :626), here a terminal that is not the process terminal and writes to the returned recorder. Terminal progress is on.
func terminalOptionMode(t *testing.T) (*InteractiveMode, *terminalRecorder) {
	t.Helper()
	out := &terminalRecorder{}
	m := statusBorderMode(t, false)
	m.opts.Terminal = sizedTerminal{tui.NewProcessTerminalWithOutput(nil, nil, out)}
	m.opts.SessionHandle = &recordingCompactHandle{inner: NewSession("terminal-option", m.opts.CWD)}
	on := true
	m.opts.Settings.ShowTerminalProgress = &on
	m.runCtx = t.Context()
	// The mode builds its first renderer on the option; a later one draws on the previous renderer's terminal.
	m.tuiInst = nil
	handle := m.createInteractiveTui(t.Context())
	t.Cleanup(handle.cleanup)
	if m.tuiInst.Terminal() != m.opts.Terminal {
		t.Fatalf("the renderer draws on %T, want the terminal option", m.tuiInst.Terminal())
	}
	return m, out
}

// Every title the mode writes arrives on the terminal option: the session's title (startup, a /reload, a session replacement), the
// name a session_info_changed event carries, the name /name sets and the name an extension sets.
func TestTitleIsWrittenOnTheTerminalOption(t *testing.T) {
	for _, site := range []struct {
		name  string
		write func(t *testing.T, m *InteractiveMode)
		// named is whether the title carries the session name "named".
		named bool
	}{
		{"the session's title", func(_ *testing.T, m *InteractiveMode) { m.updateTerminalTitle() }, false},
		{"session_info_changed", func(_ *testing.T, m *InteractiveMode) {
			m.handleAgentEvent(agent.SessionInfoChangedEvent{Name: "named"})
		}, true},
		{"/name", func(t *testing.T, m *InteractiveMode) { m.buildSlashContext(t.Context()).OnNameChange("named") }, true},
		{"an extension's setSessionName", func(t *testing.T, m *InteractiveMode) {
			if err := m.setExtensionSessionName("named"); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"an extension's setTitle", func(_ *testing.T, m *InteractiveMode) {
			(&ExtUIContext{m: m}).SetTitle(tui.BuildTerminalTitle("named", m.opts.CWD))
		}, true},
	} {
		t.Run(site.name, func(t *testing.T) {
			m, out := terminalOptionMode(t)
			site.write(t, m)
			name := ""
			if site.named {
				name = "named"
			}
			titles := terminalTitlesIn(out.take())
			if want := tui.BuildTerminalTitle(name, m.opts.CWD); len(titles) == 0 || titles[len(titles)-1] != want {
				t.Fatalf("the terminal option received the titles %q, want %q last", titles, want)
			}
		})
	}
}

// The progress indicator of a run arrives on the terminal option: on at turn_start, off at agent_end.
func TestProgressIsWrittenOnTheTerminalOption(t *testing.T) {
	var reference strings.Builder
	terminal := tui.NewProcessTerminalWithOutput(nil, nil, &reference)
	terminal.SetProgress(true)
	terminal.SetProgress(false)
	sequences := terminalProgressIn(reference.String())
	if len(sequences) != 2 {
		t.Fatalf("a terminal wrote %q for progress on and off, want two OSC 9;4 sequences", reference.String())
	}
	on, off := sequences[0], sequences[1]

	m, out := terminalOptionMode(t)
	m.handleAgentEvent(agent.TurnStartEvent{})
	if got := terminalProgressIn(out.take()); len(got) == 0 || slices.ContainsFunc(got, func(sequence string) bool { return sequence != on }) {
		t.Fatalf("turn_start wrote the progress sequences %q on the terminal option, want %q", got, on)
	}
	m.handleAgentEvent(agent.AgentEndEvent{})
	// The terminal repeats the on sequence while the run lasts; the off sequence ends it.
	if got := terminalProgressIn(out.take()); len(got) == 0 || got[len(got)-1] != off {
		t.Fatalf("agent_end wrote the progress sequences %q on the terminal option, want %q last", got, off)
	}
}

// On a frontend's surface (D91) the title and the progress go to the surface's terminal. This test only drives the writes; the
// process's stdout, which must not receive them, is read by TestTitleAndProgressStayOffTheProcessStdout.
func TestTitleAndProgressOnAFrontendSurface(t *testing.T) {
	m, _ := newFrontendProbe(t, &fakeFrontend{session: &fakeFrontendSession{}}, "regular")
	if m.surface == nil || m.tuiInst != tui.TUI(m.surface) {
		t.Fatalf("renderer = %T, want the surface renderer", m.tuiInst)
	}
	m.agent = mustNewAgent(agent.AgentOptions{})
	on := true
	m.opts.Settings.ShowTerminalProgress = &on
	m.updateTerminalTitle()
	(&ExtUIContext{m: m}).SetTitle("a title of an extension")
	m.handleAgentEvent(agent.TurnStartEvent{})
	m.handleAgentEvent(agent.AgentEndEvent{})
}

// The stdout of a process that held such modes, and of one that reloaded a mode on a test harness (the probes of the parity scenario
// session/28-session-reload-ui, whose lines are read from that stdout), carries no escape sequence: neither OSC 0 nor OSC 9;4.
func TestTitleAndProgressStayOffTheProcessStdout(t *testing.T) {
	const child = "^(TestSessionTitleIsWrittenOnTheRenderersTerminal|TestTitleIsWrittenOnTheTerminalOption|TestProgressIsWrittenOnTheTerminalOption|" +
		"TestTitleAndProgressWritersUseTheRenderersTerminal|TestTitleAndProgressOnAFrontendSurface|TestSessionStartNotifyOriginalReload.*|TestReloadWritesTheSessionTitleOnTheRenderersTerminal)$"
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), executable, "-test.run", child, "-test.count=1")
	// The scenario's probe lines, which its script reads from the start of a line.
	cmd.Env = append(os.Environ(), "PIG_RUNTIME_ORIGINAL_PROBE=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if escapes := terminalEscapesIn(stdout.String()); len(escapes) != 0 {
		t.Errorf("the process's stdout received %d escape sequences, want none: %q", len(escapes), escapes)
	}
	if runErr != nil {
		t.Fatalf("the child tests failed: %v\nstdout: %q\nstderr: %q", runErr, stdout.String(), stderr.String())
	}
	probes := 0
	for line := range strings.Lines(stdout.String()) {
		if strings.HasPrefix(line, `RUNTIME_ORIGINAL ["notify",`) {
			probes++
		}
	}
	if probes != 3 {
		t.Fatalf("%d lines of the child's stdout start with the reload probe record, want 3: %q", probes, stdout.String())
	}
}

// terminalEscapesIn lists the OSC 0 and OSC 9;4 sequences in written, or the start of the first other escape sequence.
func terminalEscapesIn(written string) []string {
	escapes := append(terminalTitleSequence.FindAllString(written, -1), terminalProgressIn(written)...)
	if len(escapes) == 0 && strings.ContainsRune(written, '\x1b') {
		at := strings.IndexRune(written, '\x1b')
		escapes = append(escapes, written[at:min(len(written), at+40)])
	}
	return escapes
}
