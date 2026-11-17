package tui

import (
	"io"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/entered"
)

// ownedWriter is a terminal writer that knows how many goroutines are inside it. It can park one write there, and it keeps
// the writes in the order they arrived.
type ownedWriter struct {
	inside  atomic.Int32
	overlap chan struct{} // closed when a goroutine enters while another is inside
	once    sync.Once
	parked  chan struct{} // closed when the write that contains parkOn is inside
	release chan struct{} // closing it lets that write return
	// unguarded is written by every write under no lock of the writer, and by a parked write only after it has told the test
	// that it is inside: the race detector reports a second goroutine in the writer, as it did for the bytes.Buffer of
	// TestInvalidateReachesMountedRootsAndEveryOverlayComponent.
	unguarded int
	mu        sync.Mutex
	parkOn    string
	writes    []string
}

func newOwnedWriter(parkOn string) *ownedWriter {
	return &ownedWriter{overlap: make(chan struct{}), parked: make(chan struct{}), release: make(chan struct{}), parkOn: parkOn}
}

func (w *ownedWriter) Write(data []byte) (int, error) { return w.WriteString(string(data)) }

func (w *ownedWriter) WriteString(data string) (int, error) {
	if w.inside.Add(1) > 1 {
		w.once.Do(func() { close(w.overlap) })
	}
	defer w.inside.Add(-1)
	w.mu.Lock()
	w.writes = append(w.writes, data)
	park := w.parkOn != "" && strings.Contains(data, w.parkOn)
	if park {
		w.parkOn = ""
	}
	w.mu.Unlock()
	if park {
		close(w.parked)
	}
	w.unguarded++
	if park {
		<-w.release
	}
	return len(data), nil
}

// parkAt parks the next write that contains marker.
func (w *ownedWriter) parkAt(marker string) {
	w.mu.Lock()
	w.parkOn = marker
	w.mu.Unlock()
}

func (w *ownedWriter) written() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.writes)
}

// hookRows renders what its function returns; the function may call back into the renderer.
type hookRows struct{ render func() []string }

func (c *hookRows) Render(int) []string { return c.render() }
func (*hookRows) Invalidate()           {}

// secondGoroutine runs call. Its name marks the goroutine in a stack dump (secondGoroutineWaitsForALock).
//
//go:noinline
func secondGoroutine(call func()) { call() }

// secondGoroutineWaitsForALock reports whether the goroutine inside secondGoroutine is parked on a lock of package sync.
func secondGoroutineWaitsForALock() bool {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	return dumpShowsASecondGoroutineWaitingForALock(string(buf))
}

// dumpShowsASecondGoroutineWaitingForALock judges a dump of all goroutines (runtime.Stack): does the entry of the goroutine inside
// secondGoroutine show a wait for a lock (a sync.Mutex or sync.RWMutex, or the lock of a file descriptor). entered.Classify reads the
// state, the one place that tells the waits the Go runtime ends itself (the world lock of a collection a goroutine that allocates
// waits on, assist credit, a stack scan, a trace) from the program's own.
func dumpShowsASecondGoroutineWaitingForALock(dump string) bool {
	for goroutine := range strings.SplitSeq(dump, "\n\n") {
		header, frames, _ := strings.Cut(goroutine, "\n")
		_, state, found := strings.Cut(header, " [")
		if found && strings.Contains(frames, "tui.secondGoroutine(") && entered.Classify([]byte(state), []byte(frames)) == entered.Lock {
			return true
		}
	}
	return false
}

