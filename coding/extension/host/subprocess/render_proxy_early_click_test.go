package subprocess

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

// heardClick is a click the extension heard of: the kit node that took it ("" for a Node component) and its cell, local to the component.
type heardClick struct {
	node string
	x, y int
}

// held draws the proxy, which starts its first request, and returns that request unanswered: the proxy paints its placeholder and knows no region of
// the component the extension is about to draw.
func (r *mouseRig) held(t *testing.T) Envelope {
	t.Helper()
	if got := r.drawn(80); got != "[card]" {
		t.Fatalf("a proxy without an answer draws %q, want its placeholder", got)
	}
	env, first := r.request(t)
	if first.Key == "" || !first.Rerender || len(first.Events) != 0 {
		t.Fatalf("first request key=%q rerender=%t events=%d, want a key, rerender and no events", first.Key, first.Rerender, len(first.Events))
	}
	return env
}

// heardClicks answers the proxy's requests with result until they have carried n clicks, and returns the clicks in the order they came. A proxy that
// took no click sends no request; the read then ends at the hang budget and fails the test.
func (r *mouseRig) heardClicks(t *testing.T, n int, result RenderResult) []heardClick {
	t.Helper()
	var heard []heardClick
	for len(heard) < n {
		if err := r.peer.SetReadDeadline(time.Now().Add(testbudget.Wait(t))); err != nil {
			t.Fatal(err)
		}
		env, err := tryReadFramed(r.peer)
		if err != nil {
			t.Fatalf("the proxy sent no request after %d of %d clicks (%v): a click made on its placeholder reached no component", len(heard), n, err)
		}
		if err := r.peer.SetReadDeadline(time.Time{}); err != nil {
			t.Fatal(err)
		}
		if env.Request == nil || env.Request.Method != "render_message" {
			t.Fatalf("request = %+v", env)
		}
		var payload RenderComponentPayload
		if err := json.Unmarshal(env.Request.Args, &payload); err != nil {
			t.Fatal(err)
		}
		for _, event := range payload.Events {
			if event.Type != ViewEventMouse || event.Mouse == nil || event.Mouse.Type != "click" {
				t.Fatalf("event = %+v, want a click", event)
			}
			heard = append(heard, heardClick{node: event.Node, x: event.Mouse.X, y: event.Mouse.Y})
		}
		r.reply(t, env, result)
	}
	return heard
}

// settled waits until the request the test has just answered, or whose connection it has just closed, is over, and then checks idle.
func (r *mouseRig) settled(t *testing.T, what string) {
	t.Helper()
	waitForRenderProxy(t, func() bool {
		r.component.mu.Lock()
		defer r.component.mu.Unlock()
		return !r.component.requesting
	})
	r.idle(t, what)
}

// idle fails when the proxy has a request in flight or a mouse event queued for one. It is read at once, at a point where a proxy that gave a click
// to a component has already asked for the request that carries it (a click is queued, and its request asked for, before the frame or the
// invalidation that the test has just seen returns).
func (r *mouseRig) idle(t *testing.T, what string) {
	t.Helper()
	r.component.mu.Lock()
	requesting, queued := r.component.requesting, len(r.component.events)
	r.component.mu.Unlock()
	if requesting || queued != 0 {
		t.Fatalf("%s gave a click to a component (request in flight: %t, mouse events queued: %d)", what, requesting, queued)
	}
}

