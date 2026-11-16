package factoryload_test

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Ports packages/coding-agent/test/suite/regressions/11264-pi-events-handler-errors.test.ts (Pi 18e99051be24): the event bus printed a throwing
// pi.events handler with console.error, which draws over the interactive TUI. Such errors now go to the extension error listener like other
// extension errors, and a handler that fails after its session was replaced reports nothing.

// stderrOf runs body with os.Stderr redirected into a pipe and returns what it wrote. The tests that use it do not run in parallel.
func stderrOf(t *testing.T, body func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	done := make(chan string)
	go func() {
		out, _ := io.ReadAll(reader)
		done <- string(out)
	}()
	func() {
		defer func() {
			os.Stderr = original
			_ = writer.Close()
		}()
		body()
	}()
	out := <-done
	_ = reader.Close()
	return out
}

// eventsHandlerErrorsRig is Pi's setup(): the factory subscribes a handler that throws and one that fails after it is released, the extension loads
// over a shared bus and runtime, and a runner over them reports to errors.
type eventsHandlerErrorsRig struct {
	bus     extension.EventBusController
	runtime *extension.ExtensionRuntime
	runner  *inproc.Runner
	mu      sync.Mutex
	errors  []extension.ExtensionError
	late    chan struct{}
	entered chan struct{}
	ext     extension.Extension
}

func newEventsHandlerErrorsRig(t *testing.T) *eventsHandlerErrorsRig {
	t.Helper()
	rig := &eventsHandlerErrorsRig{
		bus:     extension.CreateEventBus(),
		runtime: extension.CreateExtensionRuntime(),
		late:    make(chan struct{}),
		entered: make(chan struct{}, 1),
	}
	var err error
	rig.ext, err = factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		pi.Events().On("test:boom", func(any) { panic(errors.New("handler failed")) })
		pi.Events().On("test:late", func(any) {
			rig.entered <- struct{}{}
			<-rig.late
			panic(errors.New("late failure"))
		})
		return nil
	}, ".", rig.bus, rig.runtime, "<boom>")
	if err != nil {
		t.Fatal(err)
	}
	rig.runner = inproc.NewRunner([]extension.Extension{rig.ext}, ".", rig.runtime)
	rig.runner.AddErrorListener(func(e *extension.ExtensionError) {
		rig.mu.Lock()
		rig.errors = append(rig.errors, *e)
		rig.mu.Unlock()
	})
	return rig
}

func (r *eventsHandlerErrorsRig) reported() []extension.ExtensionError {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]extension.ExtensionError(nil), r.errors...)
}

// Pi: "reports a throwing handler to the extension error listener instead of stderr".
func TestPiEventsReportsAThrowingHandlerToTheExtensionErrorListenerInsteadOfStderr(t *testing.T) {
	rig := newEventsHandlerErrorsRig(t)
	stderr := stderrOf(t, func() { rig.bus.Emit("test:boom", nil) })
	got := rig.reported()
	if len(got) != 1 || got[0].ExtensionPath != "<boom>" || got[0].Event != "events:test:boom" || got[0].Error != "handler failed" {
		t.Fatalf("reported %+v, want one error {<boom>, events:test:boom, handler failed}", got)
	}
	if !strings.HasPrefix(got[0].Stack, "handler failed\n") || !strings.Contains(got[0].Stack, "events_handler_errors_upstream_test.go") {
		t.Fatalf("stack = %q, want the message line and the panicking goroutine's frames", got[0].Stack)
	}
	if stderr != "" {
		t.Fatalf("the bus wrote %q to stderr, want nothing", stderr)
	}
}