// The writer tests take "the second goroutine came to wait for a lock" from a dump of the goroutines, and a goroutine the Go runtime
// has stopped is not one that waits for the writer's lock: one that allocates while a collection starts waits on the world lock,
// which the dump shows as "semacquire", and one that allocates while a collection marks waits for assist credit. A reader that took
// the first for a lock ended startedWhileParked with "did not start" before the second goroutine reached the writer, so a renderer
// that let the second goroutine in a moment later passed: a test that could only mask a failure. The dumps are the recorded shapes
// (synthetic frames), so the judgement is tested without waiting for a collection at that instant.
func TestTheSecondGoroutineWaitsForALockOnlyWhenItsDumpEntryShowsALock(t *testing.T) {
	const own = "\ngithub.com/MichaelKinsy/PiG/tui.secondGoroutine(0x1)\n\t/tui/terminal_writer_ownership_test.go:83 +0x1\n"
	const allocating = "\nruntime.mallocgc(0x10, 0x0, 0x1)\n\t/runtime/malloc.go:1 +0x1\nruntime.makeslice(0x0, 0x10, 0x10)\n\t/runtime/slice.go:1 +0x1" + own
	const mutexWait = "\nsync.runtime_SemacquireMutex(0x1, 0x0, 0x1)\n\t/runtime/sema.go:1 +0x1\nsync.(*Mutex).Lock(...)\n\t/sync/mutex.go:1 +0x1\ngithub.com/MichaelKinsy/PiG/tui.(*tuiBase).writeOwned(0x1)\n\t/tui/tui.go:1 +0x1" + own
	const otherGoroutine = "goroutine 3 [running]:\nmain.other()\n\t/x.go:1 +0x1\n"
	for _, test := range []struct {
		name string
		dump string
		want bool
	}{
		{"a wait for a mutex", "goroutine 8 [sync.Mutex.Lock]:" + mutexWait, true},
		{"a wait for a read lock for minutes", "goroutine 8 [sync.RWMutex.RLock, 3 minutes]:" + mutexWait, true},
		{"the lock among other goroutines", otherGoroutine + "\ngoroutine 8 [sync.Mutex.Lock]:" + mutexWait, true},
		{"the world lock of a collection that starts, which an allocating goroutine waits on", "goroutine 8 [semacquire]:" + allocating, false},
		{"assist credit while a collection marks", "goroutine 8 [GC assist wait]:" + allocating, false},
		{"a stack scan", "goroutine 8 [running (scan)]:" + allocating, false},
		{"a wait for a wait group is not a lock", "goroutine 8 [sync.WaitGroup.Wait]:" + own, false},
		{"a wait for a channel", "goroutine 8 [chan receive]:" + own, false},
		{"a running goroutine", "goroutine 8 [running]:" + allocating, false},
		{"another goroutine's lock wait", "goroutine 8 [sync.Mutex.Lock]:" + strings.ReplaceAll(mutexWait, own, "\nmain.other()\n\t/x.go:1 +0x1\n"), false},
		{"no goroutine inside secondGoroutine", otherGoroutine, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := dumpShowsASecondGoroutineWaitingForALock(test.dump); got != test.want {
				t.Fatalf("dumpShowsASecondGoroutineWaitingForALock = %v, want %v for:\n%s", got, test.want, test.dump)
			}
		})
	}
}

// startedWhileParked runs call on a second goroutine while a write is parked in the writer and reports whether started was closed
// before the call returned or came to wait for a lock. It reads the state of the goroutine and no clock, so a slow machine
// cannot hide a goroutine that would have gone on. The returned channel is closed when call has returned.
func startedWhileParked(t *testing.T, started <-chan struct{}, call func()) (bool, <-chan struct{}) {
	t.Helper()
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		secondGoroutine(call)
	}()
	deadline := time.Now().Add(time.Minute)
	for waits := 0; ; {
		select {
		case <-started:
			return true, returned
		default:
		}
		select {
		case <-returned:
			select {
			case <-started:
				return true, returned
			default:
				return false, returned
			}
		default:
		}
		// Two looks in a row: a lock that is only passed through is not the one the goroutine waits for.
		if secondGoroutineWaitsForALock() {
			if waits++; waits == 2 {
				select {
				case <-started:
					return true, returned
				default:
					return false, returned
				}
			}
		} else {
			waits = 0
		}
		if time.Now().After(deadline) {
			t.Fatal("the second goroutine neither went on, returned nor waited for a lock within a minute")
		}
		runtime.Gosched()
	}
}

const (
	testFrameBegin     = "\x1b[?2026h"
	testRawWrite       = "<raw>"
	testClipboardWrite = "\x1b]52;c;"
	testSchemeOn       = "\x1b[?2031h"
	testSchemeOff      = "\x1b[?2031l"
)

