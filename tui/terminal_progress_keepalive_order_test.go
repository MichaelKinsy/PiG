package tui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Pi's progress keepalive is a setInterval callback (terminal.ts setProgress): it runs on the one event loop, between the renders of the frame
// loop, so its OSC 9;4 sequence is before the bytes of a frame or after them. A ticker goroutine writes it between the writes of a frame unless
// the renderer's owner dispatcher, the seam for work that belongs on that loop, takes the tick.

// keepaliveOut records the writes the terminal received, and tells when a progress sequence arrives while the frame is parked.
type keepaliveOut struct {
	mu      sync.Mutex
	writes  []string
	harness *keepaliveHarness
}

func (o *keepaliveOut) Write(p []byte) (int, error) {
	o.mu.Lock()
	o.writes = append(o.writes, string(p))
	o.mu.Unlock()
	if strings.Contains(string(p), terminalProgressActiveSeq) && o.harness.term.parkedNow.Load() {
		o.harness.tickWhileParked()
	}
	return len(p), nil
}

func (o *keepaliveOut) received() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.writes...)
}

// keepaliveParkingTerminal parks the goroutine of a frame after the write that contains the frame body, with the terminal's write lock free.
type keepaliveParkingTerminal struct {
	*ProcessTerminal
	parked    chan struct{}
	release   chan struct{}
	parkedNow atomic.Bool
	taken     atomic.Bool
}

func (*keepaliveParkingTerminal) Columns() int { return 100 }
func (*keepaliveParkingTerminal) Rows() int    { return 30 }

const keepaliveFrameBody = "KEEPALIVE-FRAME-BODY"

func (p *keepaliveParkingTerminal) Write(data string) {
	p.ProcessTerminal.Write(data)
	if strings.Contains(data, keepaliveFrameBody) && p.taken.CompareAndSwap(false, true) {
		p.parkedNow.Store(true)
		close(p.parked)
		<-p.release
		p.parkedNow.Store(false)
	}
}

type keepaliveLines []string

func (c keepaliveLines) Render(int) []string { return c }
func (keepaliveLines) Invalidate()           {}

type keepaliveHarness struct {
	out    *keepaliveOut
	term   *keepaliveParkingTerminal
	ui     *TuiMainScreen
	tick   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	queued []func()
}

func (h *keepaliveHarness) tickWhileParked() { h.once.Do(func() { close(h.tick) }) }

func TestProgressKeepaliveIsBeforeAFrameOrAfterIt(t *testing.T) {
	h := &keepaliveHarness{tick: make(chan struct{})}
	h.out = &keepaliveOut{harness: h}
	h.term = &keepaliveParkingTerminal{ProcessTerminal: NewProcessTerminalWithOutput(nil, nil, h.out), parked: make(chan struct{}), release: make(chan struct{})}
	h.term.progressKeepalive = time.Millisecond
	h.ui = NewTuiMainScreen(h.term, nil, "")
	h.ui.AddChild(keepaliveLines{keepaliveFrameBody})
	// The owner loop of the test: it takes the keepalive's write and runs it when the frame is out.
	h.ui.SetOwnerDispatcher(func(ctx context.Context, fn func()) error {
		h.mu.Lock()
		h.queued = append(h.queued, fn)
		h.mu.Unlock()
		if h.term.parkedNow.Load() {
			h.tickWhileParked()
		}
		return nil
	})
	h.term.SetProgress(true)
	defer h.term.SetProgress(false)

	frame := make(chan struct{})
	go func() {
		defer close(frame)
		h.ui.Render()
	}()
	<-h.term.parked
	// A keepalive tick while the frame is parked is the interleaving: it is written now, or handed to the loop.
	<-h.tick
	close(h.term.release)
	<-frame

	writes := h.out.received()
	body := -1
	for i, write := range writes {
		if strings.Contains(write, keepaliveFrameBody) {
			body = i
		}
	}
	if body < 0 || body+1 >= len(writes) {
		t.Fatalf("the terminal received %q, want the frame's body and its cursor write after it", writes)
	}
	if strings.Contains(writes[body+1], "\x1b]9;4;") {
		t.Fatalf("the terminal received %q: the keepalive's sequence is between two writes of the frame", writes)
	}
	// The tick the loop took is not lost: the loop writes it once the frame is out.
	h.mu.Lock()
	queued := h.queued
	h.mu.Unlock()
	if len(queued) == 0 {
		t.Fatal("the owner dispatcher took no keepalive tick")
	}
	queued[0]()
	if after := h.out.received(); !strings.Contains(after[len(after)-1], terminalProgressActiveSeq) {
		t.Fatalf("the terminal received %q, want the keepalive's sequence after the frame", after)
	}
}
