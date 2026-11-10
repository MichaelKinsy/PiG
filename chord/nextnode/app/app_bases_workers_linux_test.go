//go:build linux

package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports the two worker-kind tests of "bases built after the listen" of packages/chord/test/node/app/app.test.ts (Pi 284e63d9):
// `answers before a worker kind's base is built; a worker start waits for its base` and `a closed start's late worker-base build
// never touches the next start's directory`. Pi holds the worker base's build on a pipe the worker's entry imports; a Go worker's base
// is a copy of its entry (D600), so the entry is the pipe (`exec.LookPath` accepts a pipe with its execute bit): the copy reads it
// and waits until it is written, with the bytes of the program the worker runs.

// heldWorkerEntry is a named pipe that can be started, as a worker kind's entry.
func heldWorkerEntry(t *testing.T) string {
	t.Helper()
	pipe := filepath.Join(t.TempDir(), "worker")
	if err := syscall.Mkfifo(pipe, 0o700); err != nil {
		t.Fatal(err)
	}
	return pipe
}

// release writes text into the pipe a reader waits on and closes it (the write end made blocking first: it is a whole program).
func release(held *os.File, text []byte) {
	_ = held.Fd()
	_, _ = held.Write(text)
	_ = held.Close()
}

// recordedStarts is the address keys a worker kind's app `Start` was asked for, in order.
type recordedStarts struct {
	mu   sync.Mutex
	keys []string
}

func (r *recordedStarts) add(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, key)
}

func (r *recordedStarts) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.keys...)
}

// heldWorker is the tests' project worker kind behind entry, recording the keys its `Start` was asked for.
func heldWorker(t *testing.T, entry string) (AppWorkerKind, *recordedStarts) {
	t.Helper()
	starts := &recordedStarts{}
	worker := testWorker(t, project, "")
	worker.Entry = entry
	asked := worker.Start
	worker.Start = func(address contracts.Address, ctx context.Context) (delta.JsonValue, error) {
		starts.add(address.Key)
		return asked(address, ctx)
	}
	return worker, starts
}

func TestAppAnswersBeforeAWorkerKindsBaseIsBuiltAWorkerStartWaitsForItsBase(t *testing.T) {
	program, err := os.ReadFile(testWorker(t, project, "").Entry)
	if err != nil {
		t.Fatal(err)
	}
	pipe := heldWorkerEntry(t)
	worker, starts := heldWorker(t, pipe)
	s := startUnbuilt(t, map[string]map[string]string{"notes": notesPlugin}, func(o *AppOptions) { o.Workers = []AppWorkerKind{worker} })
	if got := surfaceGet(t, s.port, "/_chord/owner", nil).status; got != 200 {
		t.Fatalf("owner page = %d, want 200 before the worker base is built", got)
	}
	held, err := s.app.Host.Hold(background, notes, delta.JsonObjectOf("id", "p1"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	writer := untilReading(t, pipe)
	// A reader waits on the pipe: the start is held on the base's build, so the app's start data cannot have been asked for.
	if got := starts.all(); len(got) != 0 {
		t.Fatalf("starts = %v before the base was built, want none", got)
	}
	if !stillOpen(s.app.BasesBuilt()) {
		t.Fatal("the bases are built while the worker kind's entry is held")
	}
	// The entry becomes the program itself (a file of the same bytes at the entry's path) before the build's read ends: the build
	// holds the pipe it opened, and the watch of the entry compared once the base is built reads this file, not a pipe nobody writes.
	replacement := pipe + ".program"
	if err := os.WriteFile(replacement, program, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, pipe); err != nil {
		t.Fatal(err)
	}
	release(writer, program)
	eventually(t, "the held address live", func() bool { return held.State().Value().Kind == host.RefStateLive })
	if got := starts.all(); !reflect.DeepEqual(got, []string{"project|p1"}) {
		t.Fatalf("starts = %v, want the one after the build", got)
	}
	<-s.app.BaseWatched()
}

func TestAppAClosedStartsLateWorkerBaseBuildNeverTouchesTheNextStartsDirectoryItsBaseStaysAsItWas(t *testing.T) {
	program, err := os.ReadFile(testWorker(t, project, "").Entry)
	if err != nil {
		t.Fatal(err)
	}
	plugins := map[string]map[string]string{"notes": notesPlugin}
	pipe := heldWorkerEntry(t)
	oldWorker, _ := heldWorker(t, pipe)
	old := startUnbuilt(t, plugins, func(o *AppOptions) { o.Workers = []AppWorkerKind{oldWorker} })
	// The old start's build reads the pipe (an open for writing succeeds now); it is held until the pipe is written.
	writer := untilReading(t, pipe)
	closed := make(chan error, 1)
	go func() { closed <- old.app.Close(background) }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		release(writer, program)
		t.Fatal("the close waited for the worker base's build")
	}
	// The next start of the data directory: its worker kind's base is written into cache/workers/project/.
	next := startTestApp(t, nil, func(o *AppOptions) {
		o.DataDir, o.Root = old.dataDir, old.root
		o.Workers = []AppWorkerKind{testWorker(t, project, "")}
	})
	base := filepath.Join(old.dataDir, "cache", "workers", "project")
	var copied string
	for _, name := range dirEntries(t, base) {
		copied = filepath.Join(base, name)
	}
	if got := dirEntries(t, base); len(got) != 1 {
		t.Fatalf("the next start's base directory = %v, want its one copy", got)
	}
	before, err := os.Stat(copied)
	if err != nil {
		t.Fatal(err)
	}
	// The old start's build ends now: what it finishes after its close is dropped.
	release(writer, program)
	<-old.app.BasesBuilt()
	after, err := os.Stat(copied)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("the next start's copy after the old build ended: %v, %v, want the same file", after, err)
	}
	if got := dirEntries(t, filepath.Dir(base)); !reflect.DeepEqual(got, []string{"project"}) {
		t.Fatalf("cache/workers = %v, want only the next start's directory", got)
	}
	if got := dirEntries(t, base); len(got) != 1 {
		t.Fatalf("the next start's base directory = %v after the old build ended, want its one copy", got)
	}
	<-next.app.BaseWatched()
}
