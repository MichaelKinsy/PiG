package codingagent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

type fakeFrontend struct {
	session *fakeFrontendSession
	// screen, if set, is the session Open returns, around session.
	screen *fakeScreenSession
	err    error
	env    frontend.Env
}

func (f *fakeFrontend) Open(env frontend.Env) (frontend.Session, error) {
	f.env = env
	if f.err != nil || f.session == nil {
		return nil, f.err
	}
	if f.screen != nil {
		return f.screen, nil
	}
	return f.session, nil
}

// fakeScreenSession is a fake session that shows fullscreen overlays.
type fakeScreenSession struct {
	*fakeFrontendSession
	screen frontend.Screen
	ok     bool
}

func (s *fakeScreenSession) Screen() (frontend.Screen, bool) { return s.screen, s.ok }

type fakeFrontendSession struct {
	frames []frontend.Frame
	inputs []string
	closed int
	// onInput, if set, runs for each sequence the session takes.
	onInput func(data string)
	// calls records Suspend, Resume and each Apply as "apply", in order,
	// and onCall, if set, runs after each.
	calls  []string
	onCall func(call string)
}

func (*fakeFrontendSession) InputReady()               {}
func (*fakeFrontendSession) Columns() (main, dock int) { return 0, 0 }

func (s *fakeFrontendSession) record(call string) {
	s.calls = append(s.calls, call)
	if s.onCall != nil {
		s.onCall(call)
	}
}

func (s *fakeFrontendSession) Apply(frame frontend.Frame) error {
	s.frames = append(s.frames, frame)
	s.record("apply")
	return nil
}

func (s *fakeFrontendSession) Suspend() { s.record("suspend") }
func (s *fakeFrontendSession) Resume()  { s.record("resume") }

func (s *fakeFrontendSession) HandleInput(data string) bool {
	if !strings.HasPrefix(data, "\x1b_tsp;") {
		return false
	}
	s.inputs = append(s.inputs, data)
	if s.onInput != nil {
		s.onInput(data)
	}
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
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{Frontend: fe, TuiMode: tuiMode}, &out)
	m.openFrontend()
	m.mountInteractiveTui(true)
	t.Cleanup(func() { m.teardownCurrentTui(); m.stopInteractiveTui(); m.backgroundTasks.Wait() })
	return m, &out
}

func TestFrontendSessionDrawsTheRunInsteadOfTheTerminal(t *testing.T) {
	session := &fakeFrontendSession{}
	m, out := newFrontendProbe(t, &fakeFrontend{session: session}, "fullscreen")
	if m.surface == nil || m.tuiInst != tui.TUI(m.surface) || m.altScreen != nil {
		t.Fatalf("renderer = %T, want the surface renderer", m.tuiInst)
	}
	card := tui.NewToolExecutionComponent("bash", "expr 20 + 22", nil, tui.ToolExecutionOptions{}, nil, nil, "")
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

// A tool result's images reach the frontend's tool card under the image
// settings Pi's terminal renderer follows: shown, each at imageWidthCells;
// with showImages off, none. A result without images carries none.
func TestFrontendToolCardCarriesTheResultsImages(t *testing.T) {
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	data := base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	for _, show := range []bool{true, false} {
		t.Run(fmt.Sprintf("showImages=%v", show), func(t *testing.T) {
			session := &fakeFrontendSession{}
			var out bytes.Buffer
			manager := NewInMemorySettingsManager(Settings{ShowImages: &show, ImageWidthCells: 24})
			m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{Frontend: &fakeFrontend{session: session}, TuiMode: "fullscreen", SettingsManager: manager}, &out)
			m.openFrontend()
			m.mountInteractiveTui(true)
			t.Cleanup(func() { m.teardownCurrentTui(); m.stopInteractiveTui(); m.backgroundTasks.Wait() })
			card := func(id string, content ...ai.ToolResultMessageContent) frontend.ToolCard {
				m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: id, ToolName: "read", Args: json.RawMessage(`{"path":"dot.png"}`)})
				m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: id, ToolName: "read", Result: agent.AgentToolResult{Content: content}})
				m.tuiInst.Render()
				ops := session.toolOps()
				return ops[len(ops)-1].Node.(frontend.ToolCard)
			}

			got := card("img", ai.TextContent{Text: "Read image file [image/png]"}, ai.ImageContent{Data: data, MimeType: "image/png"})
			if got.Status != frontend.ToolDone {
				t.Fatalf("card = %#v", got)
			}
			if !show {
				if got.Images != nil {
					t.Fatalf("images with showImages off = %#v", got.Images)
				}
				return
			}
			if len(got.Images) != 1 || got.Images[0].MimeType != "image/png" || !bytes.Equal(got.Images[0].Data, pngBytes.Bytes()) || got.Images[0].MaxWidthCells != 24 {
				t.Fatalf("images = %#v", got.Images)
			}
			if text := card("text", ai.TextContent{Text: "notes"}); text.Images != nil {
				t.Fatalf("a result without images has %#v", text.Images)
			}
		})
	}
}

