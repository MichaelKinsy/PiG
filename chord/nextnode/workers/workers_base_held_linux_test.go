//go:build linux

package workers

import (
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// The tests of the worker base's build that hold it on a named pipe (Pi's mkfifo, Linux only). Pi holds the bundle's build with a
// file the entry imports; a Go worker's base is a copy of its entry (D600), so the entry itself is the pipe: the copy reads it and
// waits until it is written. `exec.LookPath` accepts a pipe with its execute bit, as the base's check of the entry.

// heldEntry is a named pipe that can be started, as a worker's entry: its copy waits until the pipe is written.
func heldEntry(t *testing.T) string {
	t.Helper()
	pipe := filepath.Join(t.TempDir(), "worker")
	if err := syscall.Mkfifo(pipe, 0o700); err != nil {
		t.Fatal(err)
	}
	return pipe
}

// untilReading returns the pipe's write end once a reader waits on it (a non-blocking open for writing succeeds then); the caller
// writes it and closes it.
func untilReading(t *testing.T, pipe string) *os.File {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if file, err := os.OpenFile(pipe, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			return file
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("nothing read the pipe")
	return nil
}

// release ends the pipe's read: the bytes a copy of it takes (the write end is made blocking first: it can be a whole program).
func release(held *os.File, text string) {
	_ = held.Fd()
	_, _ = held.WriteString(text)
	_ = held.Close()
}

// namedWithOthersFile is a base directory another start owns: it holds a file the component must leave as it is.
func namedWithOthersFile(t *testing.T) (named, others string) {
	t.Helper()
	named = filepath.Join(t.TempDir(), "bases", "worker")
	if err := os.MkdirAll(named, 0o700); err != nil {
		t.Fatal(err)
	}
	others = filepath.Join(named, "worker")
	if err := os.WriteFile(others, []byte("another start's"), 0o600); err != nil {
		t.Fatal(err)
	}
	return named, others
}

// Pi's `a start waits for the base's build` with its wait ended by the close (workers.ts `awaitWithContext(this.#built(),
// this.#closingContext)` in the start, and `close` not waiting for the build): a start held on the base's build ends when the
// component closes, before the build did; the app's start data was never asked for; a build that ends after the close writes nothing
// and its outcome is dropped (the directory is another start's by then).
func TestTheWorkerBaseACloseDoesNotWaitForTheBasesBuildAStartWaitingForItEndsAtTheCloseTheLateBuildWritesNothing(t *testing.T) {
	pipe := heldEntry(t)
	named, others := namedWithOthersFile(t)
	f := newFleet(t, fleetOptions{entry: pipe, baseDir: named})
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	held := untilReading(t, pipe)
	if f.workers.Built() != nil {
		t.Fatal("built while its entry is held")
	}
	closed := make(chan error, 1)
	go func() { closed <- f.workers.Close(background) }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		release(held, "late")
		t.Fatal("Close waited for the base's build")
	}
	if starts := f.starts.all(); len(starts) != 0 {
		t.Fatalf("starts = %v, want none: the base was never built", starts)
	}
	// The build's read ends only when the pipe is written; what it ends with after the close is dropped.
	release(held, "late")
	if built := f.workers.Build(); built.Err == nil {
		t.Fatalf("build = %+v, want the closed component's failure", built)
	}
	if got := mustRead(t, others); string(got) != "another start's" {
		t.Fatalf("the named directory's file = %q after the close and the late build, want it as it was", got)
	}
	if got := listDir(t, named); !reflect.DeepEqual(got, []string{"worker"}) {
		t.Fatalf("the named directory = %v, want only the other start's file", got)
	}
	if got := listDir(t, filepath.Dir(named)); !reflect.DeepEqual(got, []string{"worker"}) {
		t.Fatalf("beside the named directory = %v, want nothing left by the late build", got)
	}
	if kind := refKind(notes); kind == host.RefStateLive {
		t.Fatalf("the hold is %s after the close", kind)
	}
}

// The build is shared by every waiter, and a waiter's own abort ends only its wait: a start that waited for a held build starts
// the worker once the build ends (the app's start data asked for then, not before).
func TestTheWorkerBaseAStartWaitsForAHeldBuildAndForksOnceItEndedTheStartDataIsAskedForThen(t *testing.T) {
	// The entry is the fixture's program behind a pipe: the copy reads the pipe, and the bytes written are the program's.
	program := mustRead(t, fixtureEntry(t))
	pipe := heldEntry(t)
	f := newFleet(t, fleetOptions{entry: pipe})
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	held := untilReading(t, pipe)
	// A reader waits on the pipe: the start is held on the base's build, so the app's start data cannot have been asked for.
	if starts := f.starts.all(); len(starts) != 0 {
		t.Fatalf("starts = %v before the base was built, want none", starts)
	}
	if refKind(notes) == host.RefStateLive || f.workers.Built() != nil {
		t.Fatal("started or built before the pipe was written")
	}
	release(held, string(program))
	until(t, func() bool { return refKind(notes) == host.RefStateLive })
	if starts := f.starts.all(); !reflect.DeepEqual(starts, []string{"session|S1"}) {
		t.Fatalf("starts = %v, want one, after the build", starts)
	}
	if built := f.workers.Built(); built == nil || built.Err != nil {
		t.Fatalf("built = %+v", built)
	}
}

// A closed component copies nothing: its entry is not opened (a pipe nobody writes would hold the open), and its build ends at once.
func TestTheWorkerBaseAComponentThatClosedOpensNotItsEntry(t *testing.T) {
	pipe := heldEntry(t)
	f := newFleet(t, fleetOptions{entry: pipe, baseDir: filepath.Join(t.TempDir(), "bases", "worker")})
	if err := f.workers.Close(background); err != nil {
		t.Fatal(err)
	}
	done := make(chan WorkersBuilt, 1)
	go func() { done <- f.workers.Build() }()
	select {
	case built := <-done:
		if built.Err == nil {
			t.Fatal("a closed component built")
		}
	case <-time.After(3 * time.Second):
		if w, err := os.OpenFile(pipe, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
		<-done
		t.Fatal("a closed component opened its entry to copy it")
	}
}