// Pi runs a message or entry renderer inside the render that builds the component (custom-message.ts and custom-entry.ts rebuild), so a card is on
// Pi's screen together with its mouse regions and takes a click from its first frame. A proxy paints its placeholder from its first request until the
// frame that follows the extension's answer. Every click made in that time is answered unhandled, as a click outside every region is, and the frame
// that paints the component gives the clicks to it: the extension hears of each once, in the order they came and before any later click, and of no
// click outside the component's regions. Two clicks on one cell are two clicks.
func TestRenderProxyClickBeforeTheFirstFrameReachesTheDrawnComponent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result RenderResult
		clicks [][2]int
		late   [2]int
		want   []heardClick
	}{
		{
			name:   "node mouse regions",
			result: RenderResult{Lines: []string{"   card"}, Regions: []RenderMouseRegion{{X: 3, Y: 0, Width: 4, Height: 1}}},
			clicks: [][2]int{{5, 0}, {1, 0}, {3, 0}},
			late:   [2]int{4, 0},
			want:   []heardClick{{x: 5, y: 0}, {x: 3, y: 0}, {x: 4, y: 0}},
		},
		{
			name:   "node component that handles the mouse itself",
			result: RenderResult{Lines: []string{"card"}, Mouse: true},
			clicks: [][2]int{{2, 0}, {2, 0}},
			late:   [2]int{7, 0},
			want:   []heardClick{{x: 2, y: 0}, {x: 2, y: 0}, {x: 7, y: 0}},
		},
		{
			name:   "kit view mouse region",
			result: RenderResult{View: json.RawMessage(cardView)},
			clicks: [][2]int{{2, 0}, {4, 0}},
			late:   [2]int{1, 0},
			want:   []heardClick{{node: "card", x: 2, y: 0}, {node: "card", x: 4, y: 0}, {node: "card", x: 1, y: 0}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newMouseRig(t, extension.NewRenderIdentity())
			env := rig.held(t)
			for _, at := range tc.clicks {
				if result := rig.component.HandleMouse(clickAt(at[0], at[1])); result != nil {
					t.Fatalf("click at %v on the placeholder answered %+v, want unhandled: no region is known yet", at, result)
				}
			}
			rig.reply(t, env, tc.result)
			waitForRenderProxy(t, func() bool { return rig.invalidated.Load() > 0 })
			// The answer is applied and its frame is not drawn: the screen still shows the placeholder, and a click now waits behind the others.
			if result := rig.component.HandleMouse(clickAt(tc.late[0], tc.late[1])); result != nil {
				t.Errorf("click at %v before the component's frame answered %+v, want unhandled: it waits behind the clicks made before it", tc.late, result)
			}
			rig.component.Render(80)
			if got := rig.heardClicks(t, len(tc.want), tc.result); !slices.Equal(got, tc.want) {
				t.Fatalf("the extension heard of %+v, want %+v", got, tc.want)
			}
			rig.settled(t, "the card that took the clicks made on its placeholder")
			if result := rig.component.HandleMouse(clickAt(tc.want[0].x, 0)); result == nil || !result.Handled {
				t.Fatalf("click on the drawn card answered %+v, want handled: nothing waits any more", result)
			}
		})
	}
}

// A first request the extension fails draws no component, so the clicks made on its placeholder have nothing to reach: they are dropped. The drawing
// a later invalidation asks for hears of none, and the frame that paints its card asks for nothing more.
func TestRenderProxyClickBeforeAFailedFirstAnswerIsDropped(t *testing.T) {
	rig := newMouseRig(t, extension.NewRenderIdentity())
	env := rig.held(t)
	if result := rig.component.HandleMouse(clickAt(2, 0)); result != nil {
		t.Fatalf("click on the placeholder answered %+v, want unhandled", result)
	}
	writeFramed(t, rig.peer, Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{Error: &ErrorInfo{Message: "renderer threw"}}})
	rig.settled(t, "the proxy whose first request failed")
	rig.component.Invalidate()
	env, again := rig.request(t)
	if len(again.Events) != 0 {
		t.Fatalf("the drawing after a failed first request carries %+v, want no event", again.Events)
	}
	rig.reply(t, env, RenderResult{Lines: []string{"card"}, Regions: []RenderMouseRegion{{X: 0, Y: 0, Width: 4, Height: 1}}})
	waitForRenderProxy(t, func() bool { return rig.invalidated.Load() > 0 })
	rig.component.Render(80)
	rig.idle(t, "the card drawn after a failed first request")
}

// A held click belongs to the proxy that took it and is given to a component only by the frame that paints that proxy. A proxy that is not drawn
// again gives it to none: its message was unmounted, the transcript was rebuilt (a session switch builds a new proxy for the same message), or the
// extension was reloaded and the connection of the old generation ended.
func TestRenderProxyHeldClickDiesWithItsProxy(t *testing.T) {
	card := RenderResult{Lines: []string{"card"}, Regions: []RenderMouseRegion{{X: 0, Y: 0, Width: 4, Height: 1}}}
	t.Run("message unmounted", func(t *testing.T) {
		rig := newMouseRig(t, extension.NewRenderIdentity())
		env := rig.held(t)
		rig.component.HandleMouse(clickAt(2, 0))
		rig.reply(t, env, card)
		waitForRenderProxy(t, func() bool { return rig.invalidated.Load() > 0 })
		rig.idle(t, "the proxy that is not drawn again")
	})
	t.Run("transcript rebuilt for the same message", func(t *testing.T) {
		rig := newMouseRig(t, extension.NewRenderIdentity())
		env := rig.held(t)
		rig.component.HandleMouse(clickAt(2, 0))
		repainted := &atomic.Int32{}
		successor := &mouseRig{component: newRenderProxyComponent("fixture", "card", extension.CustomMessage{CustomType: "card", Content: "body", Display: true},
			extension.MessageRenderOptions{Identity: rig.identity}, rig.component.conn, time.Second, func() { repainted.Add(1) })}
		successor.component.Render(80)
		next, first := rig.request(t)
		if len(first.Events) != 0 {
			t.Fatalf("the first request of the rebuilt transcript's proxy carries %+v, want no event", first.Events)
		}
		rig.reply(t, env, card)
		rig.reply(t, next, card)
		waitForRenderProxy(t, func() bool { return rig.invalidated.Load() > 0 && repainted.Load() > 0 })
		successor.component.Render(80)
		rig.idle(t, "the replaced proxy")
		successor.idle(t, "the proxy of the rebuilt transcript")
	})
	t.Run("extension reloaded", func(t *testing.T) {
		rig := newMouseRig(t, extension.NewRenderIdentity())
		rig.held(t)
		rig.component.HandleMouse(clickAt(2, 0))
		if err := rig.peer.Close(); err != nil {
			t.Fatal(err)
		}
		rig.settled(t, "the proxy whose extension is gone")
		if got := rig.drawn(80); got != "[card]" {
			t.Fatalf("the proxy whose extension is gone draws %q, want its placeholder", got)
		}
		rig.idle(t, "the proxy whose extension is gone, drawn again")
	})
}

