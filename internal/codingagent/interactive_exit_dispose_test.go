package codingagent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// disposeHookRuntime is a runtime host with coding.Runtime's quit semantics for the beforeSessionInvalidate hook: the quit's dispose runs it.
type disposeHookRuntime struct {
	InteractiveRuntime
	hook func()
}

func (h *disposeHookRuntime) SetBeforeSessionReplacement(func(context.Context) error) {}
func (h *disposeHookRuntime) SetBeforeSessionInvalidate(hook func())                  { h.hook = hook }
func (h *disposeHookRuntime) SetRebindSession(func(context.Context, InteractiveSessionHandle) error) {
}
func (h *disposeHookRuntime) SetBeforeQuitDispose(func()) {}
func (h *disposeHookRuntime) EmitQuitShutdown()           { h.hook() }
func (h *disposeHookRuntime) EmitQuitShutdownAfterStop()  { h.hook() }

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
	if cooked < 0 || title < 0 || hint < 0 || !(cooked < title && title < hint) {
		t.Fatalf("order of the cooked restore (%d), the title (%d) and the resume hint (%d) is not Pi's; output %q", cooked, title, hint, got)
	}
}

// A signal's dispose holds shutdownMu while its hook waits for the owner loop; a quit that has entered finishInteractiveShutdown waits for that lock and
// serves nothing. The wait must end, and the hook run in place, instead of both waiting for ever.
func TestSignalDisposeDoesNotWaitForAnOwnerLoopThatWaitsForItsLock(t *testing.T) {
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{CWD: t.TempDir()}, io.Discard)
	m.runtimeHost = &disposeHookRuntime{}
	m.installRuntimeHooks()
	var ran sync.WaitGroup
	ran.Add(1)
	hook := m.runtimeHost.(*disposeHookRuntime).hook
	m.runtimeHost.(*disposeHookRuntime).hook = func() { defer ran.Done(); hook() }
	done := make(chan struct{})
	go func() { m.shutdownFromSignal(false); close(done) }()
	// The owner loop is the quit that holds nothing yet and waits for shutdownMu.
	m.markQuitEntered()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the signal's dispose waits for an owner loop that waits for its lock")
	}
	ran.Wait()
}
