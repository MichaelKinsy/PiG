package codingagent

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/tui"
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
// same bytes, no goroutine runs for it and the widget containers hold the same children. The allocations of a frame are
// compared in view_client_allocs_norace_test.go.
func TestTerminalViewClientNeverMountedCostsNothing(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				run := func(selected bool) (written string, goroutines int, children int) {
					m, term := newTickRenderProbe(t, mode)
					m.opts.TerminalViewClient = selected
					bridge := subprocess.NewUIBridge(m.requestRender)
					bridge.SetUIContext(&ExtUIContext{m: m})
					synctest.Wait()
					before := testenv.LiveGoroutines()
					m.attachTerminalViewClient(bridge)
					synctest.Wait()
					goroutines = testenv.LiveGoroutines() - before
					p := &viewModeProbe{t: t, m: m, term: term, bridge: bridge}
					written = p.frame() + p.typeText("hello") + p.typeText(" world")
					m.viewClientResized()
					written += p.frame()
					children = len(m.widgetContainer.Children())
					m.detachTerminalViewClient()
					return written, goroutines, children
				}
				offBytes, offGoroutines, offChildren := run(false)
				onBytes, onGoroutines, onChildren := run(true)
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
				t.Logf("%s: bytes %d = %d, goroutines %d = %d, widget children %d = %d", mode, len(offBytes), len(onBytes), offGoroutines, onGoroutines, offChildren, onChildren)
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

// viewAttachRun is one session whose extension takes the state the bridge pushes when the client attaches, which is how
// the abilities reach an extension, and, with mounts, mounts at that moment, off the loop.
type viewAttachRun struct {
	p              *viewModeProbe
	before, frames string
	second         string
	pushes         int32
	code, state    string
}

func runViewAttach(t *testing.T, mode, slot string, selected, mounts bool) *viewAttachRun {
	t.Helper()
	m, term := newTickRenderProbe(t, mode)
	m.opts.TerminalViewClient = selected
	bridge := subprocess.NewUIBridge(m.requestRender)
	bridge.SetUIContext(&ExtUIContext{m: m})
	m.tuiInst.SetFocus(m.editor)
	r := &viewAttachRun{p: &viewModeProbe{t: t, m: m, term: term, bridge: bridge}}
	m.requestRender()
	r.before = r.p.frame()
	var mu sync.Mutex
	mounted := false
	bridge.OnStateChanged = func() {
		mu.Lock()
		defer mu.Unlock()
		if bridge.Views().Abilities() == nil {
			return
		}
		r.pushes++
		if !mounts || mounted {
			return
		}
		mounted = true
		res, err := bridge.HandleCall("ext", &subprocess.CallPayload{Method: "ui.view.mount", Args: json.RawMessage(strings.Replace(viewProbeMount, "%SLOT%", slot, 1))})
		switch {
		case err != nil:
			r.code = err.Error()
		case res.Error != nil:
			r.code = res.Error.Code
		default:
			var status struct {
				State string `json:"state"`
			}
			if err := json.Unmarshal(res.Result, &status); err != nil {
				r.code = err.Error()
			}
			r.state = status.State
		}
	}
	m.attachTerminalViewClient(bridge)
	t.Cleanup(m.detachTerminalViewClient)
	first := r.p.frame()
	r.second = r.p.frame()
	r.frames = first + r.second + r.p.typeText("hello") + r.p.typeText(" world")
	// The lock orders this goroutine's reads after the pushes' writes.
	mu.Lock()
	defer mu.Unlock()
	return r
}

// pig additive (D614): an extension that mounts on the abilities it is pushed when the client attaches is drawn once: the
// mount is on the screen after the attach and no later frame draws it again. In both TUI modes and in a panel and a
// widget slot.
func TestTerminalViewClientDrawsAMountMadeAtTheAttachOnce(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		for _, slot := range []string{"panel", "widget-above"} {
			t.Run(mode+"/"+slot, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					r := runViewAttach(t, mode, slot, true, true)
					if r.pushes == 0 {
						t.Fatal("the attach pushed no state with abilities: the extension had no moment to mount")
					}
					if r.code != "" || r.state != "live" {
						t.Fatalf("the mount at the attach answered %q, state %q", r.code, r.state)
					}
					if n := strings.Count(widthx.StripAnsi(r.frames), "mounted view row"); n != 1 {
						t.Fatalf("the mount made at the attach was drawn %d times, want 1:\n%q", n, r.frames)
					}
					if r.second != "" {
						t.Fatalf("the frame after the one that drew the mount wrote %q", r.second)
					}
					if client := r.p.m.viewClient.Load(); len(client.mounts) != 1 || len(r.p.m.viewMounts) != 1 {
						t.Fatalf("the client holds %d mount(s) and the layout %d, want 1 and 1", len(client.mounts), len(r.p.m.viewMounts))
					}
				})
			})
		}
	}
}