// altScreenWithSelection is a started alternate screen with no CopySelection and a selection over the first row of the last frame.
func altScreenWithSelection(w *ownedWriter) *TuiAltScreen {
	ui := NewTuiAltScreenWithOutput(w, 40, 6, TuiAltScreenOptions{})
	ui.Add(NewText("row"))
	ui.Start()
	ui.mu.Lock()
	ui.previousScreen = []string{"hello world"}
	ui.selectionAnchor = &selectionPoint{row: 0, col: 0}
	ui.selectionFocus = &selectionPoint{row: 0, col: 4}
	ui.mu.Unlock()
	return ui
}

// Pi writes to the terminal from one event loop (tui.ts requestRender 1033-1041, scheduleRender 1067-1083, showOverlay
// 966-971: terminal.hideCursor() and then requestRender()): a cursor or raw write is before a frame or after it, never among
// its bytes, and no two writes run together. A renderer that keeps its own frame timer draws on the timer goroutine while its
// caller shows an overlay, hides the cursor or writes raw bytes on another; each row parks one of the two in the writer and
// lets the other come.
func TestOneGoroutineAtATimeIsInTheTerminalWriter(t *testing.T) {
	overlay := func() Component { return &recordingComponent{lines: []string{"OVER"}} }
	mainScreen := func(w *ownedWriter) *TuiMainScreen {
		ui := NewWithOutput(w, 40, 6)
		ui.Add(NewText("row"))
		return ui
	}
	// frameThen: a frame on the timer goroutine of the renderer is in the writer, and the caller makes call.
	frameThen := func(call func(ui *TuiMainScreen)) func(w *ownedWriter) (hold, second, stop func()) {
		return func(w *ownedWriter) (func(), func(), func()) {
			ui := mainScreen(w)
			w.parkAt(testFrameBegin)
			return func() { ui.RequestRender() }, func() { call(ui) }, ui.Stop
		}
	}
	rows := []struct {
		name  string
		build func(w *ownedWriter) (hold, second, stop func())
		// after, when set, is the last write of the first goroutine: the raw write of the second follows it directly.
		after string
	}{
		{"a frame is writing and ShowOverlay hides the cursor", frameThen(func(ui *TuiMainScreen) { ui.ShowOverlay(overlay(), OverlayOptions{}) }), ""},
		{"a frame is writing and HideCursor is called", frameThen(func(ui *TuiMainScreen) { ui.HideCursor() }), ""},
		{"a frame is writing and ShowCursor is called", frameThen(func(ui *TuiMainScreen) { ui.ShowCursor() }), ""},
		{"a frame is writing and WriteRaw is called", frameThen(func(ui *TuiMainScreen) { ui.WriteRaw(testRawWrite) }), "\x1b[?25l"},
		{"ShowOverlay is hiding the cursor and a frame comes", func(w *ownedWriter) (func(), func(), func()) {
			ui := mainScreen(w)
			w.parkAt("\x1b[?25l")
			return func() { ui.ShowOverlay(overlay(), OverlayOptions{}) }, ui.Render, ui.Stop
		}, ""},
		{"Stop is writing and WriteRaw is called", func(w *ownedWriter) (func(), func(), func()) {
			ui := mainScreen(w)
			ui.Render()
			w.parkAt("\r\n")
			return ui.Stop, func() { ui.WriteRaw(testRawWrite) }, func() {}
		}, "\x1b[?25h"},
		{"an alternate-screen frame is writing and ShowOverlay hides the cursor", func(w *ownedWriter) (func(), func(), func()) {
			ui := NewTuiAltScreenWithOutput(w, 40, 6, TuiAltScreenOptions{})
			ui.Add(NewText("row"))
			ui.Start()
			ui.Add(NewText("more"))
			w.parkAt(testFrameBegin)
			return func() { ui.RequestRender() }, func() { ui.ShowOverlay(overlay(), OverlayOptions{}) }, func() { ui.StopWithOptions(StopOptions{}) }
		}, ""},
		{"QueryTerminalColors is writing its query and a frame comes", func(w *ownedWriter) (func(), func(), func()) {
			ui := mainScreen(w)
			ui.Render()
			w.parkAt(terminalColorQuery)
			return func() { ui.QueryTerminalColors(TerminalColorQueryOptions{TimeoutMs: 1}) }, ui.Render, ui.Stop
		}, ""},
		// An alternate screen without a CopySelection copies through OSC 52 (tui-alt-screen.ts:1476 terminal.write): the extension host builds
		// its screens that way unless the extension supplies copySelection, and an input goroutine copies while the frame timer draws.
		{"an alternate-screen selection copy is writing and a frame comes", func(w *ownedWriter) (func(), func(), func()) {
			ui := altScreenWithSelection(w)
			w.parkAt(testClipboardWrite)
			return func() { ui.CopyActiveSelectionToClipboard() }, ui.Render, func() { ui.StopWithOptions(StopOptions{}) }
		}, ""},
		{"Start is entering the alternate screen and a frame comes", func(w *ownedWriter) (func(), func(), func()) {
			ui := NewTuiAltScreenWithOutput(w, 40, 6, TuiAltScreenOptions{})
			ui.Add(NewText("row"))
			w.parkAt(altEnterAltScreen)
			return ui.Start, ui.Render, func() { ui.StopWithOptions(StopOptions{}) }
		}, ""},
		{"Start is turning the colour-scheme reports on and WriteRaw is called", func(w *ownedWriter) (func(), func(), func()) {
			ui := NewTuiAltScreenWithOutput(w, 40, 6, TuiAltScreenOptions{})
			ui.Add(NewText("row"))
			ui.SetTerminalColorSchemeNotifications(true)
			w.parkAt(testSchemeOn)
			return ui.Start, func() { ui.WriteRaw(testRawWrite) }, func() { ui.StopWithOptions(StopOptions{}) }
		}, ""},
		{"Stop is writing the teardown and WriteRaw is called", func(w *ownedWriter) (func(), func(), func()) {
			ui := altScreenWithSelection(w)
			w.parkAt(altEnableAutowrap)
			return func() { ui.StopWithOptions(StopOptions{PreserveScreen: true}) }, func() { ui.WriteRaw(testRawWrite) }, func() {}
		}, ""},
		{"Stop is turning the colour-scheme reports off and HideCursor is called", func(w *ownedWriter) (func(), func(), func()) {
			ui := altScreenWithSelection(w)
			ui.SetTerminalColorSchemeNotifications(true)
			w.parkAt(testSchemeOff)
			return func() { ui.StopWithOptions(StopOptions{PreserveScreen: true}) }, ui.HideCursor, func() {}
		}, ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			w := newOwnedWriter("")
			hold, second, stop := row.build(w)
			go hold()
			<-w.parked
			entered, returned := startedWhileParked(t, w.overlap, second)
			if entered {
				t.Errorf("a second goroutine was in the terminal writer while the first was still inside it; writes so far: %q", w.written())
			}
			close(w.release)
			<-returned
			stop()
			writes := w.written()
			if at := slices.Index(writes, testRawWrite); row.after != "" && (at < 1 || writes[at-1] != row.after) {
				t.Errorf("the raw write is at %d of %q, want it directly after %q, the last write of the goroutine that was inside", at, writes, row.after)
			}
		})
	}
}

