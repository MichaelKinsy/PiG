//go:build unix

package codingagent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// quitDisposeRuntime records which dispose a shutdown ran: the plain one, or the one of a mode whose TUI has stopped.
type quitDisposeRuntime struct {
	InteractiveRuntime
	order *[]string
}

func (h quitDisposeRuntime) EmitQuitShutdown() { *h.order = append(*h.order, "dispose") }

func (h quitDisposeRuntime) EmitQuitShutdownAfterStop() {
	*h.order = append(*h.order, "dispose after stop")
}

// Pi's quit by keys stops the TUI and then awaits runtimeHost.dispose() (interactive-mode.ts shutdown: drainInput, this.stop(), `await this.runtimeHost.dispose()`): stdin no
// longer keeps its event loop alive, so a dispose that waits for an aborted run whose author never reacts ends with the process (exit 0) when nothing else keeps the loop
// alive. The mode therefore runs that dispose as one whose input has ended, which is what lets the extension runtimes report a drained loop.
func TestQuitByKeysDisposesAsAModeWhoseInputEnded(t *testing.T) {
	var order []string
	m := upstreamShutdownMode(t, &order, false)
	m.runtimeHost = quitDisposeRuntime{order: &order}
	if err := m.finishInteractiveShutdown(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"drainInput", "stop", "dispose after stop"}; !slices.Equal(order, want) {
		t.Fatalf("the quit by keys ran %v, want %v: the dispose that follows the TUI's stop is the dispose of a mode whose input ended", order, want)
	}
}

// Pi's signal shutdown disposes BEFORE it stops the TUI (interactive-mode.ts shutdown({ fromSignal })): stdin still keeps its loop alive, so that dispose never ends with a
// drained loop, and the mode runs the plain dispose.
func TestSignalShutdownDisposesWhileItsInputIsAttached(t *testing.T) {
	var order []string
	m := upstreamShutdownMode(t, &order, false)
	m.runtimeHost = quitDisposeRuntime{order: &order}
	m.ShutdownFromSignal()
	if err := m.finishInteractiveShutdown(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"dispose", "drainInput", "stop"}; !slices.Equal(order, want) {
		t.Fatalf("the signal shutdown ran %v, want %v", order, want)
	}
}

// Pi's stop() restores the terminal and removes its signal handlers in one synchronous call (interactive-mode.ts:7283-7300), so no signal finds the quit unmarked after the terminal
// is cooked. A ctrl+c at that terminal reaches interruptShutdown, which skips the second of shutdownExtensionsBriefly only when quitDisposing says the quit holds shutdownMu,
// and finishInteractiveShutdown holds that lock from its first line: the mark must be set when the terminal is handed back, not after it.
func TestQuitByKeysIsMarkedDisposingBeforeTheTerminalTurnsCooked(t *testing.T) {
	var order []string
	m := upstreamShutdownMode(t, &order, false)
	m.runtimeHost = quitDisposeRuntime{order: &order}
	restored, disposingAtRestore := false, false
	m.rawRestore = func() { restored, disposingAtRestore = true, m.quitDisposing.Load() }
	if err := m.finishInteractiveShutdown(); err != nil {
		t.Fatal(err)
	}
	if !restored {
		t.Fatal("the quit did not hand the terminal back")
	}
	if !disposingAtRestore {
		t.Error("a ctrl+c that arrives when the terminal turns cooked finds the quit not marked as disposing: interruptShutdown waits a second for a lock the quit holds")
	}
}

// A quit that fails (the runtime crashed while it stopped the TUI) disposes nothing, so it is not marked as disposing.
func TestQuitByKeysThatEndsInACrashIsNotMarkedDisposing(t *testing.T) {
	var order []string
	m := upstreamShutdownMode(t, &order, false)
	m.runtimeHost = quitDisposeRuntime{order: &order}
	m.fatalRuntime.Store(true)
	if err := m.finishInteractiveShutdown(); !errors.Is(err, ErrInteractiveCrashed) {
		t.Fatalf("finishInteractiveShutdown = %v, want ErrInteractiveCrashed", err)
	}
	if m.quitDisposing.Load() {
		t.Error("a quit that ended in a crash is marked as disposing")
	}
}

const quitWindowChildEnv = "PIG_QUIT_WINDOW_CHILD_STATUS"

