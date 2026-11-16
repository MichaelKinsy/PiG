package codingagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// disposeHookRuntime is a runtime host with coding.Runtime's quit semantics for the beforeSessionInvalidate hook: the quit's dispose runs it.
type disposeHookRuntime struct {
	InteractiveRuntime
	hook func()
	once sync.Once
	runs atomic.Int32
}

func (h *disposeHookRuntime) dispose() { h.once.Do(func() { h.runs.Add(1); h.hook() }) }

func (h *disposeHookRuntime) SetBeforeSessionReplacement(func(context.Context) error) {}
func (h *disposeHookRuntime) SetBeforeSessionInvalidate(hook func())                  { h.hook = hook }
func (h *disposeHookRuntime) SetRebindSession(func(context.Context, InteractiveSessionHandle) error) {
}
func (h *disposeHookRuntime) SetBeforeQuitDispose(func()) {}
func (h *disposeHookRuntime) EmitQuitShutdown()           { h.dispose() }
func (h *disposeHookRuntime) EmitQuitShutdownAfterStop()  { h.dispose() }

type stdoutWriter struct{}

func (stdoutWriter) Write(p []byte) (int, error) { return os.Stdout.Write(p) }

// Pi's quit stops the TUI, awaits runtimeHost.dispose() (whose hook writes the session title), and only then prints the resume hint
// (interactive-mode.ts:4504-4513).
func TestQuitWritesTheTitleInTheDisposeBeforeTheResumeHint(t *testing.T) {
	setStdoutTTY(t, true)
	session, err := tempSessionMgr(t).Create("exit-title", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(mkAssistantMsg("finished")); err != nil {
		t.Fatal(err)
	}
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{
		CWD: t.TempDir(), SessionHandle: &recordingCompactHandle{inner: session},
	}, io.Writer(stdoutWriter{}))
	m.runtimeHost = &disposeHookRuntime{}
	m.installRuntimeHooks()
	m.rawRestore = func() { fmt.Print("<cooked>\n") }
	got := captureStdout(t, func() {
		if err := m.finishInteractiveShutdown(); err != nil {
			t.Fatal(err)
		}
	})
	cooked, title, hint := strings.Index(got, "<cooked>"), strings.Index(got, "\x1b]0;"), strings.Index(got, "To resume this session:")
	if cooked < 0 || title < 0 || hint < 0 || cooked >= title || title >= hint {
		t.Fatalf("order of the cooked restore (%d), the title (%d) and the resume hint (%d) is not Pi's; output %q", cooked, title, hint, got)
	}
}

// A signal's dispose holds shutdownMu while its hook waits for the owner loop; a quit that has entered finishInteractiveShutdown waits for that lock and
// serves nothing. The wait must end, and the hook run in place, instead of both waiting for ever.
func TestSignalDisposeDoesNotWaitForAnOwnerLoopThatWaitsForItsLock(t *testing.T) {
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{CWD: t.TempDir()}, io.Discard)
	m.runtimeHost = &disposeHookRuntime{}
	m.installRuntimeHooks()
	host := m.runtimeHost.(*disposeHookRuntime)
	done := make(chan struct{})
	go func() { m.shutdownFromSignal(false); close(done) }()
	// The owner loop is the quit that holds nothing yet and waits for shutdownMu.
	m.markQuitEntered()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the signal's dispose waits for an owner loop that waits for its lock")
	}
	if host.runs.Load() != 1 {
		t.Fatalf("the hook ran %d times, want once", host.runs.Load())
	}
}

// Both interleavings of a signal and a quit run the hook once and write the title once: the signal's dispose runs it in place when the quit entered first
// (the quit then finds it done), and a second signal joins the first (shutdownFromSignal's CAS).
func TestSignalThenQuitRunTheHookOnceAndWriteOneTitle(t *testing.T) {
	var out bytes.Buffer
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{CWD: t.TempDir()}, &out)
	host := &disposeHookRuntime{}
	m.runtimeHost = host
	m.installRuntimeHooks()
	done := make(chan struct{})
	go func() { m.shutdownFromSignal(false); m.shutdownFromSignal(false); close(done) }()
	m.markQuitEntered()
	<-done
	if err := m.finishInteractiveShutdown(); err != nil {
		t.Fatal(err)
	}
	if host.runs.Load() != 1 || strings.Count(out.String(), "\x1b]0;") != 1 {
		t.Fatalf("hook runs = %d, titles = %d, want 1 and 1; output %q", host.runs.Load(), strings.Count(out.String(), "\x1b]0;"), out.String())
	}
}

// The posted task is claimed once: abandoned before the loop dequeued it, the caller runs the work and the loop never does; dequeued and running when the
// loop-left signal arrives, the loop's run decides and the caller only waits for its result. The loop cannot reach markQuitEntered while it runs the
// task: only that goroutine closes quitEntered, and it is busy in the task.
func TestOwnerTaskIsClaimedOnceWhateverTheOrder(t *testing.T) {
	for _, dequeued := range []bool{false, true} {
		t.Run(fmt.Sprintf("dequeued %v", dequeued), func(t *testing.T) {
			m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{CWD: t.TempDir()}, io.Discard)
			var runs atomic.Int32
			started, release := make(chan struct{}), make(chan struct{})
			abandon := make(chan struct{})
			result := make(chan error, 1)
			call := func() {
				result <- m.runOnMainAndWaitUnless(t.Context(), abandon, nil, func() error {
					runs.Add(1)
					close(started)
					<-release
					return nil
				})
			}
			drain := func() {
				for {
					select {
					case fn := <-m.uiTaskCh:
						fn()
					default:
						return
					}
				}
			}
			var err error
			if dequeued {
				go func() { // the loop: it dequeues every task and runs it
					for {
						select {
						case fn := <-m.uiTaskCh:
							go fn()
						case <-t.Context().Done():
							return
						}
					}
				}()
				go call()
				<-started
				close(abandon)
				close(release)
				err = <-result
			} else {
				close(abandon)
				go call()
				err = <-result
				drain() // the loop reaches the task late: it was abandoned
			}
			if dequeued && (err != nil || runs.Load() != 1) {
				t.Fatalf("a running task: err %v, runs %d, want nil and 1", err, runs.Load())
			}
			if !dequeued && (!errors.Is(err, errOwnerLoopLeft) || runs.Load() != 0) {
				t.Fatalf("an abandoned task: err %v, runs %d, want errOwnerLoopLeft and 0", err, runs.Load())
			}
		})
	}
}

// A loop stuck in a write (a terminal nobody reads) serves nothing and has not entered the quit: the signal's dispose waits for it as long as it waits for
// extension handlers (signalShutdownWait, a second by default) and then goes on without the reset, which could not write either.
func TestSignalDisposeGivesUpOnABlockedOwnerLoop(t *testing.T) {
	var out bytes.Buffer
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{CWD: t.TempDir()}, &out)
	m.signalShutdownWait = 50 * time.Millisecond
	host := &disposeHookRuntime{}
	m.runtimeHost = host
	m.installRuntimeHooks()
	done := make(chan struct{})
	go func() { m.shutdownFromSignal(false); close(done) }()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the signal's dispose waits for ever for a blocked owner loop")
	}
	if strings.Contains(out.String(), "\x1b]0;") {
		t.Fatalf("the reset ran off the loop that was blocked: %q", out.String())
	}
}
