//go:build unix

package tui

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports packages/tui/test/suspend-signal.test.ts (Pi e4f50d76, 3107a58d), the seven cases in order of the file after 3107a58d. pig divergence (D661): the
// listener is for SIGCONT (SetContinueSignalHandler), so process.listenerCount("SIGTSTP") is the state of that listener, and process.emit is a SIGCONT
// sent to this process, which a running process ignores.
//
//	case in Pi                                              here
//	calls the handler on SIGTSTP only while started        TestContinueSignalHandlerRunsOnlyWhileStarted
//	does not register a listener twice when start() runs    TestContinueSignalListenerIsNotAddedTwice
//	applies a handler set or cleared while started          TestContinueSignalHandlerSetWhileStartedAppliesAtOnce
//	does not register a listener when set while stopped     TestContinueSignalHandlerSetWhileStoppedAddsNoListener
//	does not register a listener before the first start()   TestContinueSignalListenerWaitsForTheFirstStart
//	keeps the listener until stop() has restored            TestContinueSignalListenerLastsUntilStopHasRestored
//	does not remove the listener while it stays needed      TestContinueSignalListenerStaysWhileNeeded

// continueTestSetup resets the process-wide state the other tests of the package may have left (a renderer that was started and never stopped).
func continueTestSetup(t *testing.T) {
	t.Helper()
	resetContinueSignalState()
	t.Cleanup(resetContinueSignalState)
}

func resetContinueSignalState() {
	continueSignal.mu.Lock()
	continueSignal.owners = nil
	continueSignal.current = nil
	exited := updateContinueListener()
	continueSignal.mu.Unlock()
	if exited != nil {
		<-exited
	}
}

func continueListening() bool {
	continueSignal.mu.Lock()
	defer continueSignal.mu.Unlock()
	return continueSignal.listening != nil
}

func continueListenerChan() chan os.Signal {
	continueSignal.mu.Lock()
	defer continueSignal.mu.Unlock()
	return continueSignal.listening
}

func newContinueTestScreen(t *testing.T) *TuiMainScreen {
	t.Helper()
	t.Setenv("COLUMNS", "40")
	t.Setenv("LINES", "10")
	var out bytes.Buffer
	ui := NewTuiMainScreen(NewProcessTerminalWithOutput(nil, nil, &out), nil, t.TempDir())
	t.Cleanup(ui.Stop)
	return ui
}

func sendContinue(t *testing.T) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
}

func TestContinueSignalHandlerRunsOnlyWhileStarted(t *testing.T) {
	continueTestSetup(t)
	var calls atomic.Int64
	called := make(chan struct{}, 4)
	ui := newContinueTestScreen(t)
	SetContinueSignalHandler(func(owned func() bool) {
		if owned() {
			calls.Add(1)
		}
		called <- struct{}{}
	})
	ui.Start()
	if !continueListening() {
		t.Fatal("no listener while started")
	}
	sendContinue(t)
	select {
	case <-called:
	case <-time.After(10 * time.Second):
		t.Fatal("the handler did not run on SIGCONT while started")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("the handler ran %d times with the terminal owned, want 1", got)
	}
	ui.Stop()
	if continueListening() {
		t.Fatal("the listener outlived stop()")
	}
	ui.Start()
	if !continueListening() {
		t.Fatal("no listener after start() again")
	}
}

func TestContinueSignalListenerIsNotAddedTwice(t *testing.T) {
	continueTestSetup(t)
	ui := newContinueTestScreen(t)
	SetContinueSignalHandler(func(func() bool) {})
	ui.Start()
	first := continueListenerChan()
	if first == nil {
		t.Fatal("no listener after first start()")
	}
	ui.Start()
	if second := continueListenerChan(); second != first {
		t.Fatal("second start() replaced the SIGCONT listener instead of keeping it")
	}
}

func TestContinueSignalHandlerSetWhileStartedAppliesAtOnce(t *testing.T) {
	continueTestSetup(t)
	ui := newContinueTestScreen(t)
	ui.Start()
	if continueListening() {
		t.Fatal("a listener without a handler")
	}
	SetContinueSignalHandler(func(func() bool) {})
	if !continueListening() {
		t.Fatal("a handler set while started did not add the listener")
	}
	SetContinueSignalHandler(nil)
	if continueListening() {
		t.Fatal("clearing the handler did not remove the listener")
	}
}