// Two frames of one renderer never run together, whichever goroutines they are on (Pi: one event loop): a frame that comes
// while another is writing starts when that one has returned, and draws the state of then.
func TestASecondFrameStartsWhenTheFrameThatIsWritingHasReturned(t *testing.T) {
	w := newOwnedWriter(testFrameBegin)
	ui := NewWithOutput(w, 40, 6)
	state, renders, again := "one", 0, make(chan struct{})
	ui.Add(&hookRows{render: func() []string {
		if renders++; renders == 2 {
			close(again)
		}
		return []string{state}
	}})
	ui.RequestRender()
	<-w.parked
	state = "two"
	started, returned := startedWhileParked(t, again, ui.Render)
	if started {
		t.Errorf("a second frame rendered the components while the first frame was still writing")
	}
	select {
	case <-w.overlap:
		t.Errorf("two frames were in the terminal writer together")
	default:
	}
	close(w.release)
	<-returned
	ui.Stop()
	writes := w.written()
	if len(writes) < 3 || !strings.Contains(writes[0], "one") || writes[1] != "\x1b[?25l" || !strings.Contains(writes[2], "two") {
		t.Errorf("writes %q, want the first frame with its cursor write and then the second frame", writes)
	}
}

// A frame asked for while a frame is being drawn is drawn once after it (tui.ts 1073-1083: the timer callback renders and,
// when a request came during the render, schedules again): the last state asked for is on the screen, and no third frame follows.
func TestAFrameRequestedDuringAFrameIsDrawnOnceAfterIt(t *testing.T) {
	w := newOwnedWriter(testFrameBegin)
	ui := NewWithOutput(w, 40, 6)
	// fired gets one value when a timer callback of the renderer has returned.
	fired := make(chan struct{}, 16)
	arm := ui.afterFunc
	ui.afterFunc = func(d time.Duration, fn func()) stoppableTimer {
		return arm(d, func() {
			fn()
			fired <- struct{}{}
		})
	}
	state := "one"
	ui.Add(&hookRows{render: func() []string { return []string{state} }})
	frames := func() (count int, last string) {
		for _, write := range w.written() {
			if strings.Contains(write, testFrameBegin) {
				count, last = count+1, write
			}
		}
		return count, last
	}
	pending := func() bool {
		ui.mu.Lock()
		defer ui.mu.Unlock()
		return ui.renderRequested || ui.renderTimer != nil
	}
	ui.RequestRender()
	<-w.parked
	state = "two"
	ui.RequestRender()
	ui.RequestRender()
	close(w.release)
	<-fired
	if count, _ := frames(); !pending() && count < 2 {
		t.Fatalf("the frame asked for while a frame was writing was dropped: %d frame drawn, none pending; writes %q", count, w.written())
	}
	<-fired
	if count, last := frames(); count != 2 || !strings.Contains(last, "two") || pending() {
		t.Errorf("%d frames, the last %q, another pending %v; want 2 frames, the last with the state asked for during the first, and none pending", count, last, pending())
	}
	ui.Stop()
}

