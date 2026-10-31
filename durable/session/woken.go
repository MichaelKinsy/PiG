package session

// The goroutines a publication's wakes ready, which the writer's hop waits for.
//
// Pi's publication resolves promises inside the Session's observers, and their continuations start in the microtasks that run
// before the macrotask hop to the next publication. A Go wake is a close or a send that readies goroutines the Session does not
// know: the Harness's waiters, a task's cancelled context, an idle waiter. wakeAndEnter finds them by their state: the
// goroutines that were parked before the wake and are runnable or running after it.

import (
	"bytes"
	"context"
	"runtime"
	"strconv"

	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// maxOrderedGoroutines bounds the number of goroutines up to which the Session orders continuations by `entered`. Every wait
// for a goroutine to enter reads a dump of all goroutines, whose cost grows with their number; a process with thousands of
// goroutines (a Harness with as many running tasks) would spend its time in dumps. Above the bound the order of the continuations
// of what one publication resolved is best effort: the woken goroutines are given the processor once (Gosched), not waited for.
const maxOrderedGoroutines = 128

// orderingAffordable reports whether the number of goroutines allows to order continuations by dumps.
func orderingAffordable() bool { return runtime.NumGoroutine() <= maxOrderedGoroutines }

// awaitEntered returns once goroutine id has entered (parked in a wait, blocked on a lock, or exited) when ordering is
// affordable; else it only yields the processor.
func awaitEntered(ctx context.Context, id uint64) {
	if !orderingAffordable() {
		runtime.Gosched()
		return
	}
	_ = entered.AwaitBlocked(ctx, id, nil)
}

// wakeAndEnter runs wake, then returns once every goroutine that was parked before it and was readied by it has entered: it is
// parked in a wait again (or blocked on a lock) or has exited. It returns early when ctx ends.
func wakeAndEnter(ctx context.Context, wake func()) {
	if !orderingAffordable() {
		wake()
		runtime.Gosched()
		return
	}
	before, buffer := parkedGoroutines(nil)
	wake()
	after, _ := parkedGoroutines(buffer)
	self := goroutineid.Current()
	for id, wasParked := range before {
		if !wasParked || id == self {
			continue
		}
		if parked, found := after[id]; found && !parked {
			// Readied and not yet back in a wait: it runs up to its next wait before the next publication.
			if err := entered.AwaitBlocked(ctx, id, nil); err != nil {
				return
			}
		}
	}
}

// parkedGoroutines returns, for every goroutine, whether it is parked in a wait (a channel, a select, a timer, a lock, a
// condition) rather than running, runnable, or in a system call, as entered.Parked classifies them; buffer is reused as the dump
// buffer.
func parkedGoroutines(buffer []byte) (map[uint64]bool, []byte) {
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
	states := map[uint64]bool{}
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
		if err != nil {
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
			states[id] = false
		default:
			states[id] = true
		}
	}
	return states, buffer
}