func TestContinueSignalHandlerSetWhileStoppedAddsNoListener(t *testing.T) {
	continueTestSetup(t)
	ui := newContinueTestScreen(t)
	ui.Start()
	ui.Stop()
	SetContinueSignalHandler(func(func() bool) {})
	if continueListening() {
		t.Fatal("a handler set while stopped added the listener")
	}
}

func TestContinueSignalListenerWaitsForTheFirstStart(t *testing.T) {
	continueTestSetup(t)
	ui := newContinueTestScreen(t)
	SetContinueSignalHandler(func(func() bool) {})
	if continueListening() {
		t.Fatal("a listener before the first start()")
	}
	ui.Start()
	if !continueListening() {
		t.Fatal("no listener after the first start()")
	}
}

// listenerRecordingWriter records continueListening() on every write to a ProcessTerminal's output sink,
// synchronized so a timer-scheduled render goroutine cannot race with reset/snapshot.
type listenerRecordingWriter struct {
	mu   sync.Mutex
	seen []bool
	out  bytes.Buffer
}

func (w *listenerRecordingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.seen = append(w.seen, continueListening())
	n, err := w.out.Write(p)
	w.mu.Unlock()
	return n, err
}

func (w *listenerRecordingWriter) reset() {
	w.mu.Lock()
	w.seen = nil
	w.mu.Unlock()
}

func (w *listenerRecordingWriter) snapshot() []bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]bool(nil), w.seen...)
}

func TestContinueSignalListenerLastsUntilStopHasRestored(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(t *testing.T, out *listenerRecordingWriter) TUI
	}{
		{
			name: "main screen",
			make: func(t *testing.T, out *listenerRecordingWriter) TUI {
				ui := NewTuiMainScreen(NewProcessTerminalWithOutput(nil, nil, out), nil, t.TempDir())
				t.Cleanup(ui.Stop)
				return ui
			},
		},
		{
			name: "alt screen",
			make: func(t *testing.T, out *listenerRecordingWriter) TUI {
				ui := NewTuiAltScreen(NewProcessTerminalWithOutput(nil, nil, out), nil, t.TempDir())
				t.Cleanup(func() { ui.StopWithOptions(StopOptions{PreserveScreen: true}) })
				return ui
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			continueTestSetup(t)
			t.Setenv("COLUMNS", "40")
			t.Setenv("LINES", "10")
			var recorder listenerRecordingWriter
			ui := tc.make(t, &recorder)
			SetContinueSignalHandler(func(func() bool) {})
			ui.Start()
			recorder.reset()
			ui.StopWithOptions(StopOptions{PreserveScreen: true, ExitTerminal: true})
			seen := recorder.snapshot()
			if len(seen) == 0 {
				t.Fatal("stop() wrote nothing to the terminal")
			}
			for i, listening := range seen {
				if !listening {
					t.Fatalf("the listener was gone at write %d of the restore (%v)", i, seen)
				}
			}
			if continueListening() {
				t.Fatal("the listener outlived stop()")
			}
		})
	}
}

func TestContinueSignalListenerStaysWhileNeeded(t *testing.T) {
	continueTestSetup(t)
	ui := newContinueTestScreen(t)
	SetContinueSignalHandler(func(func() bool) {})
	ui.Start()
	before := continueListenerChan()
	if before == nil {
		t.Fatal("no listener after start()")
	}
	SetContinueSignalHandler(func(func() bool) {})
	ui.Start()
	if after := continueListenerChan(); after != before {
		t.Fatal("the SIGCONT listener was removed and recreated while it stayed needed")
	}
}

// The renderer that starts last keeps the terminal's ownership until its own stop: a swap of renderers (stop, then start) has no moment with an owner and a
// listener missing in between that a signal could fall into, and a renderer that was never started owns nothing.
func TestContinueSignalOwnershipFollowsEachRenderer(t *testing.T) {
	continueTestSetup(t)
	first, second := newContinueTestScreen(t), newContinueTestScreen(t)
	SetContinueSignalHandler(func(func() bool) {})
	first.Start()
	second.Start()
	first.Stop()
	if !continueListening() || !terminalOwned() {
		t.Fatal("the second renderer owns the terminal; the listener must stay")
	}
	second.Stop()
	if continueListening() || terminalOwned() {
		t.Fatal("no renderer owns the terminal; the listener must go")
	}
}

