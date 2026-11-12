package codingagent

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// viewModeProbe is an interactive mode on the production renderer with an extension bridge bound to it, as
// attachSubprocess binds one. selected says whether a Piglet selected the terminal view client.
type viewModeProbe struct {
	t      *testing.T
	m      *InteractiveMode
	term   *terminalRecorder
	bridge *subprocess.UIBridge
}

func newViewModeProbe(t *testing.T, mode string, selected bool) *viewModeProbe {
	t.Helper()
	m, term := newTickRenderProbe(t, mode)
	m.opts.TerminalViewClient = selected
	bridge := subprocess.NewUIBridge(m.requestRender)
	bridge.SetUIContext(&ExtUIContext{m: m})
	m.tuiInst.SetFocus(m.editor)
	m.attachTerminalViewClient(bridge)
	t.Cleanup(m.detachTerminalViewClient)
	p := &viewModeProbe{t: t, m: m, term: term, bridge: bridge}
	// One frame with the editor focused, so every probe starts from the same renderer state.
	m.requestRender()
	p.frame()
	return p
}

// frame lets the loop run what is due and returns what the terminal was written since the last frame.
func (p *viewModeProbe) frame() string {
	time.Sleep(100 * time.Millisecond)
	synctest.Wait()
	p.m.drainMainLoopOnce()
	time.Sleep(100 * time.Millisecond)
	synctest.Wait()
	p.m.drainMainLoopOnce()
	return p.term.take()
}

// call makes a ui.view call as the extension ext and returns the status, or the error code the host answered.
func (p *viewModeProbe) call(method, args string) (status struct {
	Mount json.RawMessage `json:"mount"`
	State string          `json:"state"`
}, code string) {
	p.t.Helper()
	res, err := p.bridge.HandleCall("ext", &subprocess.CallPayload{Method: method, Args: json.RawMessage(args)})
	if err != nil {
		p.t.Fatalf("%s: %v", method, err)
	}
	if res.Error != nil {
		return status, res.Error.Code
	}
	if err := json.Unmarshal(res.Result, &status); err != nil {
		p.t.Fatalf("%s: %v", method, err)
	}
	return status, ""
}

func (p *viewModeProbe) must(method, args string) json.RawMessage {
	p.t.Helper()
	status, code := p.call(method, args)
	if code != "" {
		p.t.Fatalf("%s %s: %s", method, args, code)
	}
	return status.Mount
}

func (p *viewModeProbe) typeText(text string) string {
	for _, r := range text {
		p.m.editor.HandleInput(string(r))
	}
	p.m.requestRender()
	return p.frame()
}

const viewProbeMount = `{"key":"probe","slot":"%SLOT%","view":{"root":{"kind":"text","text":"mounted view row"}}}`

// pig additive (D614): Stock PiG attaches no view client, so a mount fails unsupported as it always did; with the client
// a Piglet selects, the same mount is accepted and drawn.
func TestTerminalViewClientIsSelectedNotDefault(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mount := strings.Replace(viewProbeMount, "%SLOT%", "panel", 1)
				stock := newViewModeProbe(t, mode, false)
				if _, code := stock.call("ui.view.mount", mount); code != "unsupported" {
					t.Fatalf("a mount in Stock PiG answered %q, want unsupported", code)
				}
				if stock.bridge.Views().Abilities() != nil || stock.m.viewClient.Load() != nil {
					t.Fatal("Stock PiG attached a view client")
				}
				if frame := stock.frame(); frame != "" {
					t.Fatalf("a refused mount wrote to the terminal: %q", frame)
				}
				selected := newViewModeProbe(t, mode, true)
				if status, code := selected.call("ui.view.mount", mount); code != "" || status.State != "live" {
					t.Fatalf("a mount with the terminal client selected answered %q, state %q", code, status.State)
				}
				if frame := widthx.StripAnsi(selected.frame()); !strings.Contains(frame, "mounted view row") {
					t.Fatalf("the mount was not drawn: %q", frame)
				}
			})
		})
	}
}

// pig additive (D614): the client selected and never sent a mount costs a session nothing: the terminal is written the
// same bytes, no goroutine runs for it, the widget containers hold the same children and a frame allocates the same.
func TestTerminalViewClientNeverMountedCostsNothing(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				run := func(selected bool) (written string, goroutines int, children int, allocs float64) {
					m, term := newTickRenderProbe(t, mode)
					m.opts.TerminalViewClient = selected
					bridge := subprocess.NewUIBridge(m.requestRender)
					bridge.SetUIContext(&ExtUIContext{m: m})
					synctest.Wait()
					before := runtime.NumGoroutine()
					m.attachTerminalViewClient(bridge)
					synctest.Wait()
					goroutines = runtime.NumGoroutine() - before
					p := &viewModeProbe{t: t, m: m, term: term, bridge: bridge}
					written = p.frame() + p.typeText("hello") + p.typeText(" world")
					m.viewClientResized()
					written += p.frame()
					children = len(m.widgetContainer.Children())
					allocs = testing.AllocsPerRun(20, func() { m.tuiInst.Render() })
					term.take()
					m.detachTerminalViewClient()
					return written, goroutines, children, allocs
				}
				offBytes, offGoroutines, offChildren, offAllocs := run(false)
				onBytes, onGoroutines, onChildren, onAllocs := run(true)
				if onBytes != offBytes {
					t.Errorf("the terminal was written different bytes with the client selected:\noff %q\non  %q", offBytes, onBytes)
				}
				if offBytes == "" {
					t.Error("the session wrote nothing: the comparison is empty")
				}
				if onGoroutines != 0 || offGoroutines != 0 {
					t.Errorf("attaching started goroutines: off %d, on %d", offGoroutines, onGoroutines)
				}
				if onChildren != offChildren {
					t.Errorf("widget container children: off %d, on %d", offChildren, onChildren)
				}
				if onAllocs != offAllocs {
					t.Errorf("allocations of one frame: off %v, on %v", offAllocs, onAllocs)
				}
				t.Logf("%s: bytes %d = %d, goroutines %d = %d, widget children %d = %d, allocations per frame %v = %v", mode, len(offBytes), len(onBytes), offGoroutines, onGoroutines, offChildren, onChildren, offAllocs, onAllocs)
			})
		})
	}
}

