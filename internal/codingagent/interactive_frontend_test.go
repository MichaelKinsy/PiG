package codingagent

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

type fakeFrontend struct {
	session *fakeFrontendSession
	err     error
	env     frontend.Env
}

func (f *fakeFrontend) Open(env frontend.Env) (frontend.Session, error) {
	f.env = env
	if f.err != nil || f.session == nil {
		return nil, f.err
	}
	return f.session, nil
}

type fakeFrontendSession struct {
	frames []frontend.Frame
	inputs []string
	closed int
}

func (*fakeFrontendSession) InputReady()               {}
func (*fakeFrontendSession) Columns() (main, dock int) { return 0, 0 }

func (s *fakeFrontendSession) Apply(frame frontend.Frame) error {
	s.frames = append(s.frames, frame)
	return nil
}

func (s *fakeFrontendSession) HandleInput(data string) bool {
	if !strings.HasPrefix(data, "\x1b_tsp;") {
		return false
	}
	s.inputs = append(s.inputs, data)
	return true
}

func (s *fakeFrontendSession) Close() error {
	s.closed++
	return nil
}

func (s *fakeFrontendSession) toolOps() []frontend.Op {
	var ops []frontend.Op
	for _, frame := range s.frames {
		for _, op := range frame.Ops {
			if _, ok := op.Node.(frontend.ToolCard); ok {
				ops = append(ops, op)
			}
		}
	}
	return ops
}

func newFrontendProbe(t *testing.T, fe frontend.Frontend, tuiMode string) (*InteractiveMode, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	m := newUnmountedSwitchTuiProbe(t, InteractiveOptions{Frontend: fe, TuiMode: tuiMode}, &out)
	m.openFrontend()
	m.mountInteractiveTui(true)
	t.Cleanup(func() { m.teardownCurrentTui(); m.stopInteractiveTui(); m.backgroundTasks.Wait() })
	return m, &out
}

func TestFrontendSessionDrawsTheRunInsteadOfTheTerminal(t *testing.T) {
	session := &fakeFrontendSession{}
	m, out := newFrontendProbe(t, &fakeFrontend{session: session}, "fullscreen")
	if m.surface == nil || m.tuiInst != tui.Renderer(m.surface) || m.altScreen != nil {
		t.Fatalf("renderer = %T, want the surface renderer", m.tuiInst)
	}
	card := tui.NewToolExecutionComponent("bash", "expr 20 + 22")
	m.chatContainer.Add(card)
	m.tuiInst.Render()
	card.MarkExecutionStarted()
	card.SetResult("42", false, 0)
	m.tuiInst.Render()

	ops := session.toolOps()
	if len(ops) != 2 || ops[0].Kind != frontend.Insert || ops[1].Kind != frontend.Update || ops[0].ID != ops[1].ID {
		t.Fatalf("tool ops = %#v", ops)
	}
	if got := ops[1].Node.(frontend.ToolCard); got.Status != frontend.ToolDone || got.Output != "42" {
		t.Fatalf("finished tool = %#v", got)
	}
	var dock bool
	for _, frame := range session.frames {
		for _, op := range frame.Ops {
			dock = dock || op.Region == frontend.RegionDock
		}
	}
	if !dock {
		t.Fatal("the editor dock was never drawn")
	}
	if out.Len() != 0 {
		t.Fatalf("terminal output while the frontend draws: %q", out.String())
	}
}

func TestFrontendThatDeclinesLeavesTheANSIRenderer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fe      *fakeFrontend
		warning string
	}{
		{name: "declined", fe: &fakeFrontend{}},
		{name: "failed", fe: &fakeFrontend{err: errors.New("no tty")}, warning: "Native rendering is unavailable: no tty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, out := newFrontendProbe(t, tc.fe, "regular")
			if m.surface != nil {
				t.Fatal("a frontend without a session installed the surface renderer")
			}
			if _, ok := m.tuiInst.(*tui.TUI); !ok {
				t.Fatalf("renderer = %T", m.tuiInst)
			}
			if m.frontendStartupWarning != tc.warning {
				t.Fatalf("warning = %q, want %q", m.frontendStartupWarning, tc.warning)
			}
			if out.Len() == 0 {
				t.Fatal("the ANSI renderer painted nothing")
			}
		})
	}
}

func TestFrontendFallbackRepaintsWithTheConfiguredRenderer(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			session := &fakeFrontendSession{}
			fe := &fakeFrontend{session: session}
			m, out := newFrontendProbe(t, fe, mode)
			m.chatContainer.Add(tui.NewText("before fallback"))
			m.tuiInst.Render()
			if out.Len() != 0 {
				t.Fatalf("terminal output before fallback: %q", out.String())
			}

			fe.env.Fallback("no reply")
			fe.env.Fallback("second call")
			m.backgroundTasks.Wait()
			for len(m.uiTaskCh) > 0 {
				(<-m.uiTaskCh)()
			}

			if m.surface != nil || session.closed != 1 {
				t.Fatalf("surface = %v, closed = %d", m.surface, session.closed)
			}
			if (m.altScreen != nil) != (mode == "fullscreen") {
				t.Fatalf("renderer after fallback = %T, want %s", m.tuiInst, mode)
			}
			if !strings.Contains(stripANSITest(out.String()), "before fallback") {
				t.Fatalf("fallback did not repaint the transcript: %q", out.String())
			}
			if texts := chatTexts(m); !containsSubstring(texts, "Native rendering stopped: no reply") {
				t.Fatalf("chat = %q", texts)
			}
			m.stopInteractiveTui()
			if session.closed != 1 {
				t.Fatalf("session closed %d times", session.closed)
			}
		})
	}
}

func TestFrontendKeepsTheScreenAgainstATuiModeSwitch(t *testing.T) {
	m, _ := newFrontendProbe(t, &fakeFrontend{session: &fakeFrontendSession{}}, "regular")
	if m.switchTuiMode("fullscreen", false, true) {
		t.Fatal("switch replaced the frontend renderer")
	}
	if m.surface == nil || m.tuiInst != tui.Renderer(m.surface) {
		t.Fatalf("renderer = %T", m.tuiInst)
	}
}

// Tern answers the hello while PiG is still starting, when input goes to the
// startup editor, and sends events once the input loop runs. Neither reaches
// the editor.
func TestFrontendInputNeverReachesTheEditor(t *testing.T) {
	for _, path := range []string{"startup", "loop"} {
		t.Run(path, func(t *testing.T) {
			session := &fakeFrontendSession{}
			m, _ := newFrontendProbe(t, &fakeFrontend{session: session}, "regular")
			send := func(data string) {
				if path == "startup" {
					m.handleStartupInput(inputChunk{data: []byte(data)}, false)
					return
				}
				if err := m.dispatchInputChunk(t.Context(), data, nil); err != nil {
					t.Fatal(err)
				}
			}
			reply := "\x1b_tsp;r;{\"r\":\"hello\",\"v\":1}\x1b\\"
			send(reply)
			send("a")
			if len(session.inputs) != 1 || session.inputs[0] != reply {
				t.Fatalf("session inputs = %q", session.inputs)
			}
			if got := m.editor.Text(); got != "a" {
				t.Fatalf("editor = %q", got)
			}
		})
	}
}

func TestFrontendSessionClosesOnceAtTeardown(t *testing.T) {
	session := &fakeFrontendSession{}
	m, _ := newFrontendProbe(t, &fakeFrontend{session: session}, "regular")
	m.stopInteractiveTui()
	m.stopInteractiveTui()
	if session.closed != 1 {
		t.Fatalf("session closed %d times", session.closed)
	}
}

func containsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