// Removing an older handler registration does not clear a newer registration.
func TestContinueSignalRemoveClearsOnlyItsOwnRegistration(t *testing.T) {
	continueTestSetup(t)
	ui := newContinueTestScreen(t)
	removeFirst := SetContinueSignalHandler(func(func() bool) {})
	removeSecond := SetContinueSignalHandler(func(func() bool) {})
	ui.Start()
	if !continueListening() {
		t.Fatal("no listener while started with second handler")
	}
	removeFirst()
	if !continueListening() {
		t.Fatal("removing the first handler cleared the second handler's listener")
	}
	removeSecond()
	if continueListening() {
		t.Fatal("removing the active handler left the listener running")
	}
}

// Repeated start/stop cycles and handler clears join their listener goroutine and leak no goroutines.
func TestContinueSignalStartStopDoesNotLeakGoroutines(t *testing.T) {
	continueTestSetup(t)
	ui := newContinueTestScreen(t)
	baseline := testenv.BaselineGoroutines()

	SetContinueSignalHandler(func(func() bool) {})
	const cycles = 25
	for range cycles {
		ui.Start()
		if !continueListening() {
			t.Fatal("listener not active after Start")
		}
		ui.Stop()
		if continueListening() {
			t.Fatal("listener still active after Stop")
		}
	}
	SetContinueSignalHandler(nil)
	baseline.RequireEnded(t, 5*time.Second)
}

// TuiSurface drops terminal ownership on Suspend (when lending the terminal to an external editor or Ctrl+Z) and regains it on Resume.
func TestContinueSignalTuiSurfaceSuspendAndResume(t *testing.T) {
	continueTestSetup(t)
	session := &recordingSession{}
	surface := NewTuiSurfaceWithSize(session, 40, 10, nil)
	t.Cleanup(surface.Stop)
	SetContinueSignalHandler(func(func() bool) {})
	if continueListening() {
		t.Fatal("listener registered before surface Start")
	}
	surface.Start()
	if !continueListening() {
		t.Fatal("listener missing after surface Start")
	}
	surface.Suspend()
	if continueListening() || terminalOwned() {
		t.Fatal("listener stayed active while surface is suspended")
	}
	surface.Resume()
	if !continueListening() || !terminalOwned() {
		t.Fatal("listener missing after surface Resume")
	}
	surface.Stop()
	if continueListening() || terminalOwned() {
		t.Fatal("listener stayed active after surface Stop")
	}
}

// virtualContinueTerminal is a Terminal that is not the process terminal, as an extension's tuiAltTerminal bridge is.
type virtualContinueTerminal struct {
	Terminal
}

// Reading (i): an extension's alternate-screen bridge (or any renderer on a non-process terminal) must not count as an owner of the process terminal.
// When the interactive renderer stops (e.g. for an external editor) while a non-process renderer is still started, the SIGCONT listener must stop and
// terminalOwned must report false so a SIGCONT does not re-arm the TUI under the editor.
func TestContinueSignalIgnoresRendererNotOnProcessTerminal(t *testing.T) {
	continueTestSetup(t)
	var mainOut, bridgeOut bytes.Buffer
	processUI := NewTuiMainScreen(NewProcessTerminalWithOutput(nil, nil, &mainOut), nil, t.TempDir())
	t.Cleanup(processUI.Stop)
	bridgeUI := NewTuiAltScreen(virtualContinueTerminal{NewProcessTerminalWithOutput(nil, nil, &bridgeOut)}, nil, t.TempDir())
	t.Cleanup(func() { bridgeUI.StopWithOptions(StopOptions{PreserveScreen: true}) })

	SetContinueSignalHandler(func(func() bool) {})
	bridgeUI.Start()
	if continueListening() || terminalOwned() {
		t.Fatal("a renderer not on the process terminal claimed continue-signal ownership")
	}
	processUI.Start()
	if !continueListening() || !terminalOwned() {
		t.Fatal("the process-terminal renderer did not claim ownership")
	}
	processUI.Stop()
	if continueListening() || terminalOwned() {
		t.Fatal("after the process-terminal renderer stopped, a non-process renderer kept the SIGCONT listener alive")
	}
}

