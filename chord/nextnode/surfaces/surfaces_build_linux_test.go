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
	if outcome := f.building.build(); outcome.Err != nil {
		t.Fatalf("build = %+v, want built: the build went on", outcome)
	}
}
