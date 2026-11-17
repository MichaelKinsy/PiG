package subprocess

import (
	"fmt"
	"net"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi runs a TuiAltScreen on one event loop: the terminal's input and the throttled frame are tasks of it, so a component's handleInput and its render never run
// at the same time, and a render never reads what a handler has half applied (tui.ts:995-997, 1067-1120). The extension's components run in another process
// that answers every request on a goroutine, thread or task of its own, so the host is what keeps the order: the screen's callbacks are sent by one goroutine,
// one at a time (tuiAltOwner). Before, the frame ran on the renderer's timer goroutine or the caller's and the input on the extension's call goroutine, and
// the extension was sent a render while it was still answering the input.

// orderPeer is the extension end of tui.alt screens. Each screen has one component whose state is two counters: input adds to the first, waits at the point a
// test chooses, then adds to the second; render reads both and reports a frame that saw one without the other (torn). The peer answers every request on a
// goroutine of its own and reports a callback that arrives while another of the same screen is unanswered.
type orderPeer struct {
	conn   net.Conn
	sendMu sync.Mutex

	mu       sync.Mutex
	inFlight map[string]int
	counters map[string][2]int
	park     map[string]string
	torn     bool
	events   []string

	// parked receives the callback that waits at the point the test chose; release lets every waiting callback go.
	parked      chan parkedCallback
	release     chan struct{}
	releaseOnce sync.Once
	// overlap is closed by the first callback that arrives while another one of its screen is unanswered.
	overlap     chan struct{}
	overlapOnce sync.Once
}

// parkedCallback is a callback the extension holds: the screen it belongs to and its request id, which a call the extension's code makes meanwhile names as its
// parent.
type parkedCallback struct{ screen, request string }

func newOrderPeer(conn net.Conn) *orderPeer {
	return &orderPeer{
		conn: conn, inFlight: map[string]int{}, counters: map[string][2]int{}, park: map[string]string{},
		parked: make(chan parkedCallback, 8), release: make(chan struct{}), overlap: make(chan struct{}),
	}
}

func (p *orderPeer) parkNext(id, kind string) {
	p.mu.Lock()
	p.park[id] = kind
	p.mu.Unlock()
}

func (p *orderPeer) releaseAll() { p.releaseOnce.Do(func() { close(p.release) }) }

func (p *orderPeer) serve() {
	for {
		env, err := readEnvelopeFrom(p.conn)
		if err != nil {
			return
		}
		if env.Type == MsgRequest && env.Request != nil {
			go p.answer(env)
		}
	}
}

func (p *orderPeer) answer(env *Envelope) {
	var args struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(env.Request.Args, &args); err != nil {
		return
	}
	result := `{}`
	switch args.Kind {
	case "render", "input":
		p.mu.Lock()
		if p.inFlight[args.ID] > 0 {
			p.overlapOnce.Do(func() { close(p.overlap) })
		}
		p.inFlight[args.ID]++
		p.events = append(p.events, args.ID+" "+args.Kind+" begin")
		state := p.counters[args.ID]
		if args.Kind == "input" {
			state[0]++
			p.counters[args.ID] = state
		}
		parks := p.park[args.ID] == args.Kind
		if parks {
			delete(p.park, args.ID)
		}
		p.mu.Unlock()
		if parks {
			p.parked <- parkedCallback{screen: args.ID, request: env.ID}
			<-p.release
		}
		p.mu.Lock()
		if args.Kind == "input" {
			state = p.counters[args.ID]
			state[1]++
			p.counters[args.ID] = state
		} else {
			state = p.counters[args.ID]
			if state[0] != state[1] {
				p.torn = true
			}
			result = fmt.Sprintf(`{"lines":["input %d/%d"]}`, state[0], state[1])
		}
		p.inFlight[args.ID]--
		p.events = append(p.events, args.ID+" "+args.Kind+" end")
		p.mu.Unlock()
	}
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	_ = writeEnvelopeTo(p.conn, &Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{Result: json.RawMessage(result)}})
}

// secondCall runs the call a test makes second, so that the probe finds the goroutine that makes it by name: no other goroutine of a test has it in its stack.
//
//go:noinline
func secondCall(do func()) { do() }

// secondIsHeld reports whether the goroutine inside secondCall waits for something of the host's (a lock, the owner's queue) and not for the extension's
// answer; present says some goroutine is inside secondCall and a tui.alt call at all, so a probe that names a function that was renamed fails as stale and not
// as an ordering.
func secondIsHeld() (held, present bool) {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	for goroutine := range strings.SplitSeq(string(buf), "\n\n") {
		if !strings.Contains(goroutine, "subprocess.secondCall(") || !strings.Contains(goroutine, "(*UIBridge).handleTuiAlt") {
			continue
		}
		present = true
		header, _, _ := strings.Cut(goroutine, "\n")
		waiting := strings.Contains(header, "[select") || strings.Contains(header, "[chan receive") || strings.Contains(header, "[sync.") || strings.Contains(header, "[semacquire")
		if waiting && !strings.Contains(goroutine, "(*Conn).request") {
			held = true
		}
	}
	return held, present
}

