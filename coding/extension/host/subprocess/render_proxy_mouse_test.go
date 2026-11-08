package subprocess

import (
	"context"
	"encoding/json"
	"net"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// mouseRig is a render proxy for a displayed message (it has an identity) over a pipe the test plays the extension on.
type mouseRig struct {
	component   *renderProxyComponent
	peer        net.Conn
	invalidated *atomic.Int32
	identity    *extension.RenderIdentity
}

func newMouseRig(t *testing.T, identity *extension.RenderIdentity) *mouseRig {
	t.Helper()
	hostEnd, peer := net.Pipe()
	conn := NewConn("render-mouse-test", hostEnd)
	ctx, cancel := context.WithCancel(context.Background())
	conn.Start(ctx)
	invalidated := &atomic.Int32{}
	component := newRenderProxyComponent("fixture", "card", extension.CustomMessage{CustomType: "card", Content: "body", Display: true},
		extension.MessageRenderOptions{Identity: identity}, conn, time.Second, func() { invalidated.Add(1) })
	t.Cleanup(func() {
		cancel()
		_ = peer.Close()
		_ = hostEnd.Close()
		<-conn.done
	})
	return &mouseRig{component: component, peer: peer, invalidated: invalidated, identity: identity}
}

// request reads the next render_message request and returns its decoded arguments.
func (r *mouseRig) request(t *testing.T) (Envelope, RenderComponentPayload) {
	t.Helper()
	env := readFramed(t, r.peer)
	if env.Request == nil || env.Request.Method != "render_message" {
		t.Fatalf("request = %+v", env)
	}
	var payload RenderComponentPayload
	if err := json.Unmarshal(env.Request.Args, &payload); err != nil {
		t.Fatal(err)
	}
	return env, payload
}

func (r *mouseRig) reply(t *testing.T, env Envelope, result RenderResult) {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	writeFramed(t, r.peer, Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{Result: encoded}})
}

// cardView is a kit view: a mouse region named "card" that answers click only, over one row of text.
const cardView = `{"root":{"kind":"mouse-region","id":"card","region":{"types":["click"]},"children":[{"kind":"text","text":"hello","paddingX":0,"paddingY":0}]}}`

// drawn is the first row the proxy paints at width, "" while it paints none (a frame for another width is not painted).
func (r *mouseRig) drawn(width int) string {
	if lines := r.component.Render(width); len(lines) > 0 {
		return lines[0]
	}
	return ""
}

func clickAt(x, y int) tui.TuiMouseEvent {
	return tui.TuiMouseEvent{Type: tui.MouseClick, Button: tui.MouseButtonLeft, X: x, Y: y, ScreenX: x + 4, ScreenY: y + 9, Width: 80, Height: 1, ClickCount: 1}
}

// shown runs the first request through to a drawn view and returns once the proxy paints it.
func (r *mouseRig) shown(t *testing.T, result RenderResult) {
	t.Helper()
	_ = r.component.Render(80)
	env, first := r.request(t)
	if first.Key == "" || !first.Rerender || len(first.Events) != 0 {
		t.Fatalf("first request key=%q rerender=%t events=%d, want a key, rerender and no events", first.Key, first.Rerender, len(first.Events))
	}
	r.reply(t, env, result)
	waitForRenderProxy(t, func() bool { return r.invalidated.Load() > 0 })
	r.component.Render(80)
}

// A click on a kit mouse region is answered at once from the region's description, and the extension hears of it in the next request, which draws the
// card again without running the renderer (rerender is false).
func TestRenderProxyViewMouseRegionClickReachesTheExtensionInTheNextRequest(t *testing.T) {
	rig := newMouseRig(t, extension.NewRenderIdentity())
	rig.shown(t, RenderResult{View: json.RawMessage(cardView)})
	if got := rig.component.Render(80); len(got) != 1 || strings.TrimRight(got[0], " ") != "hello" {
		t.Fatalf("view drawn as %q", got)
	}

	if result := rig.component.HandleMouse(tui.TuiMouseEvent{Type: tui.MousePress, Button: tui.MouseButtonLeft, X: 2, Y: 0, Width: 80, Height: 1}); result != nil {
		t.Fatalf("press handled by a region that answers click only: %+v", result)
	}
	result := rig.component.HandleMouse(clickAt(2, 0))
	if result == nil || !result.Handled || result.Target.Component != rig.component {
		t.Fatalf("click result = %+v, want handled with the proxy as target", result)
	}

	_, second := rig.request(t)
	if second.Rerender {
		t.Fatal("the request that carries a click runs the renderer; Pi draws the component it returned")
	}
	if len(second.Events) != 1 {
		t.Fatalf("the press left no event and the click one, got %d events: %+v", len(second.Events), second.Events)
	}
	event := second.Events[0]
	if event.Type != ViewEventMouse || event.Node != "card" || event.Mouse == nil || event.Mouse.Type != "click" || event.Mouse.X != 2 || event.Mouse.Y != 0 {
		t.Fatalf("event = %+v, want the click on card at 2,0", event)
	}
}