// The click pair of one terminal read. Pi's StdinBuffer emits every sequence of a read before a frame is drawn (tui.ts requestRender leaves the frame
// to a later turn of the event loop), and each click is resolved on the layout of the last frame drawn (tui-alt-screen.ts dispatchMouseToLayout
// reads currentLayout), so neither click is lost and the second lands where the screen showed its target. Here the first click opens a summary above
// two cards, which moves them down a row in the next frame, and the second lands on the row of the second card while both cards are placeholders
// whose first requests are in flight. The summary opens, and the second card, not the first, hears of the click once its frame is drawn.
func TestClickPairOfOneReadReachesTheCardBehindItsPlaceholder(t *testing.T) {
	screen := tui.NewTuiAltScreenWithOutput(io.Discard, 40, 12, tui.TuiAltScreenOptions{})
	t.Cleanup(screen.Stop)
	// A frame runs only where the test draws one: the owner loop draws none between the sequences of a read.
	screen.SetRenderDispatcher(func(func()) {})
	open := false
	summary := tui.NewMouseRegion(&frameProbePart{render: func(int) []string {
		if open {
			return []string{"summary", "body"}
		}
		return []string{"summary"}
	}}, func(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
		if event.Type != tui.MouseClick {
			return nil
		}
		open = !open
		return &tui.TuiMouseEventResult{Handled: true}
	})
	first, second := newMouseRig(t, extension.NewRenderIdentity()), newMouseRig(t, extension.NewRenderIdentity())
	screen.Add(summary)
	screen.Add(first.component)
	screen.Add(second.component)
	screen.Start()
	screen.Render()
	firstEnv, _ := first.request(t)
	secondEnv, _ := second.request(t)
	rows := map[string][]int{}
	for row, line := range screen.GetScreenLines() {
		for _, text := range []string{"summary", "[card]"} {
			if strings.Contains(line, text) {
				rows[text] = append(rows[text], row)
			}
		}
	}
	if len(rows["summary"]) != 1 || len(rows["[card]"]) != 2 {
		t.Fatalf("the frame with both first requests in flight draws the summary on rows %v and placeholders on rows %v, want one row and two", rows["summary"], rows["[card]"])
	}
	click := func(row int) string { return fmt.Sprintf("\x1b[<0;3;%dM\x1b[<0;3;%dm", row+1, row+1) }
	input := tui.NewTerminalInput(func(sequence string) { screen.HandleTerminalInput(sequence) })
	t.Cleanup(input.Close)
	input.Process([]byte(click(rows["summary"][0]) + click(rows["[card]"][1])))
	if !open {
		t.Fatal("the first click of the read did not open the summary")
	}
	card := RenderResult{Lines: []string{"card"}, Regions: []RenderMouseRegion{{X: 0, Y: 0, Width: 4, Height: 1}}}
	first.reply(t, firstEnv, card)
	second.reply(t, secondEnv, card)
	waitForRenderProxy(t, func() bool { return first.invalidated.Load() > 0 && second.invalidated.Load() > 0 })
	screen.Render()
	if got, want := second.heardClicks(t, 1, card), []heardClick{{x: 2, y: 0}}; !slices.Equal(got, want) {
		t.Fatalf("the second card heard of %+v, want %+v: the click landed on its placeholder", got, want)
	}
	first.idle(t, "the first card, where the click would land once the summary has grown,")
}

// The mouse answers of a drawn card allocate nothing: a press, which a card leaves to text selection, and a click outside every region.
func BenchmarkRenderProxyMouseUnhandled(b *testing.B) {
	component := &renderProxyComponent{key: "render-bench", conn: &Conn{}}
	component.regions.Store(&[]RenderMouseRegion{{X: 3, Y: 0, Width: 4, Height: 1}})
	for _, tc := range []struct {
		name  string
		event tui.TuiMouseEvent
	}{
		{"press", tui.TuiMouseEvent{Type: tui.MousePress, Button: tui.MouseButtonLeft, X: 5, Width: 80, Height: 1}},
		{"click outside the regions", clickAt(1, 0)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if result := component.HandleMouse(tc.event); result != nil {
					b.Fatalf("answered %+v, want unhandled", result)
				}
			}
		})
	}
}
