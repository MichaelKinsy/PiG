package entered

import (
	"bytes"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// spinSink keeps what the spinning goroutines allocate reachable, so the allocations are not removed.
var spinSink atomic.Pointer[[]byte]

// A goroutine that allocates without pause never waits for anything of the program's, but while a collection starts it waits on the
// world lock ("semacquire") and while one marks it waits for assist credit ("GC assist wait"). The runtime ends both on its own, so
// neither is a wait the goroutine has entered: Await would return for a goroutine that has yet to reach the wait it is awaited for.
// Parked took "GC assist wait" for parked, and, for the callers that may hold a lock (AwaitBlocked), "semacquire" too.
func TestParkedNeverReportsAGoroutineThatAllocatesWithoutPause(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(1))
	stop := make(chan struct{})
	ids := make(chan uint64, 1)
	var spinners atomic.Int32
	for range 4 {
		spinners.Add(1)
		go func() {
			defer spinners.Add(-1)
			select {
			case ids <- goroutineid.Current():
			default:
			}
			for {
				select {
				case <-stop:
					return
				default:
				}
				chunk := make([]byte, 4096)
				spinSink.Store(&chunk)
			}
		}()
	}
	id := <-ids
	defer func() {
		close(stop)
		for spinners.Load() != 0 {
			time.Sleep(time.Millisecond)
		}
	}()
	var stacks []byte
	for sample := range 2_000 {
		for _, locks := range []bool{false, true} {
			var parked, found bool
			if parked, found, stacks = parkedOrBlocked(id, stacks, locks); !found {
				t.Fatalf("sample %d: the goroutine that allocates is not in the dump", sample)
			} else if parked {
				start := bytes.Index(stacks, []byte("goroutine "+strconv.FormatUint(id, 10)+" ["))
				header, _, _ := bytes.Cut(stacks[max(start, 0):], []byte("\n"))
				t.Fatalf("sample %d (locks %v): reported parked while it allocates without pause: %s", sample, locks, header)
			}
		}
	}
}

func TestGoWaitsUntilTheGoroutineParksOrReturns(t *testing.T) {
	release := make(chan struct{})
	Go(func() { <-release })() // returns once the goroutine waits on release
	close(release)

	var ran atomic.Bool
	Go(func() { ran.Store(true) })()
	if !ran.Load() {
		t.Fatal("wait returned before a function with no wait ran to its end")
	}
}

func TestAwaitReturnsOnceAWokenGoroutineWaitsAgain(t *testing.T) {
	ids := make(chan uint64, 1)
	wake, again := make(chan struct{}), make(chan struct{})
	var resumed atomic.Bool
	go func() {
		ids <- goroutineid.Current()
		<-wake
		resumed.Store(true)
		<-again
	}()
	id := <-ids
	close(wake)
	Await(id, nil)
	if !resumed.Load() {
		t.Fatal("Await returned before the woken goroutine ran to its next wait")
	}
	close(again)
}

