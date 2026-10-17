package subprocess

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// renderExtension is the extension side of a render proxy's connection: it
// answers heartbeats and hands every other frame the host sends to the test,
// which answers renders when it chooses.
type renderExtension struct {
	conn    *Conn
	peer    net.Conn
	frames  chan Envelope
	writeMu sync.Mutex
}

func startRenderExtension(t *testing.T) *renderExtension {
	t.Helper()
	hostEnd, peer := net.Pipe()
	conn := NewConn("render-retry", hostEnd)
	conn.Start(t.Context())
	ext := &renderExtension{conn: conn, peer: peer, frames: make(chan Envelope, 64)}
	go func() {
		for {
			var length [4]byte
			if _, err := io.ReadFull(peer, length[:]); err != nil {
				return
			}
			data := make([]byte, binary.BigEndian.Uint32(length[:]))
			if _, err := io.ReadFull(peer, data); err != nil {
				return
			}
			var env Envelope
			if err := json.Unmarshal(data, &env); err != nil {
				return
			}
			if env.Type == MsgPing && env.Ping != nil {
				ext.write(t, Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
				continue
			}
			ext.frames <- env
		}
	}()
	t.Cleanup(func() {
		_ = conn.Close("test done")
		_ = peer.Close()
		_ = hostEnd.Close()
	})
	return ext
}

func (e *renderExtension) write(t *testing.T, env Envelope) {
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	writeFramed(t, e.peer, env)
}

// answer replies to a render request with lines.
func (e *renderExtension) answer(t *testing.T, request Envelope, lines ...string) {
	t.Helper()
	e.respond(t, request, RenderResult{Lines: lines})
}

// respond replies to a request with result.
func (e *renderExtension) respond(t *testing.T, request Envelope, result any) {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	e.write(t, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: raw}})
}

// next returns the next frame the host sent, which must be of type kind.
func (e *renderExtension) next(t *testing.T, kind string) Envelope {
	t.Helper()
	select {
	case env := <-e.frames:
		if env.Type != kind {
			t.Fatalf("host sent %s %+v, want a %s", env.Type, env, kind)
		}
		return env
	case <-time.After(time.Minute):
		t.Fatalf("the host sent no %s", kind)
		return Envelope{}
	}
}

// sent reports how many frames the host sent that the test has not read.
func (e *renderExtension) sent() int {
	synctest.Wait()
	return len(e.frames)
}

// abandon lets the renderer inactivity boundary pass on request and reads the
// cancel the host sends when it gives up.
func (e *renderExtension) abandon(t *testing.T, request Envelope, inactivity time.Duration) {
	t.Helper()
	time.Sleep(inactivity)
	cancel := e.next(t, MsgCancel)
	if cancel.Cancel == nil || cancel.Cancel.RequestID != request.ID {
		t.Fatalf("cancel = %+v, want the cancel of %s", cancel, request.ID)
	}
}

// newRetryMessageProxy returns a message render proxy as the TUI mounts it:
// a component that reports a changed frame through IsDirty, so a container
// reuses its lines until then.
func newRetryMessageProxy(ext *renderExtension, inactivity time.Duration, invalidated *atomic.Int32) tui.Component {
	return newRenderProxyComponent("fixture", "notice",
		extension.CustomMessage{CustomType: "notice", Content: "body", Display: true}, extension.MessageRenderOptions{},
		ext.conn, inactivity, func() { invalidated.Add(1) })
}

// toolCard stands for the tool card around a tool render proxy: the card
// draws again when the proxy reports a changed frame
// (ToolExecutionComponent.definitionComponentsDirty), and a container reuses
// the card's lines otherwise.
type toolCard struct{ proxy *toolRenderProxy }

func (c toolCard) Render(width int) []string { return c.proxy.Render(width) }
func (c toolCard) Invalidate()               {}
func (c toolCard) IsDirty() bool             { return c.proxy.IsDirty() }
func (c toolCard) NeedsRedraw() bool         { return c.proxy.IsDirty() }

func newRetryToolProxy(ext *renderExtension, inactivity time.Duration, invalidated *atomic.Int32) tui.Component {
	proxy := &toolRenderProxy{
		session:    &toolRenderSession{conn: ext.conn},
		tool:       "card_tool",
		phase:      "call",
		inactivity: inactivity,
		repaint:    func() { invalidated.Add(1) },
	}
	proxy.update(RenderToolPayload{Args: json.RawMessage(`{}`)})
	return toolCard{proxy}
}

