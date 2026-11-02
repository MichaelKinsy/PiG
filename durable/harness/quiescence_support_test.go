package harness

// awaitQuiescent is the barrier behind `await flush()` of upstream's tests (a macrotask turn: every ready microtask has run).
//
// Upstream runs on one thread, so when a macrotask starts, everything that was ready has run up to its next await. The Go
// equivalent is a moment when no goroutine is running, runnable, or in a system call: each one has run up to a wait and
// nothing it waits for has happened yet. It reads the state of every goroutine from a goroutine dump, which stops the world,
// so one snapshot with only parked goroutines is stable until something outside the process (a timer, the test) acts. It
// waits for that and never for a duration.

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

// activeGoroutines counts the goroutines other than self that are running, runnable or in a system call; buffer is reused as
// the dump buffer.
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
	header := []byte("goroutine ")
	rest := buffer
	for len(rest) > 0 {
		line := rest
		if end := bytes.IndexByte(rest, '\n'); end >= 0 {
			line, rest = rest[:end], rest[end+1:]
		} else {
			rest = nil
		}
		if !bytes.HasPrefix(line, header) {
			continue
		}
		line = line[len(header):]
		space := bytes.IndexByte(line, ' ')
		if space < 0 {
			continue
		}
		id, err := strconv.ParseUint(string(line[:space]), 10, 64)
		if err != nil || id == self {
			continue
		}
		state := line[space+1:]
		if len(state) == 0 || state[0] != '[' {
			continue
		}
		state = state[1:]
		if end := bytes.IndexAny(state, ",]"); end >= 0 {
			state = state[:end]
		}
		switch string(state) {
		case "running", "runnable", "syscall", "copystack", "preempted":
			active++
		}
	}
	return active, buffer
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