// TestQuitSignalAtTheTerminalHandBackChild runs one case in a child process, because the case ends the process: the first step of the termination hook (coding/cli main.go:
// interactive.ExitIfQuitting) on the signal's goroutine at the moment the quit by keys hands the terminal back. It must end the process with the signal's status. When it
// returns, the hook goes on to ShutdownFromSignal, which waits for ever on the shutdownMu that the pending quit holds: the child ends with 3 then.
func TestQuitSignalAtTheTerminalHandBackChild(t *testing.T) {
	value := os.Getenv(quitWindowChildEnv)
	if value == "" {
		t.Skip("runs as the child of TestQuitSignalAtTheTerminalHandBackEndsThePendingQuit")
	}
	status, err := strconv.Atoi(value)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	m := upstreamShutdownMode(t, &order, false)
	m.opts.AgentDir = t.TempDir()
	m.runtimeHost = quitDisposeRuntime{order: &order}
	m.rawRestore = func() {
		returned := make(chan struct{})
		go func() {
			m.ExitIfQuitting(status)
			close(returned)
		}()
		<-returned
		fmt.Fprintln(os.Stderr, "ExitIfQuitting returned at the terminal's hand back: the termination hook would wait for ever on the lock of the pending quit")
		os.Exit(3)
	}
	if err := m.finishInteractiveShutdown(); err != nil {
		t.Fatal(err)
	}
	os.Exit(4)
}

// Pi's stop() removed its signal handlers, so SIGTERM and SIGHUP at the terminal the quit by keys has handed back end Pi at once (143 and 129 after 24 to 29 ms under tmux).
// pig's termination hook asks ExitIfQuitting first, which knows the quit only through quitDisposing; a signal in the window between the terminal's restore and that mark
// went on to ShutdownFromSignal and waited for ever on the lock of the pending dispose.
func TestQuitSignalAtTheTerminalHandBackEndsThePendingQuit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{{"SIGTERM", 143}, {"SIGHUP", 129}} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestQuitSignalAtTheTerminalHandBackChild$")
			cmd.Env = append(os.Environ(), quitWindowChildEnv+"="+strconv.Itoa(tc.status), "PIG_HOME="+t.TempDir())
			output, err := cmd.CombinedOutput()
			code := 0
			if exit, ok := errors.AsType[*exec.ExitError](err); ok {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != tc.status {
				t.Errorf("the child ended with status %d, want %d:\n%s", code, tc.status, output)
			}
		})
	}
}

// heldQuitRuntime is a runtime whose dispose of a quit by keys does not return until release is closed: an author that never reacts to the abort.
type heldQuitRuntime struct {
	InteractiveRuntime
	entered chan<- struct{}
	release <-chan struct{}
}

func (h heldQuitRuntime) EmitQuitShutdownAfterStop() {
	close(h.entered)
	<-h.release
}

// Pi's stop() removed its signal handlers, so a ctrl+c at the stopped TUI of a quit whose dispose cannot return ends Pi at once (130 after 28 to 32 ms under tmux). The
// pending dispose has emitted session_shutdown already and holds shutdownMu, so the second that shutdownExtensionsBriefly grants would be spent on a lock that nothing
// releases: pig took 1.01 to 1.03 s (e12859ea5e). No clock decides here: the wait's bound is set beyond the test's budget, and the dispose is released only after
// interruptShutdown has returned, so an interrupt that waits for the lock cannot pass.
func TestInterruptOfAPendingQuitDoesNotWaitForItsLock(t *testing.T) {
	freshEnding(t)
	var order []string
	m := upstreamShutdownMode(t, &order, false)
	m.opts.AgentDir = t.TempDir()
	entered, release := make(chan struct{}), make(chan struct{})
	m.runtimeHost = heldQuitRuntime{entered: entered, release: release}
	m.signalShutdownWait = 24 * time.Hour
	budget := testbudget.Wait(t)
	released := false
	releaseQuit := func() {
		if !released {
			released = true
			close(release)
		}
	}
	defer releaseQuit()
	quit := make(chan error, 1)
	go func() { quit <- m.finishInteractiveShutdown() }()
	select {
	case <-entered:
	case <-time.After(budget):
		t.Fatalf("the quit by keys did not reach its dispose within %s", budget)
	}
	interrupted := make(chan bool, 1)
	go func() { interrupted <- m.interruptShutdown() }()
	select {
	case ok := <-interrupted:
		if !ok {
			t.Error("a running session ignored SIGINT")
		}
	case <-time.After(budget):
		t.Fatalf("interruptShutdown waited %s for the lock that the pending dispose of the quit holds, which nothing releases while the quit is held", budget)
	}
	releaseQuit()
	if err := <-quit; err != nil {
		t.Fatal(err)
	}
}