// secondOverlapsOrIsHeld waits until overlap is closed, the extension having been sent a second callback while the first is unanswered (it reports true), or
// the second call is held by the host (false): it waits for the screen and has not reached the extension. It reads goroutine states, not a clock.
func secondOverlapsOrIsHeld(t *testing.T, overlap <-chan struct{}) bool {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	seen := false
	for held := 0; time.Now().Before(deadline); runtime.Gosched() {
		select {
		case <-overlap:
			return true
		default:
		}
		// Two looks in a row: a call that only passes through a lock is not held by it.
		now, present := secondIsHeld()
		seen = seen || present
		if now {
			if held++; held == 2 {
				return false
			}
		} else {
			held = 0
		}
	}
	if !seen {
		t.Fatal("no goroutine is inside secondCall and (*UIBridge).handleTuiAlt: the probe is stale")
	}
	t.Fatal("the second call neither reached the extension nor waited for the screen")
	return false
}

// anOrderedScreen builds the screens of one connection, as the packed runtime cell serves several extensions through one, each with a focused component that
// takes input, started and drawn once.
func anOrderedScreen(t *testing.T, ids ...string) (b *UIBridge, conn *Conn, peer *orderPeer) {
	t.Helper()
	hostEnd, peerEnd := net.Pipe()
	conn = NewConn("alt-order", hostEnd)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("the test ends") })
	peer = newOrderPeer(peerEnd)
	t.Cleanup(peer.releaseAll)
	go peer.serve()
	b = &UIBridge{}
	t.Cleanup(func() { b.disposeTuiAlt(conn, false) })
	for _, id := range ids {
		for _, body := range []string{
			`{"op":"create","id":%q,"terminal":{"columns":40,"rows":6}}`,
			`{"op":"addChild","id":%q,"key":"c","caps":{"input":true}}`,
			`{"op":"setFocus","id":%q,"key":"c","caps":{"input":true}}`,
			`{"op":"start","id":%q}`,
			`{"op":"renderNow","id":%q}`,
		} {
			if _, err := b.handleTuiAlt(conn, "", json.RawMessage(fmt.Sprintf(body, id))); err != nil {
				t.Fatalf("tui.alt %s: %v", fmt.Sprintf(body, id), err)
			}
		}
	}
	return b, conn, peer
}

// A callback of one screen is answered before the next is sent. Each row parks the extension in the first callback, makes the second call, and the extension
// must not be sent the second callback until the first is answered. The rows run for one screen and for a screen beside another extension's on the same
// connection (a packed cell), whose own frame must not wait for the held one.
func TestAnExtensionScreensInputAndRenderNeverRunAtOnce(t *testing.T) {
	cases := []struct {
		name          string
		kind          string
		first, second string
	}{
		{"input while a frame renders", "render", `{"op":"renderNow","id":%q}`, `{"op":"input","id":%q,"data":"x"}`},
		{"input while a scheduled frame renders", "render", `{"op":"requestRender","id":%q}`, `{"op":"input","id":%q,"data":"x"}`},
		{"a frame while input is handled", "input", `{"op":"input","id":%q,"data":"x"}`, `{"op":"renderNow","id":%q}`},
	}
	for _, packed := range []bool{false, true} {
		mode := map[bool]string{false: "isolated", true: "packed"}[packed]
		for _, tc := range cases {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				ids := []string{"s"}
				if packed {
					ids = append(ids, "beside")
				}
				b, conn, peer := anOrderedScreen(t, ids...)
				call := func(body string) error {
					_, err := b.handleTuiAlt(conn, "", json.RawMessage(body))
					return err
				}
				peer.parkNext("s", tc.kind)
				errs := make(chan error, 2)
				go func() { errs <- call(fmt.Sprintf(tc.first, "s")) }()
				select {
				case parked := <-peer.parked:
					if parked.screen != "s" {
						t.Fatalf("the extension parked in screen %q, want %q", parked.screen, "s")
					}
				case <-time.After(testbudget.Wait(t)):
					t.Fatalf("the extension was never sent the %s callback the first call makes", tc.kind)
				}
				go secondCall(func() { errs <- call(fmt.Sprintf(tc.second, "s")) })
				if secondOverlapsOrIsHeld(t, peer.overlap) {
					peer.releaseAll()
					t.Fatalf("%s: the extension was sent a callback of the screen while it was still answering the %s callback of the first call", tc.name, tc.kind)
				}
				if packed {
					before := len(peer.snapshot())
					returnsWithinBudget(t, "a frame of the screen beside the held one", func() {
						if err := call(`{"op":"renderNow","id":"beside"}`); err != nil {
							t.Error(err)
						}
					})
					if got := peer.snapshot()[before:]; !slices.Equal(got, []string{"beside render begin", "beside render end"}) {
						t.Errorf("the screen beside the held one: events %q, want its own frame", got)
					}
				}
				peer.releaseAll()
				for range 2 {
					select {
					case err := <-errs:
						if err != nil {
							t.Errorf("tui.alt call: %v", err)
						}
					case <-time.After(testbudget.Wait(t)):
						t.Fatal("a call did not return once the extension answered")
					}
				}
				peer.mu.Lock()
				torn := peer.torn
				peer.mu.Unlock()
				if torn {
					t.Errorf("%s: a frame read the component between the two halves of its input", tc.name)
				}
			})
		}
	}
}

// snapshot returns the callbacks the component of every screen has begun and ended, in order.
func (p *orderPeer) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.events)
}
