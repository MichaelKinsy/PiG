package harness

// awaitQuiescent is the barrier behind `await flush()` of upstream's tests (a macrotask turn: every ready microtask has run).
//
// Upstream runs on one thread, so when a macrotask starts, everything that was ready has run up to its next await. The Go
// equivalent is a moment when no goroutine is running, runnable, or in a system call: each one has run up to a wait and
// nothing it waits for has happened yet. It reads the state of every goroutine from a goroutine dump, which stops the world,
// so one snapshot with only parked goroutines is stable until something outside the process (a timer, the test) acts. It
// waits for that and never for a duration.
//
// A goroutine the Go runtime itself has stopped is not parked in a wait of the program's (runtimeWaits): a goroutine that
// allocates while a collection starts waits on the world lock, and one that allocates while a collection marks waits for
// assist credit. The runtime ends both without anything outside the process acting, but the dump shows them as waits, so a
// barrier that took them for parked returned while a task goroutine was still on its way to its commit.

import (
	"bytes"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// quiescenceTimeout bounds the wait for quiescence: a goroutine that never parks fails the test instead of hanging it.
const quiescenceTimeout = 30 * time.Second

// runtimeWaits are the dump states of a goroutine the Go runtime has stopped for its own work (a collection's start, mark and
// termination, a stop of the world) and ends itself. "semacquire" is one too, unless internal/poll holds the goroutine on a
// file descriptor (activeGoroutine). Waits of the program (channels, select, sleep, sync primitives, IO wait) are not here.
var runtimeWaits = map[string]bool{
	"GC assist marking":       true,
	"GC assist wait":          true,
	"garbage collection":      true,
	"garbage collection scan": true,
	"GC mark termination":     true,
	"stopping the world":      true,
	"flushing proc caches":    true,
	"wait for GC cycle":       true,
	"GC weak to strong wait":  true,
}

// activeGoroutines counts the goroutines other than self that are running, runnable, in a system call or stopped by the
// runtime (runtimeWaits); buffer is reused as the dump buffer.
func activeGoroutines(self uint64, buffer []byte) (int, []byte) {
	if cap(buffer) < 64<<10 {
		buffer = make([]byte, 64<<10)
	}
	buffer = buffer[:cap(buffer)]
	for {
		n := runtime.Stack(buffer, true)
		if n < len(buffer) {
			buffer = buffer[:n]
			break
		}
		buffer = make([]byte, 2*len(buffer))
	}
	active := 0
	for block := range bytes.SplitSeq(buffer, []byte("\n\n")) {
		if activeGoroutine(self, block) {
			active++
		}
	}
	return active, buffer
}

// activeGoroutine reports whether the goroutine of block, one entry of a goroutine dump, is running, runnable, in a system call
// or stopped by the runtime, and is not self.
func activeGoroutine(self uint64, block []byte) bool {
	header, frames, _ := bytes.Cut(block, []byte("\n"))
	header, found := bytes.CutPrefix(header, []byte("goroutine "))
	if !found {
		return false
	}
	idText, state, found := bytes.Cut(header, []byte(" "))
	if !found {
		return false
	}
	id, err := strconv.ParseUint(string(idText), 10, 64)
	if err != nil || id == self {
		return false
	}
	state, found = bytes.CutPrefix(state, []byte("["))
	if !found {
		return false
	}
	if end := bytes.IndexAny(state, ",]"); end >= 0 {
		state = state[:end]
	}
	switch string(state) {
	case "running", "runnable", "syscall", "copystack", "preempted":
		return true
	case "semacquire":
		// The collection's world lock, but also a goroutine that waits for a file descriptor another goroutine reads, which
		// only that read's data ends: the frames of internal/poll tell them apart, as the runtime's own are not shown.
		return !bytes.Contains(frames, []byte("internal/poll."))
	}
	return runtimeWaits[string(state)]
}

// awaitQuiescent returns once every goroutine but the caller is parked in a wait.
func awaitQuiescent(t testing.TB) {
	t.Helper()
	self := goroutineid.Current()
	var buffer []byte
	deadline := time.Now().Add(quiescenceTimeout)
	for attempt := 0; ; attempt++ {
		var active int
		if active, buffer = activeGoroutines(self, buffer); active == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines still ran after %s", active, quiescenceTimeout)
		}
		if attempt < 100 {
			runtime.Gosched()
		} else {
			time.Sleep(100 * time.Microsecond)
		}
	}
}
