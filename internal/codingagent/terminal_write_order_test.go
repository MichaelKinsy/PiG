package codingagent

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi writes every escape sequence it sends with terminal.write on its one event loop: a title, a clipboard copy or a progress keepalive is
// before the bytes of a frame or after them, never between two writes of one frame (terminal.ts write; tui-main-screen.ts doRender holds
// the loop until its last write). A goroutine other than the owner loop (an extension's host call) has to hand its write to the loop. The
// tests below hold a frame between two of its writes, make the other goroutine's write, and read the order the terminal received.

// orderedOut is the terminal's output: the writes it received, in order.
type orderedOut struct {
	mu     sync.Mutex
	writes []string
}

func (o *orderedOut) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.writes = append(o.writes, string(p))
	return len(p), nil
}

func (o *orderedOut) received() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.writes...)
}

// parkingTerminal is the process terminal over a recorder that holds the goroutine of a frame, once its write that contains marker is out and
// the terminal's own write lock is free: the frame is between two of its writes.
type parkingTerminal struct {
	*tui.ProcessTerminal
	marker  string
	parked  chan struct{}
	release chan struct{}
	taken   atomic.Bool
}

func (*parkingTerminal) Columns() int { return 100 }
func (*parkingTerminal) Rows() int    { return 30 }

func (p *parkingTerminal) Write(data string) {
	p.ProcessTerminal.Write(data)
	if strings.Contains(data, p.marker) && p.taken.CompareAndSwap(false, true) {
		close(p.parked)
		<-p.release
	}
}

type frameLines []string

func (c frameLines) Render(int) []string { return c }
func (frameLines) Invalidate()           {}

// frameBetweenWrites is a mode whose owner loop is a goroutine that renders one frame and parks between two of the frame's writes.
type frameBetweenWrites struct {
	m      *InteractiveMode
	out    *orderedOut
	term   *parkingTerminal
	finish chan struct{}
}

const frameMarker = "FRAME-BODY"

// parkAFrame renders a frame on a goroutine that is the owner loop of m and returns once the frame is between two writes. finish lets the frame
// end, runs on that goroutine what other goroutines handed to the loop, as the loop does once it is back, and returns the writes in order.
func parkAFrame(t *testing.T) *frameBetweenWrites {
	t.Helper()
	out := &orderedOut{}
	term := &parkingTerminal{
		ProcessTerminal: tui.NewProcessTerminalWithOutput(nil, nil, out),
		marker:          frameMarker,
		parked:          make(chan struct{}),
		release:         make(chan struct{}),
	}
	ui := tui.NewTuiMainScreen(term, nil, "")
	ui.AddChild(frameLines{frameMarker})
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work"})
	m.tuiInst = ui
	m.runCtx = t.Context()
	f := &frameBetweenWrites{m: m, out: out, term: term, finish: make(chan struct{})}
	ownerUp := make(chan struct{})
	go func() {
		defer close(f.finish)
		leave := m.enterOwnerLoop()
		defer leave()
		close(ownerUp)
		ui.Render()
		for {
			select {
			case fn := <-m.uiTaskCh:
				fn()
			default:
				return
			}
		}
	}()
	<-ownerUp
	<-term.parked
	return f
}

// end releases the frame and returns the writes the terminal received once the owner loop is done.
func (f *frameBetweenWrites) end() []string {
	close(f.term.release)
	<-f.finish
	return f.out.received()
}

// requireWrittenAfterTheFrame fails when a write that contains probe is not the terminal's last write: a write of the frame came after it.
func requireWrittenAfterTheFrame(t *testing.T, writes []string, probe string) {
	t.Helper()
	if len(writes) == 0 || !strings.Contains(writes[len(writes)-1], probe) {
		t.Fatalf("the terminal received %q: the write of %q is not after the last write of the frame", writes, probe)
	}
	for _, write := range writes {
		if strings.Contains(write, probe) {
			return
		}
	}
	t.Fatalf("the terminal received %q: no write of %q", writes, probe)
}

// An extension's ctx.ui.setTitle runs on the extension host's goroutine, not on the owner loop: its title is written by the loop, after the frame.
func TestExtensionTitleFromAHostGoroutineIsWrittenBetweenFrames(t *testing.T) {
	f := parkAFrame(t)
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		(&ExtUIContext{m: f.m}).SetTitle("from the extension")
	}()
	<-returned
	requireWrittenAfterTheFrame(t, f.end(), "\x1b]0;from the extension\x07")
}

// The same holds for the session name an extension sets: its title is the session's title, written by the extension's action.
func TestExtensionSessionNameTitleFromAHostGoroutineIsWrittenBetweenFrames(t *testing.T) {
	f := parkAFrame(t)
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		f.m.setTerminalTitle("a session name")
	}()
	<-returned
	requireWrittenAfterTheFrame(t, f.end(), "\x1b]0;a session name\x07")
}

// An extension's copyToClipboard runs on the host's goroutine; its OSC 52 sequence is written by the loop, after the frame.
func TestExtensionClipboardOSC52FromAHostGoroutineIsWrittenBetweenFrames(t *testing.T) {
	f := parkAFrame(t)
	remove := f.m.orderClipboardWrites()
	defer remove()
	copier := hostClipboardCopier()
	copier.stdout = f.out
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		copier.emitOSC52("copied text")
	}()
	<-returned
	requireWrittenAfterTheFrame(t, f.end(), "\x1b]52;c;")
}

// A copy the owner loop makes (the /copy command, a selection) is written where it is made: the loop is between frames, and a task it queued for
// itself would wait for the loop's next turn.
func TestOwnerLoopClipboardOSC52IsWrittenWhereItIsMade(t *testing.T) {
	out := &orderedOut{}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work"})
	m.runCtx = t.Context()
	remove := m.orderClipboardWrites()
	defer remove()
	copier := hostClipboardCopier()
	copier.stdout = out
	leave := m.enterOwnerLoop()
	defer leave()
	copier.emitOSC52("copied text")
	if writes := out.received(); len(writes) != 1 || !strings.HasPrefix(writes[0], "\x1b]52;c;") {
		t.Fatalf("the terminal received %q, want the OSC 52 sequence at once", writes)
	}
}