var retryProxyKinds = []struct {
	name   string
	method string
	make   func(*renderExtension, time.Duration, *atomic.Int32) tui.Component
}{
	{"message", "render_message", newRetryMessageProxy},
	{"tool", RequestRenderTool, newRetryToolProxy},
}

// Pi renders an extension's renderer synchronously in process, so its output
// always reaches the screen. When the renderer inactivity boundary (D56)
// abandons a render, the proxy keeps its fallback only until the extension
// answers: the next frame after the extension answered the abandoned render
// asks again, and the real rendering comes back. Frames go through a
// container, as the TUI draws them, so a retry that the proxy does not report
// as a changed frame never reaches the extension.
func TestRenderProxyRendersAgainOnceTheExtensionAnswersAnAbandonedRender(t *testing.T) {
	const inactivity = time.Second
	for _, kind := range retryProxyKinds {
		t.Run(kind.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ext := startRenderExtension(t)
				invalidated := &atomic.Int32{}
				proxy := tui.NewContainer(kind.make(ext, inactivity, invalidated))
				before := proxy.Render(80)
				first := ext.next(t, MsgRequest)
				if first.Request.Method != kind.method {
					t.Fatalf("first request %q, want %q", first.Request.Method, kind.method)
				}
				ext.abandon(t, first, inactivity)
				// The extension answers the abandoned render late; the host has given up on it.
				ext.answer(t, first, "late")
				synctest.Wait()
				if invalidated.Load() == 0 {
					t.Fatal("the proxy asked for no frame after the extension answered the abandoned render")
				}
				if got := proxy.Render(80); !slices.Equal(got, before) {
					t.Fatalf("frame after the late answer = %q, want the fallback %q until the extension renders again", got, before)
				}
				second := ext.next(t, MsgRequest)
				ext.answer(t, second, "rendered")
				synctest.Wait()
				if got := proxy.Render(80); !slices.Equal(got, []string{"rendered"}) {
					t.Fatalf("frame after the extension rendered again = %q, want [rendered]", got)
				}
				if n := ext.sent(); n != 0 {
					t.Fatalf("the host sent %d more frames after the render it needed", n)
				}
			})
		})
	}
}

// An extension that never answers gets one render per backoff, not one per
// frame, and never two at once: the backoff starts at the inactivity boundary
// and doubles with each abandonment in a row.
func TestRenderProxyBacksOffFromAnExtensionThatNeverAnswers(t *testing.T) {
	const inactivity = time.Second
	for _, kind := range retryProxyKinds {
		t.Run(kind.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ext := startRenderExtension(t)
				proxy := kind.make(ext, inactivity, &atomic.Int32{})
				// Frames call the proxy directly, with no container to absorb them, so the proxy itself must hold back.
				frames := func() {
					for range 100 {
						proxy.Render(80)
					}
				}
				frames()
				request := ext.next(t, MsgRequest)
				if n := ext.sent(); n != 0 {
					t.Fatalf("100 frames sent %d more requests while one was in flight", n)
				}
				for _, backoff := range []time.Duration{inactivity, 2 * inactivity, 4 * inactivity} {
					ext.abandon(t, request, inactivity)
					time.Sleep(backoff - time.Millisecond)
					frames()
					if n := ext.sent(); n != 0 {
						t.Fatalf("100 frames within the %s backoff sent %d requests", backoff, n)
					}
					time.Sleep(time.Millisecond)
					synctest.Wait()
					frames()
					request = ext.next(t, MsgRequest)
					if n := ext.sent(); n != 0 {
						t.Fatalf("100 frames after the %s backoff sent %d requests, want 1", backoff, n+1)
					}
				}
			})
		})
	}
}

// A renderer that always answers after the boundary renders again at once
// only after the first abandonment in a row; from the second on its late
// answers do not cut the backoff short, so it gets one request per backoff.
func TestRenderProxyBacksOffFromARendererThatAlwaysAnswersLate(t *testing.T) {
	const inactivity = time.Second
	const late = inactivity / 2
	for _, kind := range retryProxyKinds {
		t.Run(kind.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ext := startRenderExtension(t)
				proxy := kind.make(ext, inactivity, &atomic.Int32{})
				frames := func() {
					for range 100 {
						proxy.Render(80)
					}
				}
				frames()
				request := ext.next(t, MsgRequest)
				// First abandonment: its late answer makes the retry due at once.
				ext.abandon(t, request, inactivity)
				time.Sleep(late)
				ext.answer(t, request, "late")
				synctest.Wait()
				frames()
				request = ext.next(t, MsgRequest)
				// Second abandonment in a row: the late answer leaves the 2x backoff in place.
				ext.abandon(t, request, inactivity)
				time.Sleep(late)
				ext.answer(t, request, "late")
				synctest.Wait()
				frames()
				if n := ext.sent(); n != 0 {
					t.Fatalf("a late answer to the second abandonment in a row sent %d requests before the backoff", n)
				}
				time.Sleep(2*inactivity - late - time.Millisecond)
				frames()
				if n := ext.sent(); n != 0 {
					t.Fatalf("frames within the 2x backoff sent %d requests", n)
				}
				time.Sleep(time.Millisecond)
				synctest.Wait()
				frames()
				ext.next(t, MsgRequest)
				if n := ext.sent(); n != 0 {
					t.Fatalf("frames after the 2x backoff sent %d requests, want 1", n+1)
				}
			})
		})
	}
}

