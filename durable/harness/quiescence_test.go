package harness

import (
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// quiescenceSink keeps what allocatingGoroutine allocates reachable, so the allocations are not removed.
var quiescenceSink atomic.Pointer[[]byte]

// A goroutine that allocates while a collection runs waits for the runtime: for the world lock while one starts, for assist
// credit while one marks. The dump shows those waits as parked, so a barrier that counted them so returned while a task goroutine
// that was only waiting for the runtime had yet to commit, and the idle-state test then read the state before the commit's
// publication (TestIncrementalIdleStateEqualsAFullRecomputeAfterEveryStepOfRandomNestedWork). A goroutine that never parks is
// never quiescent, however often the barrier looks.
func TestActiveGoroutinesCountsAGoroutineThatNeverParks(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(1))
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			chunk := make([]byte, 4096)
			quiescenceSink.Store(&chunk)
		}
	}()
	defer func() { close(stop); <-done }()
	self := goroutineid.Current()
	var buffer []byte
	for snapshot := range 10_000 {
		var active int
		if active, buffer = activeGoroutines(self, buffer); active == 0 {
			t.Fatalf("snapshot %d took a goroutine that allocates without pause for parked:\n%s", snapshot, buffer)
		}
	}
}

func TestActiveGoroutineClassifiesDumpStates(t *testing.T) {
	const self = 7
	for _, test := range []struct {
		name  string
		block string
		want  bool
	}{
		{"running", "goroutine 8 [running]:\nmain.f()\n\t/x.go:1 +0x1", true},
		{"runnable", "goroutine 8 [runnable]:\nmain.f()\n\t/x.go:1 +0x1", true},
		{"system call", "goroutine 8 [syscall, 2 minutes]:\nsyscall.read()\n\t/x.go:1 +0x1", true},
		{"preempted", "goroutine 8 [preempted]:\nmain.f()\n\t/x.go:1 +0x1", true},
		{"world lock of a collection", "goroutine 8 [semacquire]:\nmain.allocate()\n\t/x.go:1 +0x1", true},
		{"assist credit of a collection", "goroutine 8 [GC assist wait]:\nmain.allocate()\n\t/x.go:1 +0x1", true},
		{"assist work of a collection", "goroutine 8 [GC assist marking]:\nmain.allocate()\n\t/x.go:1 +0x1", true},
		{"stop of the world", "goroutine 8 [stopping the world]:\nruntime.GC()\n\t/x.go:1 +0x1", true},
		{"wait for a collection", "goroutine 8 [wait for GC cycle]:\nruntime.GC()\n\t/x.go:1 +0x1", true},
		{"file descriptor lock", "goroutine 8 [semacquire]:\ninternal/poll.runtime_Semacquire(0x1)\n\t/x.go:1 +0x1\ninternal/poll.(*fdMutex).rwlock(0x1)\n\t/y.go:2 +0x2", false},
		{"channel", "goroutine 8 [chan receive]:\nmain.f()\n\t/x.go:1 +0x1", false},
		{"select", "goroutine 8 [select, 3 minutes]:\nmain.f()\n\t/x.go:1 +0x1", false},
		{"sleep", "goroutine 8 [sleep]:\ntime.Sleep(0x1)\n\t/x.go:1 +0x1", false},
		{"mutex", "goroutine 8 [sync.Mutex.Lock]:\nsync.(*Mutex).Lock(0x1)\n\t/x.go:1 +0x1", false},
		{"wait group", "goroutine 8 [sync.WaitGroup.Wait]:\nsync.(*WaitGroup).Wait(0x1)\n\t/x.go:1 +0x1", false},
		{"IO wait", "goroutine 8 [IO wait]:\ninternal/poll.(*pollDesc).wait(0x1)\n\t/x.go:1 +0x1", false},
		{"idle collector worker", "goroutine 8 [GC worker (idle)]:\nruntime.gcBgMarkWorker()\n\t/x.go:1 +0x1", false},
		{"the caller", "goroutine 7 [running]:\nmain.f()\n\t/x.go:1 +0x1", false},
		{"not a goroutine", "main.f()\n\t/x.go:1 +0x1", false},
	} {
		if got := activeGoroutine(self, []byte(test.block)); got != test.want {
			t.Errorf("%s: active = %v, want %v", test.name, got, test.want)
		}
	}
}

// The barrier still returns while goroutines are parked in the waits of the program, among them one that waits for a file
// descriptor another goroutine reads ("semacquire", as the collection's world lock is, told apart by its internal/poll frames).
func TestAwaitQuiescentReturnsWithGoroutinesParkedInTheProgramsWaits(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var (
		release = make(chan struct{})
		never   chan struct{}
		mu      sync.Mutex
		group   sync.WaitGroup
		cond    = sync.NewCond(&sync.Mutex{})
		met     bool
		ended   sync.WaitGroup
	)
	mu.Lock()
	group.Add(1)
	for _, wait := range []func(){
		func() { <-release },
		func() {
			select {
			case <-release:
			case <-never:
			}
		},
		func() { time.Sleep(20 * time.Millisecond) },
		func() { mu.Lock(); defer mu.Unlock() },
		func() { group.Wait() },
		func() {
			cond.L.Lock()
			for !met {
				cond.Wait()
			}
			cond.L.Unlock()
		},
		func() { _, _ = reader.Read(make([]byte, 1)) },
		func() { _, _ = reader.Read(make([]byte, 1)) },
	} {
		ended.Go(wait)
	}
	defer func() {
		close(release)
		mu.Unlock()
		group.Done()
		cond.L.Lock()
		met = true
		cond.L.Unlock()
		cond.Broadcast()
		_ = writer.Close()
		ended.Wait()
		_ = reader.Close()
	}()
	awaitQuiescent(t)
	buffer := make([]byte, 1<<20)
	if dump := string(buffer[:runtime.Stack(buffer, true)]); !strings.Contains(dump, "internal/poll.(*fdMutex).rwlock") {
		t.Errorf("no goroutine waits for the file descriptor's lock, so the barrier was not tried against it:\n%s", dump)
	}
}