func TestParkedReportsAnExitedGoroutineAsNotFound(t *testing.T) {
	ids := make(chan uint64, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ids <- goroutineid.Current()
	}()
	id := <-ids
	<-done
	for range 100 {
		if _, found, _ := Parked(id, nil); !found {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("an exited goroutine is still found")
}

// A wait observed while busy reports true is not the goroutine's own: AwaitUnless keeps polling until busy clears.
func TestAwaitUnlessIgnoresWaitsWhileBusy(t *testing.T) {
	var busy atomic.Bool
	busy.Store(true)
	release := make(chan struct{})
	ids := make(chan uint64, 1)
	go func() {
		ids <- goroutineid.Current()
		<-release
	}()
	id := <-ids
	returned := make(chan struct{})
	go func() {
		AwaitUnless(id, nil, busy.Load)
		close(returned)
	}()
	select {
	case <-returned:
		t.Fatal("AwaitUnless returned while busy")
	case <-time.After(20 * time.Millisecond):
	}
	busy.Store(false)
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("AwaitUnless did not return once the goroutine's wait became its own")
	}
	close(release)
}

// className names a Class in a test failure.
func className(class Class) string { return [...]string{"Busy", "Lock", "Waiting"}[class] }

func TestClassifySeparatesTheRuntimesWaitsFromTheProgramsOwn(t *testing.T) {
	const frames = "main.allocate()\n\t/x.go:1 +0x1"
	const pollFrames = "internal/poll.runtime_Semacquire(0x1)\n\t/x.go:1 +0x1\ninternal/poll.(*fdMutex).rwlock(0x1)\n\t/y.go:2 +0x2"
	for _, test := range []struct {
		header, frames string
		want           Class
	}{
		{"running]:", frames, Busy},
		{"runnable]:", frames, Busy},
		{"syscall, 2 minutes]:", frames, Busy},
		{"copystack]:", frames, Busy},
		{"preempted]:", frames, Busy},
		{"semacquire]:", frames, Busy},
		{"GC assist wait]:", frames, Busy},
		{"GC assist marking]:", frames, Busy},
		{"garbage collection]:", frames, Busy},
		{"garbage collection scan]:", frames, Busy},
		{"GC mark termination]:", frames, Busy},
		{"stopping the world]:", frames, Busy},
		{"flushing proc caches]:", frames, Busy},
		{"wait for GC cycle]:", frames, Busy},
		{"GC weak to strong wait]:", frames, Busy},
		// The lock of a file descriptor another goroutine reads is not the runtime's to end.
		{"semacquire]:", pollFrames, Lock},
		{"sync.Mutex.Lock]:", frames, Lock},
		{"sync.RWMutex.Lock, 3 minutes]:", frames, Lock},
		{"sync.RWMutex.RLock]:", frames, Lock},
		{"chan receive]:", frames, Waiting},
		{"chan send]:", frames, Waiting},
		{"select, locked to thread]:", frames, Waiting},
		{"select (no cases)]:", frames, Waiting},
		{"sleep]:", frames, Waiting},
		{"IO wait]:", frames, Waiting},
		{"sync.WaitGroup.Wait]:", frames, Waiting},
		{"sync.Cond.Wait]:", frames, Waiting},
		{"GC worker (idle)]:", frames, Waiting},
		{"]:", frames, Waiting},
	} {
		if got := Classify([]byte(test.header), []byte(test.frames)); got != test.want {
			t.Errorf("Classify(%q, %.20q...) = %s, want %s", test.header, test.frames, className(got), className(test.want))
		}
	}
}

// A goroutine that needs the lock of a file descriptor another goroutine is reading waits in "semacquire" too, but nothing the
// runtime does ends that wait: it stays a lock (not parked, unless the caller may hold the lock) however Classify takes the
// collection's own.
func TestParkedKeepsAFileDescriptorLockAsALock(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ids := make(chan uint64, 2)
	var ended sync.WaitGroup
	for range 2 {
		ended.Go(func() {
			ids <- goroutineid.Current()
			_, _ = reader.Read(make([]byte, 1))
		})
	}
	defer func() {
		_ = writer.Close()
		ended.Wait()
		_ = reader.Close()
	}()
	first, second := <-ids, <-ids
	deadline := time.Now().Add(5 * time.Second)
	var stacks []byte
	for {
		// One reader waits for the pipe's data (parked in both modes); the other waits for the first one's read lock.
		var dataWait, lockWait bool
		for _, id := range []uint64{first, second} {
			var parked, blocked bool
			parked, _, stacks = Parked(id, stacks)
			blocked, _, stacks = parkedOrBlocked(id, stacks, true)
			switch {
			case parked && blocked:
				dataWait = true
			case !parked && blocked:
				lockWait = true
			}
		}
		if dataWait && lockWait {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reader waits for the file descriptor's lock as a lock (a reader waits for the data: %v, a reader waits for the lock: %v)", dataWait, lockWait)
		}
		time.Sleep(time.Millisecond)
	}
}

// The runtime stops a goroutine for the execution tracer and for a heap dump too, and ends those stops itself, but only a
// goroutine of a test run with -trace or of a heap dump is ever in them.
func TestClassifyTakesTheTracerAndHeapDumpStopsForBusy(t *testing.T) {
	for _, header := range []string{"trace goroutine status]:", "trace proc status]:", "page trace flush]:", "dumping heap]:"} {
		if got := Classify([]byte(header), []byte("main.f()\n\t/x.go:1 +0x1")); got != Busy {
			t.Errorf("Classify(%q) = %s, want Busy", header, className(got))
		}
	}
}

// Only the waits of a file descriptor's lock are Lock, not any goroutine with an internal/poll frame: poll code that allocates
// waits for a collection's world lock in "semacquire" with its frames on the stack. The lock wait of an fd is in
// internal/poll.(*fdMutex).rwlock (a read or write that waits for another's), and FD.Close waits for the reads it evicted in
// internal/poll.runtime_Semacquire.
func TestClassifyTakesOnlyAFileDescriptorLocksFramesForALock(t *testing.T) {
	for _, test := range []struct {
		name, frames string
		want         Class
	}{
		{"a read allocating in poll code", "reflect.unsafe_New(0x1)\n\t/malloc.go:1 +0x1\ninternal/poll.(*FD).Read(0x1, {0x2, 0x3})\n\t/fd_unix.go:1 +0x1\nos.(*File).read(0x1)\n\t/file.go:1 +0x1", Busy},
		{"a pollDesc wait allocating", "main.f()\n\t/x.go:1 +0x1\ninternal/poll.(*pollDesc).wait(0x1)\n\t/fd_poll_runtime.go:1 +0x1", Busy},
		{"the lock of a read", "internal/poll.runtime_Semacquire(0x1)\n\t/sema.go:1 +0x1\ninternal/poll.(*fdMutex).rwlock(0x1, 0x1)\n\t/fd_mutex.go:1 +0x1\ninternal/poll.(*FD).readLock(0x1)\n\t/fd_unix.go:1 +0x1", Lock},
		{"the close of an fd", "internal/poll.runtime_Semacquire(0x1)\n\t/sema.go:1 +0x1\ninternal/poll.(*FD).Close(0x1)\n\t/fd_unix.go:1 +0x1", Lock},
	} {
		if got := Classify([]byte("semacquire]:"), []byte(test.frames)); got != test.want {
			t.Errorf("%s: Classify = %s, want %s", test.name, className(got), className(test.want))
		}
	}
}

// A header's state ends at the first " (" too: a goroutine whose stack a collection scans is "running (scan)", and one the
// runtime found leaked is "chan receive (leaked)", inside the brackets.
func TestClassifyReadsTheStateBeforeItsSuffixes(t *testing.T) {
	const frames = "main.f()\n\t/x.go:1 +0x1"
	for _, test := range []struct {
		header string
		want   Class
	}{
		{"running (scan)]:", Busy},
		{"runnable (scan)]:", Busy},
		{"running (scan), locked to thread]:", Busy},
		{"GC assist wait (scan)]:", Busy},
		{"semacquire (scan)]:", Busy},
		{"sync.Mutex.Lock (leaked)]:", Lock},
		{"sync.RWMutex.RLock (leaked), 4 minutes]:", Lock},
		{"chan receive (leaked)]:", Waiting},
		{"select (scan)]:", Waiting},
	} {
		if got := Classify([]byte(test.header), []byte(frames)); got != test.want {
			t.Errorf("Classify(%q) = %s, want %s", test.header, className(got), className(test.want))
		}
	}
}
