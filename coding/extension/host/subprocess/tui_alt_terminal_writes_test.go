package subprocess

import (
	"net"
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
	t.Cleanup(func() { b.disposeTuiAlt(conn, false) })
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

// An extension's alternate screen that supplies no copySelection copies a selection with OSC 52 through the screen's terminal, as Pi's copyTextToClipboard does
// with terminal.write (tui-alt-screen.ts:1497). The extension's Terminal must see one output at a time, each acknowledged before the next, so a frame that
// comes while the copy is being written waits for it: the owner of the screen sends the copy and the frame, one after the other.
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
	requested := make(chan struct{})
	go secondCall(func() {
		defer close(requested)
		call(`{"op":"requestRender","id":"s","force":true}`)
	})
	if secondOverlapsOrIsHeld(t, peer.overlap) {
		t.Errorf("the extension's Terminal got a frame while the clipboard write was unanswered; writes %q", peer.written())
	}
	close(peer.release)
	<-copied
	<-requested
	call(`{"op":"stop","id":"s"}`)
	want := "\u001b]52;c;aGVsbG8=\a"
	if !slices.Contains(peer.written(), want) {
		t.Errorf("writes %q, want the clipboard write %q whole", peer.written(), want)
	}
}

// The stop of a screen shows the cursor before it leaves the alternate screen; a frame of the screen may be writing when the call comes, so the cursor write
// waits for the frame, as every write of the screen does.
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
	go secondCall(func() {
		defer close(stopped)
		call(`{"op":"stop","id":"s"}`)
	})
	if secondOverlapsOrIsHeld(t, peer.overlap) {
		t.Errorf("the extension's Terminal got the cursor write while a frame was unanswered; writes %q", peer.written())
	}
	close(peer.release)
	<-stopped
}