// A component may write to the terminal from its Render, as it may in Pi, where showOverlay calls terminal.hideCursor() at once
// (tui.ts 966) and the frame is written when every component has rendered: the frame that is rendering does not wait for
// itself, and the writes are before its bytes.
func TestWritesFromARenderGoBeforeTheBytesOfTheFrame(t *testing.T) {
	w := newOwnedWriter("")
	ui := NewWithOutput(w, 40, 6)
	asked := false
	over := &hookRows{render: func() []string {
		ui.HideCursor()
		return []string{"OVER"}
	}}
	ui.Add(&hookRows{render: func() []string {
		if !asked {
			asked = true
			ui.ShowOverlay(over, OverlayOptions{})
			ui.ShowCursor()
			ui.WriteRaw(testRawWrite)
		}
		return []string{"row"}
	}})
	never := make(chan struct{})
	_, returned := startedWhileParked(t, never, ui.Render)
	select {
	case <-returned:
	default:
		t.Fatal("the frame waits for a lock that it holds itself: a write from a Render must not take what the frame keeps while its components render")
	}
	ui.Stop()
	writes := w.written()
	frame := slices.IndexFunc(writes, func(write string) bool { return strings.Contains(write, testFrameBegin) })
	want := []string{"\x1b[?25l", "\x1b[?25h", testRawWrite}
	if frame < len(want) || !slices.Equal(writes[:len(want)], want) {
		t.Errorf("writes %q, want %q before the frame at %d", writes, want, frame)
	}
}

func BenchmarkCursorWrites(b *testing.B) {
	ui := NewWithOutput(io.Discard, 80, 24)
	b.ReportAllocs()
	for b.Loop() {
		ui.HideCursor()
		ui.ShowCursor()
		ui.WriteRaw("\x1b[16t")
	}
}