// Writer-backed renderers (NewWithOutput, NewTuiAltScreenWithOutput) have terminal == nil and do not draw on the process terminal;
// they must not claim process-terminal SIGCONT ownership when started.
func TestContinueSignalIgnoresWriterBackedRendererWithNilTerminal(t *testing.T) {
	continueTestSetup(t)
	SetContinueSignalHandler(func(func() bool) {})

	var mainOut, altOut bytes.Buffer
	mainUI := NewWithOutput(&mainOut, 40, 10)
	t.Cleanup(mainUI.Stop)
	mainUI.Start()
	if continueListening() || terminalOwned() {
		t.Fatal("writer-backed TuiMainScreen (terminal == nil) claimed process-terminal SIGCONT ownership")
	}

	altUI := NewTuiAltScreenWithOutput(&altOut, 40, 10, TuiAltScreenOptions{})
	t.Cleanup(func() { altUI.StopWithOptions(StopOptions{PreserveScreen: true}) })
	altUI.Start()
	if continueListening() || terminalOwned() {
		t.Fatal("writer-backed TuiAltScreen (terminal == nil) claimed process-terminal SIGCONT ownership")
	}
}

// ProcessTerminal.enterRawMode must begin SIGCONT ownership BEFORE MakeRaw runs and end it AFTER RestoreCooked finishes.
// Moving t.continues.begin() after MakeRaw or moving defer t.continues.end() before RestoreCooked fails this test immediately.
func TestContinueSignalRawModeOwnershipSpansMakeRawAndRestoreCooked(t *testing.T) {
	continueTestSetup(t)
	SetContinueSignalHandler(func(func() bool) {})

	var inMakeRaw, inRestore bool
	prevMake, prevRestore := makeRawTerminal, restoreTerminal
	makeRawTerminal = func(int) (*term.State, error) {
		inMakeRaw = continueListening()
		return &term.State{}, nil
	}
	restoreTerminal = func(int, *term.State) error {
		inRestore = continueListening()
		return nil
	}
	t.Cleanup(func() {
		makeRawTerminal = prevMake
		restoreTerminal = prevRestore
	})

	var out bytes.Buffer
	pt := NewProcessTerminalWithOutput(os.Stdin, nil, &out)
	restore, err := pt.enterRawMode(false)
	if err != nil {
		t.Fatalf("enterRawMode: %v", err)
	}
	if !inMakeRaw {
		t.Fatal("SIGCONT listener was not active during MakeRaw (t.continues.begin() ran too late)")
	}
	if !continueListening() {
		t.Fatal("SIGCONT listener was not active while raw mode was armed")
	}
	restore()
	if !inRestore {
		t.Fatal("SIGCONT listener ended before RestoreCooked completed (t.continues.end() ran too early)")
	}
	if continueListening() {
		t.Fatal("SIGCONT listener outlived raw-mode restore")
	}
}

// ProcessTerminal.rearmRawMode must pop any active Kitty keyboard protocol push before re-pushing,
// so repeated re-arms on any unix platform (including macOS, without an ANSI stop writing ESC[<u) leave
// pushes and pops balanced on exit.
func TestRearmRawModeBalancesKittyKeyboardStack(t *testing.T) {
	continueTestSetup(t)
	prevMake, prevRestore := makeRawTerminal, restoreTerminal
	makeRawTerminal = func(int) (*term.State, error) { return &term.State{}, nil }
	restoreTerminal = func(int, *term.State) error { return nil }
	t.Cleanup(func() {
		makeRawTerminal = prevMake
		restoreTerminal = prevRestore
		keyboardProtocolPushed.Store(false)
		modifyOtherKeysActive.Store(false)
	})
	t.Setenv("PI_PROGRAM_STATUS", "0")

	var out bytes.Buffer
	pt := NewProcessTerminalWithOutput(os.Stdin, nil, &out)
	restore, err := pt.enterRawMode(false)
	if err != nil {
		t.Fatalf("enterRawMode: %v", err)
	}
	for range 3 {
		if err := pt.rearmRawMode(); err != nil {
			t.Fatalf("rearmRawMode: %v", err)
		}
	}
	restore()

	got := out.String()
	pushes := strings.Count(got, "\x1b[>7u")
	pops := strings.Count(got, "\x1b[<u")
	if pushes != 4 || pops != 4 {
		t.Fatalf("unbalanced Kitty keyboard stack across enter + 3 re-arms + restore: pushes=%d pops=%d (want 4/4); out=%q", pushes, pops, got)
	}
}