// pig additive (D614): a session that mounted a view and ended it draws, from then on, what a session that never
// mounted draws: the same widget rows and the same bytes for the same keys, after a remove, after a finish, and with a
// hide and a show in between.
func TestTerminalViewClientMountThenUnmountLeavesNoTrace(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		for _, slot := range []string{"panel", "widget-above"} {
			for _, end := range []string{"ui.view.remove", "ui.view.finish"} {
				t.Run(mode+"/"+slot+"/"+end, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						never := newViewModeProbe(t, mode, true)
						mounted := newViewModeProbe(t, mode, true)
						ref := mounted.must("ui.view.mount", strings.Replace(viewProbeMount, "%SLOT%", slot, 1))
						if frame := widthx.StripAnsi(mounted.frame()); !strings.Contains(frame, "mounted view row") {
							t.Fatalf("the mount was not drawn: %q", frame)
						}
						ref = mounted.must("ui.view.update", `{"mount":`+string(ref)+`,"view":{"root":{"kind":"text","text":"updated view row\nsecond row"}}}`)
						if frame := widthx.StripAnsi(mounted.frame()); !strings.Contains(frame, "updated view row") {
							t.Fatalf("the update was not drawn: %q", frame)
						}
						mounted.must("ui.view.hide", `{"mount":`+string(ref)+`}`)
						mounted.frame()
						mounted.must("ui.view.show", `{"mount":`+string(ref)+`}`)
						if frame := widthx.StripAnsi(mounted.frame()); !strings.Contains(frame, "updated view row") {
							t.Fatalf("the shown mount was not drawn: %q", frame)
						}
						mounted.must(end, `{"mount":`+string(ref)+`}`)
						mounted.frame()
						never.frame()

						if got, want := mounted.m.widgetContainer.Render(80), never.m.widgetContainer.Render(80); strings.Join(got, "\n") != strings.Join(want, "\n") {
							t.Fatalf("widget rows after the mount ended: %q, never mounted: %q", got, want)
						}
						if len(mounted.m.viewMounts) != 0 || len(mounted.m.viewClient.Load().mounts) != 0 {
							t.Fatal("the client still holds the mount")
						}
						for _, text := range []string{"hello", " there", "\x7f\x7f"} {
							got, want := mounted.typeText(text), never.typeText(text)
							if got != want {
								t.Fatalf("after the mount ended, typing %q wrote\n%q\nand a session that never mounted wrote\n%q", text, got, want)
							}
							if want == "" {
								t.Fatalf("typing %q wrote nothing: the comparison is empty", text)
							}
						}
						mounted.m.tuiInst.Invalidate()
						never.m.tuiInst.Invalidate()
						mounted.m.requestRender()
						never.m.requestRender()
						if got, want := mounted.frame(), never.frame(); got != want {
							t.Fatalf("a full redraw after the mount ended wrote\n%q\nand a session that never mounted wrote\n%q", got, want)
						}
					})
				})
			}
		}
	}
}

// pig additive (D614): with a panel focused, the key that interrupts returns the focus to the editor and does not reach
// the editor's interrupt; pressed again it interrupts, as it does in a session with no panel. F6 gives the focus back.
func TestTerminalViewClientEscapeLeavesThePanelBeforeItInterrupts(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := newViewModeProbe(t, mode, true)
				interrupts := 0
				p.m.defaultCustomEditor().OnEscape = func() { interrupts++ }
				p.must("ui.view.mount", `{"key":"list","slot":"panel","focus":[{"node":"pick","request":"mount"}],"view":{"root":{"kind":"select-list","id":"pick","maxVisible":4,"items":[{"value":"a","label":"Alpha"},{"value":"b","label":"Beta"}]},"focus":"pick"}}`)
				p.frame()
				mount := p.m.viewClient.Load().order[0]
				if p.m.tuiInst.GetFocusedComponent() != mount {
					t.Fatal("the panel that asked for the focus does not hold it")
				}
				if err := p.m.handleKey(t.Context(), "\x1b"); err != nil {
					t.Fatal(err)
				}
				if interrupts != 0 {
					t.Fatal("Escape in a focused panel interrupted")
				}
				if focused := p.m.tuiInst.GetFocusedComponent(); focused != p.m.editor {
					t.Fatalf("after Escape the focus is %T, want the editor", focused)
				}
				if err := p.m.handleKey(t.Context(), "\x1b"); err != nil {
					t.Fatal(err)
				}
				if interrupts != 1 {
					t.Fatalf("the second Escape interrupted %d times, want 1", interrupts)
				}
				if _, consumed := p.m.tuiInst.RunInputListeners("\x1b[17~"); !consumed || p.m.tuiInst.GetFocusedComponent() != mount {
					t.Fatal("F6 did not give the focus to the panel")
				}
				if err := p.m.handleKey(t.Context(), "\x1b[B"); err != nil {
					t.Fatal(err)
				}
				if frame := widthx.StripAnsi(p.frame()); !strings.Contains(frame, "Beta") {
					t.Fatalf("Down in the focused panel drew %q", frame)
				}
				if text := p.m.editor.Text(); text != "" {
					t.Fatalf("a key of the focused panel reached the editor: %q", text)
				}
			})
		})
	}
}
