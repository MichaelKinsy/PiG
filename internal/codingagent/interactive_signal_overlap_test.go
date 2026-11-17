package codingagent

import (
	"bytes"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// heldDisposeRuntime is coding.Runtime's quit dispose with the extensions' session_shutdown handlers held: the first caller emits and waits for release,
// later callers return at once (Runtime.EmitQuitShutdown's once), and the beforeSessionInvalidate hook runs when the handlers return.
type heldDisposeRuntime struct {
	disposeHookRuntime
	entered chan struct{}
	release chan struct{}
	emitted atomic.Bool
}

func newHeldDisposeRuntime() *heldDisposeRuntime {
	return &heldDisposeRuntime{entered: make(chan struct{}), release: make(chan struct{})}
}

func (h *heldDisposeRuntime) EmitQuitShutdown() {
	if !h.emitted.CompareAndSwap(false, true) {
		return
	}
	close(h.entered)
	<-h.release
	h.dispose()
}

func (h *heldDisposeRuntime) EmitQuitShutdownAfterStop() { h.EmitQuitShutdown() }

func awaitClosed(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// Pi's shutdown() returns at once for a second trigger (isShuttingDown), so a quit by keys that arrives during a signal's dispose changes nothing: the
// terminal is stopped once, by the signal's exit, after the dispose. pig's quit has entered the exit and waits for the dispose's lock, with the TUI still up;
// the dispose's end is its only bound (Pi's `await runtimeHost.dispose()` has none either, and a SIGINT ends the process within signalShutdownWait). When it
// ends, the signal's dispose has run once and written the one title, the quit adds neither a second session_shutdown nor a resume hint, and the title
// comes before the terminal stop.
func TestQuitByKeysDuringASignalDisposeWaitsForItAndAddsNothing(t *testing.T) {
	setStdoutTTY(t, true)
	session, err := tempSessionMgr(t).Create("overlap", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(mkAssistantMsg("finished")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{
		CWD: t.TempDir(), SessionHandle: &recordingCompactHandle{inner: session},
	}, &out)
	host := newHeldDisposeRuntime()
	m.runtimeHost = host
	m.installRuntimeHooks()
	signalDone := make(chan struct{})
	go func() { m.shutdownFromSignal(false); close(signalDone) }()
	awaitClosed(t, "the signal's dispose to emit", host.entered)
	quit := make(chan error, 1)
	stdout := captureStdout(t, func() {
		go func() { quit <- m.finishInteractiveShutdown() }()
		awaitClosed(t, "the quit to enter the exit", m.quitEnteredChan())
		close(host.release)
		awaitClosed(t, "the signal's dispose to end", signalDone)
		if err := <-quit; err != nil {
			t.Error(err)
		}
	})
	if host.runs.Load() != 1 {
		t.Fatalf("the dispose ran %d times, want once", host.runs.Load())
	}
	if titles := strings.Count(out.String(), "\x1b]0;"); titles != 1 {
		t.Fatalf("%d titles written, want one; output %q", titles, out.String())
	}
	if strings.Contains(stdout, "To resume this session:") {
		t.Fatalf("a quit that a signal's dispose had already begun printed the resume hint: %q", stdout)
	}
	title, stop := strings.Index(out.String(), "\x1b]0;"), strings.LastIndex(out.String(), "\x1b[?25h")
	if stop < 0 || title > stop {
		t.Fatalf("the title (%d) does not come before the terminal stop (%d): %q", title, stop, out.String())
	}
}

// A SIGINT that arrives while a signal's dispose holds its lock waits for the dispose no longer than signalShutdownWait (shutdownExtensionsBriefly), and the
// dispose it did not wait for goes on: the process ends at once afterwards (exitAtSignal), as Pi's, which has no SIGINT handler, does. A SIGTERM or SIGHUP
// that follows a SIGINT joins the dispose the SIGINT started and does not emit again.
func TestSigintDuringASignalDisposeWaitsOnlyItsBoundAndASecondSignalJoins(t *testing.T) {
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{CWD: t.TempDir()}, io.Discard)
	m.signalShutdownWait = 20 * time.Millisecond
	host := newHeldDisposeRuntime()
	m.runtimeHost = host
	m.installRuntimeHooks()
	// The first signal holds the dispose.
	first := make(chan struct{})
	go func() { m.shutdownFromSignal(false); close(first) }()
	awaitClosed(t, "the first signal's dispose to emit", host.entered)
	// A SIGINT gives up its wait for the handlers after its bound.
	interrupted := make(chan struct{})
	go func() { m.shutdownExtensionsBriefly(); close(interrupted) }()
	awaitClosed(t, "the SIGINT's wait to end by its bound", interrupted)
	select {
	case <-first:
		t.Fatal("the first signal's dispose ended before its handlers returned")
	default:
	}
	// A second SIGTERM or SIGHUP joins it.
	second := make(chan struct{})
	go func() { m.shutdownFromSignal(false); close(second) }()
	close(host.release)
	awaitClosed(t, "the first signal's dispose to end", first)
	awaitClosed(t, "the second signal to join", second)
	if host.runs.Load() != 1 {
		t.Fatalf("the dispose ran %d times, want once", host.runs.Load())
	}
}
