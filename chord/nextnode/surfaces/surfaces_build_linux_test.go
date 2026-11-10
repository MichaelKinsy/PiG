//go:build linux

package surfaces

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// An entry in a new directory importing `./held.ts`, a pipe: its build waits until the pipe is written (Pi's mkfifo).
func heldEntry(t *testing.T) (entry, pipe string) {
	t.Helper()
	dir := t.TempDir()
	pipe = filepath.Join(dir, "held.ts")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	entry = filepath.Join(dir, "main.ts")
	if err := os.WriteFile(entry, []byte("import \"./held.ts\";\nglobalThis.started = true;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry, pipe
}

// release writes the pipe once the build reads it (an open for writing succeeds only while a reader waits).
func release(t *testing.T, pipe string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if file, err := os.OpenFile(pipe, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_, _ = file.WriteString("export {};")
			_ = file.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("nothing read the pipe")
}

func TestARolloutStepDuringTheBuildStopsWaitingAtItsContextsAbortBeforeTheBuildEndsTheBuildGoesOn(t *testing.T) {
	entry, pipe := heldEntry(t)
	f := newBuildFixture(t, entry)
	built, err := f.plugins.Build(nil, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(chordctx.Background())
	defer cancel(nil)
	stepped := make(chan error, 1)
	go func() {
		_, err := f.building.step("web").Reload(built, map[string][]plugins.PluginScript{}, ctx)
		stepped <- err
	}()
	select {
	case err := <-stepped:
		t.Fatalf("the step ended before the build did: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	cancel(errors.New("cancel Apply"))
	if err := <-stepped; err == nil || !strings.Contains(err.Error(), "cancel Apply") {
		t.Fatalf("step = %v, want the abort", err)
	}
	if f.building.built() != nil {
		t.Fatal("the build ended with the step")
	}
	release(t, pipe)
	if outcome := f.building.build(); outcome.Error != nil {
		t.Fatalf("build = %+v, want built: the build went on", outcome)
	}
}

// The server's close fails a build in flight (Pi: "the server's close fails it"): the waiting request is answered 503 once the
// build's esbuild run ends, and nothing is published on the closed host.
func TestTheServersCloseFailsTheBuildInFlightItsWaitersAnswer503(t *testing.T) {
	entry, pipe := heldEntry(t)
	f := newBuildFixture(t, entry)
	answered := make(chan error, 1)
	go func() {
		response, err := f.building.file(WebPrefix+"base.0.js", context.Background())
		if err == nil && (response.Kind != http.KindError || response.Status != 503) {
			err = errors.New("the waiter was not answered 503")
		}
		answered <- err
	}()
	select {
	case err := <-answered:
		t.Fatalf("the request was answered before the build ended: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := f.server.Close(chordctx.Background()); err != nil {
		t.Fatal(err)
	}
	release(t, pipe)
	select {
	case err := <-answered:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the waiter was never answered after the close")
	}
	if outcome := f.building.build(); outcome.Error == nil {
		t.Fatal("a build the close ended reports built")
	}
}

// Forced Go difference: the close joins a build in flight. It returns once the build ended, not before (Pi's `#close` does not
// wait for the build's Promise).
func TestClosingASurfaceWaitsForItsBuildInFlight(t *testing.T) {
	entry, pipe := heldEntry(t)
	f := newBuildFixture(t, entry)
	f.building.start()
	closed := make(chan error, 1)
	go func() { closed <- f.building.close(context.Background()) }()
	// The build's esbuild run reads the pipe: it ends only when the pipe is written, after the close began waiting for it.
	release(t, pipe)
	if err := <-closed; err != nil {
		t.Fatalf("close = %v, want nil once the build ended", err)
	}
	if f.building.built() == nil {
		t.Fatal("close returned before the build ended")
	}
}

// A close whose context ended abandons the build in flight: it returns the context's error, the goroutine finishes on its own.
func TestClosingASurfaceAbandonsItsBuildInFlightWhenTheContextEnds(t *testing.T) {
	entry, pipe := heldEntry(t)
	f := newBuildFixture(t, entry)
	f.building.start()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// The pipe is not written: the build cannot have ended.
	if err := f.building.close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("close = %v, want the context's error", err)
	}
	if f.building.built() != nil {
		t.Fatal("the build ended while its pipe was held")
	}
	release(t, pipe)
	<-f.building.done
	if f.building.built() == nil {
		t.Fatal("the abandoned build never ended")
	}
}