// A user message's images reach the frontend on its MarkdownText, live and
// when a session's messages are drawn again; a message without images has
// none.
func TestFrontendUserMessageCarriesItsImages(t *testing.T) {
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	data := base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	session := &fakeFrontendSession{}
	m, _ := newFrontendProbe(t, &fakeFrontend{session: session}, "fullscreen")
	userText := func() []frontend.MarkdownText {
		var out []frontend.MarkdownText
		for _, frame := range session.frames {
			for _, op := range frame.Ops {
				if md, ok := op.Node.(frontend.MarkdownText); ok && md.Role == frontend.RoleUser {
					out = append(out, md)
				}
			}
		}
		return out
	}
	check := func(what string, md frontend.MarkdownText) {
		t.Helper()
		if md.Text != "Look" || len(md.Images) != 1 || md.Images[0].MimeType != "image/png" || !bytes.Equal(md.Images[0].Data, pngBytes.Bytes()) {
			t.Fatalf("%s: %#v", what, md)
		}
	}

	m.handleAgentEvent(agent.MessageStartEvent{Message: agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{
		ai.TextContent{Text: "Look"}, ai.ImageContent{Data: data, MimeType: "image/png"},
	}}}})
	m.tuiInst.Render()
	got := userText()
	if len(got) != 1 {
		t.Fatalf("user nodes = %#v", got)
	}
	check("live", got[0])

	session.frames = nil
	entry := contextFixtureEntry(t, "u", "", "message", map[string]any{"message": map[string]any{"role": "user", "timestamp": 1, "content": []any{
		map[string]any{"type": "text", "text": "Look"}, map[string]any{"type": "image", "data": data, "mimeType": "image/png"},
	}}})
	plain := contextFixtureEntry(t, "v", "u", "message", map[string]any{"message": map[string]any{"role": "user", "timestamp": 2, "content": "Plain"}})
	m.chatContainer.Clear()
	m.renderSessionEntryList([]SessionEntry{entry, plain}, false)
	m.tuiInst.Render()
	got = userText()
	if len(got) != 2 || got[1].Text != "Plain" || got[1].Images != nil {
		t.Fatalf("rebuilt user nodes = %#v", got)
	}
	check("rebuilt", got[0])
}

