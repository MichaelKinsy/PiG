package entered

import (
	"bytes"
	"runtime/debug"
	"strconv"
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