// A Node component whose tree has MouseRegions is hit-tested by the host from the rectangles the runtime found with Pi's own dispatch: a click inside
// one is handled and reaches the extension, a click outside every one (the padding of the card) is left unhandled so it falls through to text
// selection as in Pi, and the other events are never handled.
func TestRenderProxyNodeMouseRegionsAreHitTestedLikePi(t *testing.T) {
	rig := newMouseRig(t, extension.NewRenderIdentity())
	rig.shown(t, RenderResult{Lines: []string{"drawn by node", "second row"}, Regions: []RenderMouseRegion{{X: 3, Y: 0, Width: 20, Height: 2}}})
	for _, outside := range [][2]int{{1, 0}, {2, 1}, {23, 0}, {5, 2}} {
		if result := rig.component.HandleMouse(clickAt(outside[0], outside[1])); result != nil {
			t.Fatalf("click at %v outside every region handled: %+v", outside, result)
		}
	}
	for _, kind := range []tui.TuiMouseEventType{tui.MousePress, tui.MouseRelease, tui.MouseDrag, tui.MouseMove, tui.MouseWheel} {
		if result := rig.component.HandleMouse(tui.TuiMouseEvent{Type: kind, Button: tui.MouseButtonLeft, X: 5, Y: 0, Width: 80, Height: 2}); result != nil {
			t.Fatalf("%s handled: %+v", kind, result)
		}
	}
	if result := rig.component.HandleMouse(clickAt(22, 1)); result == nil || !result.Handled {
		t.Fatalf("click inside the region = %+v, want handled", result)
	}
	_, next := rig.request(t)
	if len(next.Events) != 1 || next.Events[0].Mouse == nil || next.Events[0].Mouse.X != 22 || next.Events[0].Mouse.Y != 1 {
		t.Fatalf("events = %+v, want the one click at 22,1", next.Events)
	}
}

// A Node component that handles the mouse itself (not a MouseRegion) is the residue (D277): it takes the click only, answered as handled for the whole
// component, and gets the event local to the component. Its other events stay with the host's selection and scrolling.
func TestRenderProxyNodeComponentTakesClickOnly(t *testing.T) {
	rig := newMouseRig(t, extension.NewRenderIdentity())
	rig.shown(t, RenderResult{Lines: []string{"drawn by node"}, Mouse: true})
	for _, kind := range []tui.TuiMouseEventType{tui.MousePress, tui.MouseRelease, tui.MouseDrag, tui.MouseMove, tui.MouseWheel} {
		if result := rig.component.HandleMouse(tui.TuiMouseEvent{Type: kind, Button: tui.MouseButtonLeft, X: 1, Y: 0, Width: 80, Height: 1}); result != nil {
			t.Fatalf("%s handled by a component that takes the click only: %+v", kind, result)
		}
	}
	result := rig.component.HandleMouse(clickAt(3, 0))
	if result == nil || !result.Handled {
		t.Fatalf("click result = %+v, want handled", result)
	}
	_, next := rig.request(t)
	if len(next.Events) != 1 || next.Events[0].Type != ViewEventMouse || next.Events[0].Mouse == nil || next.Events[0].Mouse.X != 3 || next.Events[0].Node != "" {
		t.Fatalf("events = %+v, want the click at 3,0 with no region", next.Events)
	}
}