// pig additive (D614): an extension that takes the same push and does not mount leaves the session what it is without
// the client: the terminal is written the same bytes before the attach, after it and while a line is typed.
func TestTerminalViewClientAttachWithAListeningExtensionCostsNothing(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				off := runViewAttach(t, mode, "panel", false, false)
				on := runViewAttach(t, mode, "panel", true, false)
				if off.pushes != 0 {
					t.Fatalf("a session without the client pushed abilities %d times", off.pushes)
				}
				if on.pushes == 0 {
					t.Fatal("the attach pushed no state with abilities: the comparison has no listening extension")
				}
				if on.before != off.before || on.frames != off.frames {
					t.Errorf("the terminal was written different bytes with the client attached and a listening extension:\noff %q\non  %q", off.before+off.frames, on.before+on.frames)
				}
				if off.frames == "" {
					t.Error("the session wrote nothing after the attach: the comparison is empty")
				}
				if n := len(on.p.m.widgetContainer.Children()) - len(off.p.m.widgetContainer.Children()); n != 0 {
					t.Errorf("the widget container holds %d more children with the client attached", n)
				}
			})
		})
	}
}

const viewProbeList = `{"root":{"kind":"select-list","id":"pick","maxVisible":4,"items":[{"value":"a","label":"Alpha"},{"value":"b","label":"Beta"}]},"focus":"pick"}`

// pig additive (D614): one live panel without a title is drawn as it was before panels were tabs: a client whose panel
// slot has tabs and one whose panels are stacked write the same bytes for the mount, for F6 into it and for a key in it.
func TestTerminalViewClientOneUntitledPanelDrawsAsWithoutTabs(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				run := func(tabs bool) []string {
					slot := viewSlotOf(uiview.SlotPanel)
					was := slot.tabs
					slot.tabs = tabs
					defer func() { slot.tabs = was }()
					p := newViewModeProbe(t, mode, true)
					var frames []string
					p.must("ui.view.mount", `{"key":"list","slot":"panel","view":`+viewProbeList+`}`)
					frames = append(frames, p.frame())
					if _, consumed := p.m.tuiInst.RunInputListeners("\x1b[17~"); !consumed {
						t.Fatal("F6 was not consumed")
					}
					frames = append(frames, p.frame())
					if err := p.m.handleKey(t.Context(), "\x1b[B"); err != nil {
						t.Fatal(err)
					}
					frames = append(frames, p.frame())
					if p.m.viewTabRow != nil {
						t.Fatal("one live panel without a title laid a tab row")
					}
					p.m.detachTerminalViewClient()
					return frames
				}
				tabs, stacked := run(true), run(false)
				for i := range tabs {
					if tabs[i] != stacked[i] {
						t.Fatalf("frame %d with tabs wrote\n%q\nand stacked\n%q", i, tabs[i], stacked[i])
					}
					if stacked[i] == "" {
						t.Fatalf("frame %d is empty: the comparison proves nothing", i)
					}
				}
			})
		})
	}
}