// A tool renderer resolution the boundary abandoned is not recorded as next(): once the extension answers the
// abandoned request, the host asks again with the same next(), the tool gets the renderers the extension's resolver
// returns, as it always does in Pi, and the cards are drawn again only for that changed answer.
func TestToolRendererResolutionAsksAgainAfterAnAbandonedRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ext := startRenderExtension(t)
		h, me, resolve := resolverFixture(t)
		me.conn.Store(ext.conn)
		var redraws atomic.Int32
		h.SetToolRenderersResolvedFunc(func(string) { redraws.Add(1) })
		base := &extension.ToolRenderers{}
		next := func() *extension.ToolRenderers { return base }
		own := ResolvedToolRenderers{Use: "own", ToolRenderersDecl: ToolRenderersDecl{RendersCall: true}}
		if got := resolve("bash", next); got != base {
			t.Fatalf("first resolution = %+v, want next() while the extension is asked", got)
		}
		first := ext.next(t, MsgRequest)
		if first.Request.Method != RequestResolveToolRenderers {
			t.Fatalf("request %q, want %q", first.Request.Method, RequestResolveToolRenderers)
		}
		ext.abandon(t, first, rendererInactivity)
		ext.respond(t, first, own)
		second := ext.next(t, MsgRequest)
		if second.Request.Method != RequestResolveToolRenderers || string(second.Request.Args) != string(first.Request.Args) {
			t.Fatalf("second request %s %s, want the first one's %s", second.Request.Method, second.Request.Args, first.Request.Args)
		}
		if got := resolve("bash", next); got != base {
			t.Fatalf("resolution while the extension is asked again = %+v, want next()", got)
		}
		if n := redraws.Load(); n != 0 {
			t.Fatalf("cards drawn again %d times before the extension answered, want 0", n)
		}
		ext.respond(t, second, own)
		synctest.Wait()
		if got := resolve("bash", next); got == base || got == nil || got.RenderCall == nil {
			t.Fatalf("resolution after the extension answered = %+v, want its own call renderer", got)
		}
		if n := redraws.Load(); n != 1 {
			t.Fatalf("cards drawn again %d times, want once for the changed answer", n)
		}
		if n := ext.sent(); n != 0 {
			t.Fatalf("the host sent %d more frames after the resolution it needed", n)
		}
	})
}

// The wait for a retry is a task of its connection: Close joins it, so a waiter never outlives the connection, and a connection that no longer
// admits tasks reports itself closed at once instead of leaving a goroutine nobody owns.
func TestRetryBackoffWaiterBelongsToItsConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ext := startRenderExtension(t)
		release := make(chan struct{})
		var due, closed atomic.Int32
		ext.conn.afterRetryBackoff(nil, time.Hour, func() { due.Add(1) }, func() { closed.Add(1); <-release })
		closeReturned := make(chan struct{})
		go func() { _ = ext.conn.Close("test"); close(closeReturned) }()
		synctest.Wait()
		select {
		case <-closeReturned:
			t.Fatal("Close returned while the retry waiter was still running: the waiter does not belong to the connection")
		default:
		}
		close(release)
		synctest.Wait()
		select {
		case <-closeReturned:
		default:
			t.Fatal("Close did not return once the retry waiter ended")
		}
		if due.Load() != 0 || closed.Load() != 1 {
			t.Fatalf("closing the connection ran due %d times and closed %d times, want 0 and 1", due.Load(), closed.Load())
		}
		ext.conn.afterRetryBackoff(nil, time.Hour, func() { due.Add(1) }, func() { closed.Add(1) })
		if due.Load() != 0 || closed.Load() != 2 {
			t.Fatalf("a closed connection ran due %d times and closed %d times, want 0 and 2", due.Load(), closed.Load())
		}
	})
}
