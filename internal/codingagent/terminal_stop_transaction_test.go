package codingagent

import (
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's stop() is one synchronous call (tui.ts:1046-1056): nothing writes inside it. A write that a callback of Pi's loop made before the stop began was
// written before it; one the loop runs after it (a quit by keys awaits the runtime's dispose, :4506) is written after it; after a signal's stop()
// process.exit(0) (:4491-4494) ends the process and none is written. These tests hold the stop where it holds the terminal, make the write of another
// goroutine, and read the order the terminal received.

// stopParkingRenderer is the main-screen renderer whose stop holds its goroutine, with the terminal held by the stop's transaction, before it writes.
type stopParkingRenderer struct {
	*tui.TuiMainScreen
	parked  chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *stopParkingRenderer) StopWithOptions(options tui.StopOptions) {
	r.once.Do(func() {
		close(r.parked)
		<-r.release
	})
	r.TuiMainScreen.StopWithOptions(options)
}

// stopFixture is a mode whose renderer draws on a process terminal over out, with a goroutine that is the owner loop and serves nothing (the loop is
// parked, as it is while a signal's cleanup runs, or has not come back from a long task).
type stopFixture struct {
	m   *InteractiveMode
	out *orderedOut
	ui  *stopParkingRenderer
}

func newStopFixture(t *testing.T, quitByKeys bool) *stopFixture {
	t.Helper()
	out := &orderedOut{}
	term := tui.NewProcessTerminalWithOutput(nil, nil, out)
	ui := &stopParkingRenderer{TuiMainScreen: tui.NewTuiMainScreen(term, nil, ""), parked: make(chan struct{}), release: make(chan struct{})}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work"})
	m.tuiInst = ui
	m.runCtx = t.Context()
	m.rememberDrawTerminal()
	m.quitByKeys = quitByKeys
	m.signalShutdownDone.Store(!quitByKeys)
	loopUp, loopEnds := make(chan struct{}), make(chan struct{})
	go func() {
		defer m.enterOwnerLoop()()
		close(loopUp)
		<-loopEnds
	}()
	<-loopUp
	t.Cleanup(func() { close(loopEnds) })
	return &stopFixture{m: m, out: out, ui: ui}
}

// hostWrites are the writes of goroutines other than the owner loop that the tests make: an extension's title and an OSC 52 copy.
var hostWrites = []struct {
	name  string
	probe string
	do    func(f *stopFixture)
}{
	{"title", "\x1b]0;HOST\x07", func(f *stopFixture) { (&ExtUIContext{m: f.m}).SetTitle("HOST") }},
	{"osc52", "\x1b]52;c;", func(f *stopFixture) {
		copier := hostClipboardCopier()
		copier.stdout = f.out
		copier.emitOSC52("HOST")
	}},
}

// A write requested before the stop began is not lost: the loop never ran it, so the stop writes it, in order, before its own bytes.
func TestHostWriteRequestedBeforeTheStopIsWrittenBeforeIt(t *testing.T) {
	for _, quitByKeys := range []bool{true, false} {
		name := "signal"
		if quitByKeys {
			name = "quit-by-keys"
		}
		for _, write := range hostWrites {
			t.Run(name+"/"+write.name, func(t *testing.T) {
				f := newStopFixture(t, quitByKeys)
				remove := f.m.orderClipboardWrites()
				defer remove()
				write.do(f)
				close(f.ui.release)
				f.m.stopInteractiveTui()
				got := strings.Join(f.out.received(), "")
				at, stop := strings.Index(got, write.probe), strings.Index(got, "\x1b[?25h")
				if at < 0 || stop < 0 || at > stop {
					t.Fatalf("terminal received %q: the write requested before the stop is not before the stop's cursor show", got)
				}
			})
		}
	}
}

// A write made while the stop holds the terminal never waits for it, never lands between its bytes, and runs after it for a quit by keys, as Pi's loop runs
// its callbacks during the dispose that follows stop(); a signal's exit drops it, as process.exit(0) leaves none to run.
func TestHostWriteMadeDuringTheStopIsAfterItOrDropped(t *testing.T) {
	for _, quitByKeys := range []bool{true, false} {
		name := "signal"
		if quitByKeys {
			name = "quit-by-keys"
		}
		for _, write := range hostWrites {
			t.Run(name+"/"+write.name, func(t *testing.T) {
				f := newStopFixture(t, quitByKeys)
				remove := f.m.orderClipboardWrites()
				defer remove()
				stopped := make(chan struct{})
				go func() {
					defer close(stopped)
					f.m.stopInteractiveTui()
				}()
				<-f.ui.parked
				write.do(f) // returns: the write of another goroutine does not wait for the stop
				close(f.ui.release)
				<-stopped
				got := strings.Join(f.out.received(), "")
				at, modes := strings.Index(got, write.probe), strings.LastIndex(got, "\x1b[?2004l")
				switch {
				case modes < 0:
					t.Fatalf("terminal received %q: the stop wrote no bracketed paste off", got)
				case quitByKeys && at < modes:
					t.Fatalf("terminal received %q: the write made during the stop is not after the stop's bytes", got)
				case quitByKeys && at < 0:
					t.Fatalf("terminal received %q: a quit by keys dropped the write made during the stop", got)
				case !quitByKeys && at >= 0:
					t.Fatalf("terminal received %q: a signal's stop is followed by a write", got)
				}
			})
		}
	}
}

// After the exit's stop ended, a signal's exit still runs: a write made then is dropped, a quit by keys writes it where it is made.
func TestHostWriteMadeAfterTheStopIsDroppedForASignalOnly(t *testing.T) {
	for _, quitByKeys := range []bool{true, false} {
		name := "signal"
		if quitByKeys {
			name = "quit-by-keys"
		}
		for _, write := range hostWrites {
			t.Run(name+"/"+write.name, func(t *testing.T) {
				f := newStopFixture(t, quitByKeys)
				remove := f.m.orderClipboardWrites()
				defer remove()
				close(f.ui.release)
				f.m.stopInteractiveTui()
				remove() // the mode's teardown: a quit by keys drops the order, a signal's exit keeps the stop gate
				write.do(f)
				got := strings.Join(f.out.received(), "")
				if at := strings.LastIndex(got, write.probe); (at >= 0) != quitByKeys {
					t.Fatalf("terminal received %q: write after the stop written = %t, want %t", got, at >= 0, quitByKeys)
				}
			})
		}
	}
}

// A stop that the run goes on after (an external editor, a suspend) is its own transaction, and the terminal is open again once it ends. The title is the
// terminal's own: a write that reaches it from a goroutine of its own (the remote editor's, the progress keepalive's) does not wait for the stop.
func TestHeldStopOrdersAWriteAndOpensAgain(t *testing.T) {
	f := newStopFixture(t, false)
	close(f.ui.release)
	terminal := *f.m.drawTerminal.Load()
	end := f.m.holdTerminalStop()
	terminal.SetTitle("DURING")
	if got := strings.Join(f.out.received(), ""); strings.Contains(got, "DURING") {
		t.Fatalf("terminal received %q: a title was written while the editor's stop held the terminal", got)
	}
	end()
	if got := strings.Join(f.out.received(), ""); !strings.HasSuffix(got, "\x1b]0;DURING\x07") {
		t.Fatalf("terminal received %q: the title made during the stop is not written when it ends", got)
	}
	terminal.SetTitle("AFTER")
	if got := strings.Join(f.out.received(), ""); !strings.HasSuffix(got, "\x1b]0;AFTER\x07") {
		t.Fatalf("terminal received %q: a title after a restart was not written", got)
	}
}