// pig additive (D614): the production renderer draws titled panels as tabs: one row of titles, the front tab's view
// under it, F6 from tab to tab. A focus request moves the focus from an empty editor and leaves an editor that holds
// text alone, and the extension reads why.
func TestTerminalViewClientDrawsPanelsAsTabs(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := newViewModeProbe(t, mode, true)
				p.must("ui.view.mount", `{"key":"mail","slot":"panel","title":"Mail","view":{"root":{"kind":"text","text":"the inbox rows"}}}`)
				p.must("ui.view.mount", `{"key":"cal","slot":"panel","title":"Calendar","view":`+viewProbeList+`}`)
				frame := widthx.StripAnsi(p.frame())
				if !strings.Contains(frame, "Mail  Calendar") || !strings.Contains(frame, "Alpha") || strings.Contains(frame, "the inbox rows") {
					t.Fatalf("two titled panels drew %q, want the tab row and the panel shown last alone", frame)
				}
				client := p.m.viewClient.Load()
				mail, cal := client.order[0], client.order[1]
				if _, consumed := p.m.tuiInst.RunInputListeners("\x1b[17~"); !consumed || p.m.tuiInst.GetFocusedComponent() != tui.Component(p.m.viewTabRow) {
					t.Fatalf("F6 from the editor: the focus is %T, want the tab row on Mail, whose view takes no key", p.m.tuiInst.GetFocusedComponent())
				}
				if frame := widthx.StripAnsi(p.frame()); !strings.Contains(frame, "[Mail]  Calendar") || !strings.Contains(frame, "the inbox rows") || strings.Contains(frame, "Alpha") {
					t.Fatalf("on the first tab the terminal drew %q", frame)
				}
				if _, consumed := p.m.tuiInst.RunInputListeners("\x1b[17~"); !consumed || p.m.tuiInst.GetFocusedComponent() != tui.Component(cal) {
					t.Fatal("F6 from the first tab did not focus the second tab's view")
				}
				if _, consumed := p.m.tuiInst.RunInputListeners("\x1b[17~"); !consumed || p.m.tuiInst.GetFocusedComponent() != tui.Component(p.m.editor) {
					t.Fatal("F6 from the last tab did not return the focus to the editor")
				}
				p.frame()

				// The editor holds text: Mail asks for the focus on an update, is shown, and the focus stays.
				p.typeText("draft")
				mailRef := `{"key":"mail","epoch":` + strconv.FormatUint(mail.ref.Epoch, 10) + `,"rev":` + strconv.FormatUint(mail.ref.Rev, 10) + `}`
				asking := `,"title":"Mail","focus":[{"node":"pick","request":"update"}],"view":` + viewProbeList + `}`
				next := p.must("ui.view.update", `{"mount":`+mailRef+asking)
				if frame := widthx.StripAnsi(p.frame()); p.m.tuiInst.GetFocusedComponent() != tui.Component(p.m.editor) || client.front != mail || !strings.Contains(frame, "Mail  Calendar") {
					t.Fatalf("a focus request while the editor holds text: focus %T, front %s, frame %q", p.m.tuiInst.GetFocusedComponent(), client.front.key.key, frame)
				}
				if text := p.m.editor.Text(); text != "draft" {
					t.Fatalf("the editor holds %q, want draft", text)
				}
				var diag struct {
					Notices []uiview.Notice `json:"notices"`
				}
				res, err := p.bridge.HandleCall("ext", &subprocess.CallPayload{Method: "ui.view.diagnostics", Args: json.RawMessage(`{"key":"mail"}`)})
				if err != nil || res.Error != nil || json.Unmarshal(res.Result, &diag) != nil {
					t.Fatalf("diagnostics: %v %+v", err, res)
				}
				if !slices.ContainsFunc(diag.Notices, func(n uiview.Notice) bool {
					return n.Code == uiview.NoticeUnfocused && n.Path == "focus" && n.Message == "the editor holds text"
				}) {
					t.Fatalf("the extension reads %+v, want an unfocused notice: the editor holds text", diag.Notices)
				}
				// The editor is empty: the same request is granted.
				p.typeText("\x7f\x7f\x7f\x7f\x7f")
				p.must("ui.view.update", `{"mount":`+string(next)+asking)
				p.frame()
				if p.m.tuiInst.GetFocusedComponent() != tui.Component(mail) {
					t.Fatalf("a focus request with an empty editor: the focus is %T, want Mail's view", p.m.tuiInst.GetFocusedComponent())
				}
			})
		})
	}
}