// Pi: "drops an error from a handler that fails after its session was disposed". Pi's note: the case also fails with only the stale check removed,
// so it pins the runner's staleMessage guard and not only the runtime's invalidation.
func TestPiEventsDropsAnErrorFromAHandlerThatFailsAfterItsSessionWasDisposed(t *testing.T) {
	rig := newEventsHandlerErrorsRig(t)
	stderr := stderrOf(t, func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			rig.bus.Emit("test:late", nil)
		}()
		<-rig.entered
		rig.runner.Invalidate("")
		close(rig.late)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the late handler never returned")
		}
	})
	if got := rig.reported(); len(got) != 0 {
		t.Fatalf("reported %+v after the runner was invalidated, want none", got)
	}
	if stderr != "" {
		t.Fatalf("the bus wrote %q to stderr, want nothing", stderr)
	}
}

// A handler that fails after a FAILED reload belongs to a runner that is stale (InvalidateForReload) but not retired, and still delivers events. Pi
// drops the error all the same: reportError looks at staleMessage, which a reload sets right after session_shutdown (agent-session.ts:3825-3826).
func TestPiEventsDropsAnErrorFromTheInstanceAFailedReloadLeftStale(t *testing.T) {
	rig := newEventsHandlerErrorsRig(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		rig.bus.Emit("test:late", nil)
	}()
	<-rig.entered
	rig.runner.InvalidateForReload("")
	close(rig.late)
	<-done
	if got := rig.reported(); len(got) != 0 {
		t.Fatalf("reported %+v for a stale (failed reload) runner, want none", got)
	}
}

// Pi's constructor binds runtime.reportError, so an error raised once the runner exists is reported although its core is not bound yet; before the
// runner exists the report is dropped without touching stderr (loader.ts createExtensionRuntime's `reportError: () => {}`).
func TestPiEventsReportsOnceTheRunnerExistsAndDropsBefore(t *testing.T) {
	bus := extension.CreateEventBus()
	runtime := extension.CreateExtensionRuntime()
	ext, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		pi.Events().On("test:boom", func(any) { panic("handler failed") })
		return nil
	}, ".", bus, runtime, "<boom>")
	if err != nil {
		t.Fatal(err)
	}
	if stderr := stderrOf(t, func() { bus.Emit("test:boom", nil) }); stderr != "" {
		t.Fatalf("an error before the runner existed wrote %q to stderr, want it dropped", stderr)
	}
	runner := inproc.NewRunner([]extension.Extension{ext}, ".", runtime)
	var got []extension.ExtensionError
	runner.AddErrorListener(func(e *extension.ExtensionError) { got = append(got, *e) })
	bus.Emit("test:boom", nil)
	if len(got) != 1 || got[0].Error != "handler failed" || got[0].Event != "events:test:boom" {
		t.Fatalf("reported %+v after the runner existed (BindCore not called), want one events:test:boom error with the panic's text", got)
	}
}

// The failure does not reach the emitter, and the channel's other subscribers still run (event-bus.ts:19-25).
func TestPiEventsHandlerErrorDoesNotStopOtherSubscribers(t *testing.T) {
	rig := newEventsHandlerErrorsRig(t)
	var heard []string
	rig.bus.On("test:boom", func(any) { heard = append(heard, "direct") })
	rig.bus.Emit("test:boom", nil)
	if len(heard) != 1 || len(rig.reported()) != 1 {
		t.Fatalf("heard %v, reported %d errors; want the later subscriber heard once and the failure reported once", heard, len(rig.reported()))
	}
}

// The bus keeps its own stderr write for a subscriber that does not go through pi.events, which only a host calling eventBus.on directly creates.
func TestPiEventsBusKeepsItsOwnReportForADirectSubscriber(t *testing.T) {
	rig := newEventsHandlerErrorsRig(t)
	rig.bus.On("direct", func(any) { panic("direct failure") })
	stderr := stderrOf(t, func() { rig.bus.Emit("direct", nil) })
	if !strings.Contains(stderr, "Event handler error (direct): direct failure") || len(rig.reported()) != 0 {
		t.Fatalf("stderr %q, reported %+v; want the bus's own line and no extension error", stderr, rig.reported())
	}
}