// The surface marks the assistant message the agent is generating as
// streaming, and no other message.
func TestFrontendMarksOnlyTheGeneratingMessageAsStreaming(t *testing.T) {
	session := &fakeFrontendSession{}
	m, _ := newFrontendProbe(t, &fakeFrontend{session: session}, "fullscreen")
	done, live := m.newAssistantMessageBlock(), m.newAssistantMessageBlock()
	done.SetTextDelta("earlier")
	live.SetTextDelta("now")
	m.chatContainer.Add(done)
	m.chatContainer.Add(live)
	m.evCurrentBlock = live
	m.turnActive.Store(true)
	m.tuiInst.Render()
	m.turnActive.Store(false)
	m.tuiInst.Render()
	var got []frontend.MarkdownText
	for _, frame := range session.frames {
		for _, op := range frame.Ops {
			if md, ok := op.Node.(frontend.MarkdownText); ok {
				got = append(got, md)
			}
		}
	}
	want := []frontend.MarkdownText{
		{Role: frontend.RoleAssistant, Text: "earlier"},
		{Role: frontend.RoleAssistant, Text: "now", Streaming: true},
		{Role: frontend.RoleAssistant, Text: "now"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("markdown nodes = %#v", got)
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
			if _, ok := m.tuiInst.(*tui.TuiMainScreen); !ok {
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

// pig additive (D107): extensions learn from StatePayload.frontend that a
// frontend draws, through a state_update when the session opens and when it
// falls back. A frontend that declines the run never sets it.
func TestFrontendSessionIsReportedToExtensions(t *testing.T) {
	probe := func(t *testing.T, fe *fakeFrontend) (*InteractiveMode, *subprocess.UIBridge, chan bool) {
		var out bytes.Buffer
		m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{Frontend: fe, TuiMode: "regular"}, &out)
		bridge := subprocess.NewUIBridge(func() {})
		pushed := make(chan bool, 4)
		bridge.OnStateChanged = func() { pushed <- bridge.Snapshot(nil, 0, false).Frontend }
		m.opts.SubprocessUIBridge = bridge
		m.openFrontend()
		m.mountInteractiveTui(true)
		t.Cleanup(func() { m.teardownCurrentTui(); m.stopInteractiveTui(); m.backgroundTasks.Wait() })
		return m, bridge, pushed
	}
	next := func(t *testing.T, pushed chan bool) bool {
		t.Helper()
		select {
		case got := <-pushed:
			return got
		case <-time.After(5 * time.Second):
			t.Fatal("no state_update was pushed")
			return false
		}
	}
	t.Run("open and fall back", func(t *testing.T) {
		fe := &fakeFrontend{session: &fakeFrontendSession{}}
		m, bridge, pushed := probe(t, fe)
		if !next(t, pushed) || !bridge.Snapshot(nil, 0, false).Frontend {
			t.Fatal("an open frontend session was not reported as frontend=true")
		}
		fe.env.Fallback("no reply")
		m.backgroundTasks.Wait()
		for len(m.uiTaskCh) > 0 {
			(<-m.uiTaskCh)()
		}
		if next(t, pushed) || bridge.Snapshot(nil, 0, false).Frontend {
			t.Fatal("the fallback to the terminal renderer did not report frontend=false")
		}
	})
	t.Run("declined", func(t *testing.T) {
		_, bridge, pushed := probe(t, &fakeFrontend{})
		if bridge.Snapshot(nil, 0, false).Frontend {
			t.Fatal("a declined frontend set frontend=true")
		}
		select {
		case got := <-pushed:
			t.Fatalf("a declined frontend pushed state (frontend=%v)", got)
		case <-time.After(50 * time.Millisecond):
		}
	})
}

func TestFrontendKeepsTheScreenAgainstATuiModeSwitch(t *testing.T) {
	m, _ := newFrontendProbe(t, &fakeFrontend{session: &fakeFrontendSession{}}, "regular")
	if m.switchTuiMode("fullscreen", false, true) {
		t.Fatal("switch replaced the frontend renderer")
	}
	if m.surface == nil || m.tuiInst != tui.TUI(m.surface) {
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

// The first frame carries the active theme, and a theme switch reaches the
// session as the next frame, with the new palette: an extension's setTheme
// and a /settings or /theme preview. A frame without a switch carries none.
func TestFrontendFramesCarryTheThemeAcrossSwitches(t *testing.T) {
	previous := tui.ActiveTheme().Name
	t.Cleanup(func() { tui.SetThemeByName(previous) })
	tui.SetTheme("dark")
	session := &fakeFrontendSession{}
	m, _ := newFrontendProbe(t, &fakeFrontend{session: session}, "regular")
	themes := func(from int) []string {
		var names []string
		for _, frame := range session.frames[from:] {
			name := "-"
			if frame.Theme != nil {
				name = fmt.Sprintf("%s dark=%t accent=%s", frame.Theme.Name, frame.Theme.Dark, frame.Theme.Colors["accent"])
			}
			names = append(names, name)
		}
		return names
	}
	accent := func(name string) string {
		return tui.ColorToHex(tui.ActiveThemeRegistry().Get(name).Colors()["accent"])
	}
	if got := themes(0)[0]; got != "dark dark=true accent="+accent("dark") {
		t.Fatalf("first frame theme = %s", got)
	}
	step := func(name string, run func(), want string) {
		t.Helper()
		frames := len(session.frames)
		run()
		m.tuiInst.Render()
		if got := themes(frames); !slices.Equal(got, []string{want}) {
			t.Fatalf("after %s: frame themes %q, want [%s]", name, got, want)
		}
	}
	step("setTheme light", func() {
		if err := m.theme().setThemeName("light", false); err != nil {
			t.Fatal(err)
		}
	}, "light dark=false accent="+accent("light"))
	step("an edit", func() { m.editor.SetText("x") }, "-")
	step("a dark preview", func() { m.previewTheme("dark") }, "dark dark=true accent="+accent("dark"))
}

// Under a frontend session PiG picks the theme as it does on a plain terminal
// that shows the same appearance: the surface writes the terminal color query
// to the terminal the session draws on, and the replies decide light or
// dark. A light/dark report sends the query again, and its replies switch the
// theme. Each switch reaches the session as a frame with the new palette, and
// no reply reaches the editor. The replies are headless Tern 0.4.5's.
func TestFrontendThemeFollowsTheTerminalAppearance(t *testing.T) {
	query := "\x1b]10;?\x07\x1b]11;?\x07"
	for i := range 16 {
		query += fmt.Sprintf("\x1b]4;%d;?\x07", i)
	}
	query += "\x1b[c"
	replies := func(fg, bg string, palette ...string) []string {
		seqs := []string{"\x1b]10;rgb:" + fg + "\x07", "\x1b]11;rgb:" + bg + "\x07"}
		for i, color := range palette {
			seqs = append(seqs, fmt.Sprintf("\x1b]4;%d;rgb:%s\x07", i, color))
		}
		return append(seqs, "\x1b[?62;52;c")
	}
	light := replies("3b3b/3b3b/3b3b", "f8f8/f8f8/f8f8", "0000/0000/0000", "cdcd/3131/3131", "1010/7c7c/1010", "9191/9595/1414", "0404/5151/a5a5", "bcbc/0505/bcbc", "0505/9898/bcbc", "5555/5555/5555",
		"6666/6666/6666", "f1f1/4c4c/4c4c", "0909/a6a6/0a0a", "9191/9595/1616", "3b3b/8e8e/eaea", "cece/6868/cece", "1a1a/9c9c/baba", "a5a5/a5a5/a5a5")
	dark := replies("e8e8/ecec/f4f4", "1515/1818/2020", "3b3b/4242/4d4d", "ffff/4747/5757", "0000/ffff/8888", "f0f0/c0c0/4040", "0000/b4b4/ffff", "d0d0/6f6f/eded", "1313/d1d1/cdcd", "c3c3/c9c9/d3d3",
		"6b6b/7272/8080", "ffff/7b7b/8686", "6b6b/ffff/b0b0", "fdfd/dbdb/7272", "6e6e/c9c9/ffff", "e6e6/9d9d/fcfc", "7575/efef/eaea", "f7f7/f9f9/fcfc")
	for _, tc := range []struct{ setting, light, dark string }{
		{"light/dark", "light dark=false", "dark dark=true"},
		{"", "system dark=false", "system dark=true"},
	} {
		t.Run("theme="+tc.setting, func(t *testing.T) {
			restoreStartupTheme(t)
			t.Cleanup(func() { tui.SetTerminalColors(tui.TerminalColors{}); tui.SetTerminalColorScheme("") })
			manager := NewInMemorySettingsManager(Settings{Theme: tc.setting})
			session := &fakeFrontendSession{}
			out := new(syncedBuffer)
			m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{Frontend: &fakeFrontend{session: session}, TuiMode: "regular", Settings: manager.Get(), SettingsManager: manager}, out)
			m.openFrontend()
			m.mountInteractiveTui(true)
			ctx := t.Context()
			m.backgroundCtx = ctx
			t.Cleanup(func() { m.disposeTheme(); m.teardownCurrentTui(); m.stopInteractiveTui(); m.backgroundTasks.Wait() })
			m.initTheme()
			m.applyThemeFromSettings(ctx)
			wire := "\x1b[?2031h" + query
			if got := out.String(); got != wire {
				t.Fatalf("terminal = %q, want %q", got, wire)
			}
			step := func(name string, input []string, wire, want string) {
				t.Helper()
				for _, data := range input {
					if err := m.dispatchInputChunk(ctx, data, nil); err != nil {
						t.Fatal(err)
					}
				}
				if err := m.waitForTerminalColors(ctx); err != nil {
					t.Fatal(err)
				}
				m.tuiInst.Render()
				got := "-"
				for _, frame := range session.frames {
					if frame.Theme != nil {
						got = fmt.Sprintf("%s dark=%t", frame.Theme.Name, frame.Theme.Dark)
					}
				}
				if got != want || out.String() != wire || m.editor.Text() != "" || len(session.inputs) != 0 {
					t.Fatalf("%s: theme %q (want %q), terminal %q (want %q), editor %q, session inputs %q", name, got, want, out.String(), wire, m.editor.Text(), session.inputs)
				}
			}
			step("light replies", light, wire, tc.light)
			wire += query
			step("dark report and replies", append([]string{"\x1b[?997;1n"}, dark...), wire, tc.dark)
			wire += query
			step("light report and replies", append([]string{"\x1b[?997;2n"}, light...), wire, tc.light)
		})
	}
}

// turnTerminal records writes, holding each one until release closes, and
// noting a Write that starts while another is in progress.
type turnTerminal struct {
	entered, release chan struct{}
	inFlight         atomic.Int32
	overlapped       atomic.Bool
	buf              syncedBuffer
}

func (term *turnTerminal) Write(data []byte) (int, error) {
	if term.inFlight.Add(1) > 1 {
		term.overlapped.Store(true)
	}
	defer term.inFlight.Add(-1)
	select {
	case term.entered <- struct{}{}:
	default:
	}
	<-term.release
	return term.buf.Write(data)
}

// A session writes from its own goroutines, and PiG writes its terminal
// queries while the session draws: PiG's writes wait for a session's Write in
// progress, so a query never lands inside a frame.
func TestFrontendTerminalWritesTakeTurns(t *testing.T) {
	term := &turnTerminal{entered: make(chan struct{}, 1), release: make(chan struct{})}
	fe := &fakeFrontend{session: &fakeFrontendSession{}}
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{Frontend: fe, TuiMode: "regular"}, term)
	m.openFrontend()
	t.Cleanup(func() { m.teardownCurrentTui(); m.stopInteractiveTui(); m.backgroundTasks.Wait() })
	frame := "\x1b_tsp;f;{\"sf\":\"pig\",\"s\":1,\"ops\":[]}\x1b\\"
	written := make(chan struct{})
	go func() {
		_, _ = fe.env.Out.Write([]byte(frame))
		close(written)
	}()
	<-term.entered
	var pig sync.WaitGroup
	pig.Go(func() { m.tuiInst.QueryTerminalColors(tui.TerminalColorQueryOptions{TimeoutMs: 1}) })
	pig.Go(func() { m.writeThemeNotifications(true) })
	// Time for PiG's writes to start, if they did not wait.
	time.Sleep(50 * time.Millisecond)
	close(term.release)
	<-written
	pig.Wait()
	query := "\x1b]10;?\x07\x1b]11;?\x07"
	for i := range 16 {
		query += fmt.Sprintf("\x1b]4;%d;?\x07", i)
	}
	query += "\x1b[c"
	got := term.buf.String()
	if term.overlapped.Load() || (got != frame+query+"\x1b[?2031h" && got != frame+"\x1b[?2031h"+query) {
		t.Fatalf("overlapped=%t, terminal %q, want the frame, then the query and the notification request", term.overlapped.Load(), got)
	}
}

// A frontend learns the session file from the first frame and from the frame
// after each command that switches the file: /clone, /new, /fork and
// /resume, as Pi's getSessionFile reports it; "" is an in-memory session.
// Moving the leaf within the file, as /tree does, reports nothing.
func TestFrontendFramesReportTheSessionFileAcrossSwitches(t *testing.T) {
	m := sessionChromeMode(t)
	session := &fakeFrontendSession{}
	surface := tui.NewTuiSurfaceWithSize(session, 100, 30, func(err error) { t.Fatalf("apply: %v", err) })
	surface.SetRenderDispatcher(func(func()) {})
	surface.SetHooks(m.frontendSurfaceHooks())
	surface.SetLayout(m.chatContainer, nil)
	m.surface, m.tuiInst = surface, surface
	surface.Start()

	reported := func() []string {
		var files []string
		for _, frame := range session.frames {
			if frame.SessionFile != nil {
				files = append(files, *frame.SessionFile)
			}
		}
		return files
	}
	want := []string{m.currentSession().Path()}
	step := func(name string, run func() error) {
		t.Helper()
		if err := run(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		surface.Render()
		if got := reported(); !slices.Equal(got, want) {
			t.Fatalf("after %s: reported %q, want %q", name, got, want)
		}
	}
	step("start", func() error { return nil })
	original := m.currentSession().Path()
	firstUser := m.currentSession().GetEntries()[0].Base().ID

	switched := func(name string, run func() error) {
		t.Helper()
		before := m.currentSession().Path()
		step(name, func() error {
			if err := run(); err != nil {
				return err
			}
			if m.currentSession().Path() == before {
				t.Fatalf("%s kept the session file %s", name, before)
			}
			want = append(want, m.currentSession().Path())
			return nil
		})
	}
	switched("/clone", func() error { return cloneHandler(m.buildSlashContext(t.Context())) })
	switched("/new", func() error { return newHandler(m.buildSlashContext(t.Context())) })
	switched("/resume", func() error {
		sc := m.buildSlashContext(t.Context())
		sc.PickSession = func() (string, bool) { return original, true }
		return resumeHandler(sc)
	})
	switched("/fork", func() error { return m.buildSlashContext(t.Context()).ForkToNewSession(firstUser) })
	step("a leaf move", func() error {
		if err := m.currentSession().SetLeafID(nil); err != nil {
			return err
		}
		m.renderSessionEntries()
		return nil
	})
	step("an in-memory session", func() error {
		m.opts.SessionHandle.ReplaceInner(NewSession("in-memory", m.opts.CWD))
		m.renderCurrentSessionState()
		want = append(want, "")
		return nil
	})
}

func containsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

// An edit's patch reaches a frontend as the card's diff whether the result
// is live (typed details) or reloaded from the session (a details map); a
// failed call or details without a patch carry none.
func TestToolResultDiffReadsTheEditPatch(t *testing.T) {
	const patch = "--- a.go\n+++ a.go\n@@ -1 +1 @@\n-a\n+b\n"
	args := map[string]any{"path": "a.go"}
	for _, tc := range []struct {
		name   string
		args   map[string]any
		result any
		want   *frontend.Diff
	}{
		{"live", args, agent.AgentToolResult{Details: &tools.EditToolDetails{Diff: "+1 b", Patch: patch}}, &frontend.Diff{Path: "a.go", Text: patch}},
		{"reloaded", map[string]any{"file_path": "a.go"}, agent.AgentToolResult{Details: map[string]any{"diff": "+1 b", "patch": patch}}, &frontend.Diff{Path: "a.go", Text: patch}},
		{"failed", args, agent.AgentToolResult{Details: &tools.EditToolDetails{Patch: patch}, IsError: true}, nil},
		{"no patch", args, agent.AgentToolResult{Details: map[string]any{"diff": "+1 b"}}, nil},
		{"no result", args, nil, nil},
	} {
		if got := toolResultDiff("edit", tc.args, tc.result); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: diff = %#v, want %#v", tc.name, got, tc.want)
		}
	}
}

// newFrontendEditorProbe draws a run through a fake session with the editor
// focused and Pi's submit handler installed, as Run leaves them.
func newFrontendEditorProbe(t *testing.T) (*InteractiveMode, *fakeFrontend, *fakeFrontendSession) {
	t.Helper()
	session := &fakeFrontendSession{}
	fe := &fakeFrontend{session: session}
	m, _ := newFrontendProbe(t, fe, "regular")
	m.tuiInst.SetFocus(m.editor)
	m.setupEditorSubmitHandler(t.Context())
	return m, fe, session
}

// editorNode returns the editor node the session holds after every frame.
func editorNode(session *fakeFrontendSession) (frontend.Editor, bool) {
	var node frontend.Editor
	present := false
	for _, frame := range session.frames {
		for _, op := range frame.Ops {
			if op.Region != frontend.RegionDock || op.ID != "editor" {
				continue
			}
			node, present = frontend.Editor{}, op.Kind != frontend.Remove
			if present {
				node = op.Node.(frontend.Editor)
			}
		}
	}
	return node, present
}

const tspEvent = "\x1b_tsp;e;{}\x1b\\"

// An action the session takes while handling an input sequence runs before
// the next key, so a native edit and a typed key apply in the order Tern
// wrote them; each edit is one undo step.
func TestFrontendEditorActionsFromInputRunBeforeTheNextKey(t *testing.T) {
	m, fe, session := newFrontendEditorProbe(t)
	m.editor.SetText("hello world")
	dispatch := func(data string) {
		t.Helper()
		if err := m.dispatchInputChunk(t.Context(), data, nil); err != nil {
			t.Fatal(err)
		}
	}
	session.onInput = func(string) {
		fe.env.Edit(frontend.Edit{From: 6, To: 11, Text: "there", Cursor: 11, Len: 11})
	}
	dispatch(tspEvent)
	dispatch("!")
	if got := m.editor.Text(); got != "hello there!" {
		t.Fatalf("editor after edit and key = %q", got)
	}
	session.onInput = func(string) { fe.env.Undo() }
	dispatch(tspEvent)
	if got := m.editor.Text(); got != "hello there" {
		t.Fatalf("editor after one undo = %q", got)
	}
	dispatch(tspEvent)
	if got := m.editor.Text(); got != "hello world" {
		t.Fatalf("editor after two undos = %q", got)
	}
	runPostedTasks(t, m)
	if got := m.editor.Text(); got != "hello world" {
		t.Fatalf("editor after the posted drains = %q", got)
	}
}

// An edit computed against text that keys in flight have since changed is
// ignored; one against the current text applies, from any goroutine.
func TestFrontendEditIgnoresAStaleLength(t *testing.T) {
	m, fe, _ := newFrontendEditorProbe(t)
	m.editor.SetText("abc")
	done := make(chan struct{})
	go func() {
		defer close(done)
		fe.env.Edit(frontend.Edit{From: 0, To: 2, Text: "x", Cursor: 1, Len: 2})
	}()
	<-done
	runPostedTasks(t, m)
	if got := m.editor.Text(); got != "abc" {
		t.Fatalf("stale edit applied: %q", got)
	}
	fe.env.Edit(frontend.Edit{From: 0, To: 2, Text: "x", Cursor: 1, Len: 3})
	runPostedTasks(t, m)
	if got := m.editor.Text(); got != "xc" {
		t.Fatalf("current edit = %q", got)
	}
}

// Send takes the path Enter takes: a slash command runs with its arguments,
// a multi-line prompt reaches the prompt loop whole, and a prompt during
// compaction queues for after it. The draft in the editor stays.
func TestFrontendSendSubmitsLikeEnter(t *testing.T) {
	m, fe, _ := newFrontendEditorProbe(t)
	var args []string
	m.slashRegistry = NewSlashRegistry()
	m.slashRegistry.Register(BuiltinSlashCommand{Name: "probe", TakesArguments: true, Handler: func(sc *SlashContext) error {
		args = append(args, sc.Args)
		return nil
	}})
	m.editor.SetText("/probe typed")
	if err := m.dispatchKey(t.Context(), "\r"); err != nil {
		t.Fatal(err)
	}
	m.editor.SetText("draft")
	fe.env.Send("  /probe one two \n")
	fe.env.Send("first line\nsecond line")
	runPostedTasks(t, m)
	if !reflect.DeepEqual(args, []string{"typed", "one two"}) {
		t.Fatalf("command args = %q", args)
	}
	if !reflect.DeepEqual(m.pendingUserInputs, []string{"first line\nsecond line"}) {
		t.Fatalf("prompts = %q", m.pendingUserInputs)
	}
	if got := m.editor.Text(); got != "draft" {
		t.Fatalf("editor after send = %q", got)
	}

	m.isCompacting = true
	fe.env.Send("after compaction")
	runPostedTasks(t, m)
	if len(m.compactionQueue) != 1 || m.compactionQueue[0].text != "after compaction" || m.compactionQueue[0].mode != compactionQueueSteer {
		t.Fatalf("compaction queue = %#v", m.compactionQueue)
	}
}

// While the agent works, a sent prompt steers it, as Enter does.
func TestFrontendSendWhileWorkingSteers(t *testing.T) {
	m := newPendingDisplayHarness(t)
	provider := &blockingProvider{started: make(chan struct{}), release: make(chan struct{})}
	m.agent = mustNewAgent(agent.AgentOptions{Model: &ai.Model{ID: "m", Provider: provider}})
	result := make(chan error, 1)
	go func() {
		_, err := m.agent.Send(t.Context(), "initial")
		result <- err
	}()
	<-provider.started
	m.isIdle = false
	m.editor.SetText("draft")
	m.sendFrontendPrompt(t.Context(), "check this while you work")
	steering, followUps := m.agent.PendingMessages()
	if len(steering) != 1 || len(followUps) != 0 || extractAgentMessageText(steering[0]) != "check this while you work" {
		t.Fatalf("steering = %v, follow-ups = %v", steering, followUps)
	}
	if got := m.editor.Text(); got != "draft" {
		t.Fatalf("editor after send = %q", got)
	}
	close(provider.release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

// The editor is sendable once the input loop runs and while it has the
// keys, streaming and compacting included; a selector's focus, the external
// editor and a requested exit clear it, and an overlay removes the node.
func TestFrontendEditorSendableTransitions(t *testing.T) {
	m, _, session := newFrontendEditorProbe(t)
	state := func() (bool, bool) {
		m.tuiInst.Render()
		node, present := editorNode(session)
		return node.Sendable, present
	}
	expect := func(step string, sendable, present bool) {
		t.Helper()
		if gotSendable, gotPresent := state(); gotSendable != sendable || gotPresent != present {
			t.Fatalf("%s: sendable = %v, present = %v", step, gotSendable, gotPresent)
		}
	}
	expect("before the input loop", false, true)
	m.frontendInputReady()
	expect("input loop", true, true)
	m.turnActive.Store(true)
	m.isCompacting = true
	expect("streaming and compacting", true, true)
	m.turnActive.Store(false)
	m.isCompacting = false
	selector := tui.NewText("selector")
	m.tuiInst.SetFocus(selector)
	expect("selector focused", false, true)
	m.tuiInst.SetFocus(m.editor)
	expect("focus back", true, true)
	m.externalEditorActive = true
	expect("external editor", false, true)
	m.externalEditorActive = false
	// An extension dialog takes the keys without taking the focus.
	m.extensionDialog = &extensionDialog{component: tui.NewText("dialog")}
	expect("extension dialog", false, true)
	m.extensionDialog = nil
	expect("extension dialog closed", true, true)
	handle := m.tuiInst.ShowOverlay(tui.NewText("overlay"), tui.OverlayOptions{})
	expect("overlay", false, false)
	handle.Close()
	expect("overlay closed", true, true)
	m.requestShutdown()
	expect("exit requested", false, true)
}

// After the session closes, its actions change nothing, whether they were
// queued before the close or after it.
func TestFrontendEditorActionsAfterCloseDoNothing(t *testing.T) {
	m, fe, _ := newFrontendEditorProbe(t)
	m.editor.SetText("abc")
	fe.env.Edit(frontend.Edit{From: 0, To: 3, Text: "x", Cursor: 1, Len: 3})
	m.stopInteractiveTui()
	fe.env.Send("late")
	runPostedTasks(t, m)
	if got := m.editor.Text(); got != "abc" || len(m.pendingUserInputs) != 0 {
		t.Fatalf("editor = %q, prompts = %q", got, m.pendingUserInputs)
	}
}