// Without the extension's report that its component takes the mouse, and for a renderer call that is not for a displayed message, nothing is handled
// and nothing is sent.
func TestRenderProxyWithoutMouseTakesNone(t *testing.T) {
	rig := newMouseRig(t, extension.NewRenderIdentity())
	rig.shown(t, RenderResult{Lines: []string{"plain"}})
	if result := rig.component.HandleMouse(clickAt(1, 0)); result != nil {
		t.Fatalf("click handled by a component that takes no mouse: %+v", result)
	}

	anonymous := newMouseRig(t, nil)
	_ = anonymous.component.Render(80)
	env := readFramed(t, anonymous.peer)
	var payload RenderComponentPayload
	if err := json.Unmarshal(env.Request.Args, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Key != "" {
		t.Fatalf("a renderer call without an identity carries key %q", payload.Key)
	}
	anonymous.reply(t, env, RenderResult{Lines: []string{"x"}, Mouse: true})
	waitForRenderProxy(t, func() bool { return anonymous.invalidated.Load() > 0 })
	if result := anonymous.component.HandleMouse(clickAt(1, 0)); result != nil {
		t.Fatalf("click handled for a call with no identity: %+v", result)
	}
}

// Pi runs the renderer when it builds the component and draws the same component on every other frame: only the first draw, a new expansion or
// padding and an invalidation ask the extension to run it.
func TestRenderProxyRerendersOnlyWhenPiBuildsTheComponent(t *testing.T) {
	rig := newMouseRig(t, extension.NewRenderIdentity())
	rig.shown(t, RenderResult{Lines: []string{"first"}})

	_ = rig.component.Render(100)
	env, resized := rig.request(t)
	if resized.Rerender {
		t.Fatal("a resize runs the renderer")
	}
	rig.reply(t, env, RenderResult{Lines: []string{"resized"}})
	waitForRenderProxy(t, func() bool { return rig.drawn(100) == "resized" })

	for name, change := range map[string]func(){
		"expansion":    func() { rig.component.SetExpanded(true) },
		"padding":      func() { rig.component.SetOutputPad(3) },
		"invalidation": func() { rig.component.Invalidate() },
	} {
		change()
		env, got := rig.request(t)
		if !got.Rerender {
			t.Fatalf("a new %s does not run the renderer", name)
		}
		rig.reply(t, env, RenderResult{Lines: []string{name}})
		waitForRenderProxy(t, func() bool { return rig.drawn(100) == name })
		_ = rig.component.Render(120)
		env, next := rig.request(t)
		if next.Rerender {
			t.Fatalf("a resize after the %s runs the renderer again", name)
		}
		rig.reply(t, env, RenderResult{Lines: []string{name + "-resized"}})
		waitForRenderProxy(t, func() bool { return rig.drawn(120) == name+"-resized" })
		_ = rig.component.Render(100)
		env, _ = rig.request(t)
		rig.reply(t, env, RenderResult{Lines: []string{name}})
		waitForRenderProxy(t, func() bool { return rig.drawn(100) == name })
	}
}

// A key stands for one identity on one connection for as long as the identity lives; when the transcript drops the message the extension is told to
// drop what it kept.
func TestRenderKeysNameIdentitiesAndReleaseThemWhenCollected(t *testing.T) {
	hostEnd, peer := net.Pipe()
	conn := NewConn("render-keys-test", hostEnd)
	ctx, cancel := context.WithCancel(context.Background())
	conn.Start(ctx)
	t.Cleanup(func() {
		cancel()
		_ = peer.Close()
		_ = hostEnd.Close()
		<-conn.done
	})

	first, second := extension.NewRenderIdentity(), extension.NewRenderIdentity()
	a, b := renderKeys.key(conn, first), renderKeys.key(conn, second)
	if a == "" || b == "" || a == b {
		t.Fatalf("keys %q and %q, want two distinct non-empty keys", a, b)
	}
	if again := renderKeys.key(conn, first); again != a {
		t.Fatalf("the same identity got key %q then %q", a, again)
	}
	if got := renderKeys.key(nil, first); got != "" {
		t.Fatalf("an identity without a connection got key %q", got)
	}
	if got := renderKeys.key(conn, nil); got != "" {
		t.Fatalf("no identity got key %q", got)
	}

	runtime.KeepAlive(second)
	released := make(chan string, 1)
	go func() {
		for {
			env, err := tryReadFramed(peer)
			if err != nil {
				return
			}
			if env.Notify != nil && env.Notify.Method == NotifyRenderRelease {
				var payload RenderReleasePayload
				_ = json.Unmarshal(env.Notify.Args, &payload)
				released <- payload.Key
				return
			}
		}
	}()
	deadline := time.After(10 * time.Second)
	for {
		runtime.GC()
		select {
		case key := <-released:
			if key != a {
				t.Fatalf("released key %q, want the collected identity's %q", key, a)
			}
			runtime.KeepAlive(second)
			return
		case <-deadline:
			t.Fatal("the extension was not told to release the collected identity")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// A click is answered from the view's description and costs the loop no IPC: the proxy returns while the extension has not read the request (a pipe
// nobody reads blocks a write), and the event waits for the request that carries it.
func TestRenderProxyClickNeverWaitsForIPC(t *testing.T) {
	rig := newMouseRig(t, extension.NewRenderIdentity())
	rig.shown(t, RenderResult{View: json.RawMessage(cardView)})
	done := make(chan *tui.TuiMouseDispatchResult, 1)
	go func() { done <- rig.component.HandleMouse(clickAt(2, 0)) }()
	select {
	case result := <-done:
		if result == nil || !result.Handled {
			t.Fatalf("click result = %+v, want handled", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the click waited for the extension")
	}
	_, next := rig.request(t)
	if len(next.Events) != 1 {
		t.Fatalf("the request after the click carries %d events, want 1", len(next.Events))
	}
}
