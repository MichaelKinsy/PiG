package subprocess

import (
	"net"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// terminalWritePeer is the extension end of a tui.alt screen: it answers the screen's tui.alt.cb requests, serves one component that renders "hello world",
// and keeps the order of the terminal writes. It parks the write that contains park, and closes overlap when a terminal write arrives while another is
// unanswered: the host promises the extension's Terminal that each write is acknowledged before the next (tuiAltTerminal).
type terminalWritePeer struct {
	conn    net.Conn
	sendMu  sync.Mutex
	open    atomic.Int32
	once    sync.Once
	overlap chan struct{}
	parked  chan struct{}
	release chan struct{}
	mu      sync.Mutex
	park    string
	writes  []string
}

func newTerminalWritePeer(conn net.Conn) *terminalWritePeer {
	return &terminalWritePeer{conn: conn, overlap: make(chan struct{}), parked: make(chan struct{}), release: make(chan struct{})}
}

func (p *terminalWritePeer) parkAt(marker string) {
	p.mu.Lock()
	p.park = marker
	p.mu.Unlock()
}

func (p *terminalWritePeer) written() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.writes)
}

func (p *terminalWritePeer) serve() {
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

func (p *terminalWritePeer) answer(env *Envelope) {
	var args struct {
		Kind   string `json:"kind"`
		Method string `json:"method"`
		Data   string `json:"data"`
	}
	if err := json.Unmarshal(env.Request.Args, &args); err != nil {
		return
	}
	result := `{}`
	switch {
	case args.Kind == "render":
		result = `{"lines":["hello world"]}`
	case args.Kind == "terminal" && args.Method != "start" && args.Method != "stop" && args.Method != "drainInput":
		if p.open.Add(1) > 1 {
			p.once.Do(func() { close(p.overlap) })
		}
		p.mu.Lock()
		if args.Method == "write" {
			p.writes = append(p.writes, args.Data)
		}
		park := p.park != "" && strings.Contains(args.Data, p.park)
		if park {
			p.park = ""
		}
		p.mu.Unlock()
		if park {
			close(p.parked)
			<-p.release
		}
		p.open.Add(-1)
	}
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	_ = writeEnvelopeTo(p.conn, &Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{Result: json.RawMessage(result)}})
}

// goroutineWaitsForALock reports whether a goroutine whose stack holds frame is parked on a lock of package sync (waits), and whether any goroutine holds frame
// at all (present): a frame that is on no goroutine is a stale name, not an ordering.
func goroutineWaitsForALock(frame string) (waits, present bool) {
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
		if !strings.Contains(goroutine, frame) {
			continue
		}
		present = true
		header, _, _ := strings.Cut(goroutine, "\n")
		if strings.Contains(header, "[sync.") || strings.Contains(header, "[semacquire") {
			waits = true
		}
	}
	return waits, present
}

// anExtensionScreen is a started tui.alt screen with no copySelection, as the extension host builds it, and the extension end it talks to.
func anExtensionScreen(t *testing.T) (call func(body string), peer *terminalWritePeer) {
	t.Helper()
	hostEnd, peerEnd := net.Pipe()
	conn := NewConn("alt-writes", hostEnd)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("the test ends") })
	peer = newTerminalWritePeer(peerEnd)
	go peer.serve()
	b := &UIBridge{}
	call = func(body string) {
		t.Helper()
		if _, err := b.handleTuiAlt(conn, "", json.RawMessage(body)); err != nil {
			t.Fatalf("tui.alt %s: %v", body, err)
		}
	}
	call(`{"op":"create","id":"s","terminal":{"columns":40,"rows":6}}`)
	call(`{"op":"addChild","id":"s","key":"c","caps":{}}`)
	call(`{"op":"start","id":"s"}`)
	call(`{"op":"renderNow","id":"s"}`)
	return call, peer
}

// secondWaitsOrWrites waits until the extension has seen a second output while one is unanswered (it reports true) or the goroutine that made the second call
// is parked on a lock of the screen, holding frame in its stack (false). It reads goroutine states, not a clock. frame names the function that performs the
// terminal write of a frame; if no goroutine ever holds it, the test fails as stale (the function was renamed), which is not the ordering failure.
func secondWaitsOrWrites(t *testing.T, peer *terminalWritePeer, frame string) bool {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	seen := false
	for waits := 0; time.Now().Before(deadline); runtime.Gosched() {
		select {
		case <-peer.overlap:
			return true
		default:
		}
		// Two looks in a row: a lock that is only passed through is not the one the goroutine waits for.
		waiting, present := goroutineWaitsForALock(frame)
		seen = seen || present
		if waiting {
			if waits++; waits == 2 {
				return false
			}
		} else {
			waits = 0
		}
	}
	if !seen {
		t.Fatalf("the frame function %s was not found on any goroutine: the probe is stale", frame)
	}
	t.Fatal("the second goroutine neither wrote nor waited for a lock within a minute")
	return false
}

// An extension's alternate screen that supplies no copySelection copies a selection with OSC 52 through the screen's terminal, as Pi's copyTextToClipboard does
// with terminal.write (tui-alt-screen.ts:1497). The copy runs on the goroutine of the extension's input call, the frames on the screen's timer goroutine; the
// extension's Terminal must see one output at a time, each acknowledged before the next, so a frame that comes while the copy is being written waits for it.
func TestAnExtensionScreenWithoutCopySelectionWritesTheClipboardBeforeTheNextFrame(t *testing.T) {
	call, peer := anExtensionScreen(t)
	call(`{"op":"input","id":"s","data":"\u001b[<0;1;1M"}`)
	call(`{"op":"input","id":"s","data":"\u001b[<32;5;1M"}`)
	peer.parkAt("\u001b]52;c;")
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		call(`{"op":"input","id":"s","data":"\u001b[<0;5;1m"}`)
	}()
	select {
	case <-peer.parked:
	case <-time.After(time.Minute):
		t.Fatalf("the release of the drag did not copy: writes %q", peer.written())
	}
	call(`{"op":"requestRender","id":"s","force":true}`)
	if secondWaitsOrWrites(t, peer, "tui.(*TuiAltScreen).drawFrame") {
		t.Errorf("the extension's Terminal got a frame while the clipboard write was unanswered; writes %q", peer.written())
	}
	close(peer.release)
	<-copied
	call(`{"op":"stop","id":"s"}`)
	want := "\u001b]52;c;aGVsbG8=\a"
	if !slices.Contains(peer.written(), want) {
		t.Errorf("writes %q, want the clipboard write %q whole", peer.written(), want)
	}
}

// The stop of a screen shows the cursor before it leaves the alternate screen; the call runs on the extension's goroutine while a frame of the screen's timer
// may be writing, so the cursor write waits for the frame, as every write of the screen does.
func TestAnExtensionScreenShowsTheCursorAtStopAfterTheFrameThatIsWriting(t *testing.T) {
	call, peer := anExtensionScreen(t)
	peer.parkAt("\u001b[?2026h")
	call(`{"op":"requestRender","id":"s","force":true}`)
	select {
	case <-peer.parked:
	case <-time.After(time.Minute):
		t.Fatalf("no frame was written: writes %q", peer.written())
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		call(`{"op":"stop","id":"s"}`)
	}()
	if secondWaitsOrWrites(t, peer, "subprocess.(*tuiAltScreenState).run") {
		t.Errorf("the extension's Terminal got the cursor write while a frame was unanswered; writes %q", peer.written())
	}
	close(peer.release)
	<-stopped
}
