// Package entered is the shared mechanism that keeps upstream's start and resume order across goroutines.
//
// Upstream code runs on one JavaScript event loop. A function it starts runs synchronously up to its first await
// before the next one starts, and continuations of promises settled together run in settlement order, each up to
// its next await. A Go port that starts or wakes several goroutines at once loses that order: the runtime runs them
// in no fixed order and, through its runnext slot, often runs the most recently readied first. This package lets a
// caller wait until a goroutine it started or woke has "entered": it is parked in a wait, or it has exited, so the
// next one can be started or woken in upstream's order.
//
// A goroutine's state is read from a goroutine dump, polled after a short grace period with exponential backoff.
// A sync.Mutex, sync.RWMutex or semaphore wait is not a wait here, except for AwaitBlocked: upstream code cannot block
// on a lock, so a lock in Go code is part of its synchronous work, unless the caller may hold the lock. Nor is a wait the Go
// runtime itself ends, such as the one of a goroutine that allocates while a collection runs: the goroutine is still on its
// way to the wait the program gave it. Classify is the one place that tells these apart.
package entered

import (
	"bytes"
	"context"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// Polling bounds: the first poll after grace, then backoff doubling up to maxDelay.
const (
	grace    = 50 * time.Microsecond
	maxDelay = 2 * time.Millisecond
)

// Go runs fn on a new goroutine and returns wait, which blocks until fn has entered: it returned, or its goroutine
// is parked in a wait. wait may be called at most once; a caller that never calls it pays no polling.
func Go(fn func()) (wait func()) {
	ids := make(chan uint64, 1)
	done := make(chan struct{})
	go func() {
		ids <- goroutineid.Current()
		defer close(done)
		fn()
	}()
	return func() { Await(<-ids, done) }
}

// Await blocks until goroutine id is parked in a wait or has exited, or done (which may be nil) is closed. Called
// right after a send or close that readied id, it returns once id has run up to its next wait.
func Await(id uint64, done <-chan struct{}) { AwaitUnless(id, done, nil) }

// AwaitUnless is Await, except that a wait id is in while busy reports true is not counted: busy marks a wait that is
// not the goroutine's own (for example a hand-off of its work to another goroutine that runs it). busy may be nil.
func AwaitUnless(id uint64, done <-chan struct{}, busy func() bool) {
	delay := grace
	timer := time.NewTimer(delay)
	defer timer.Stop()
	stacks := buffers.Get().(*[]byte)
	defer putBuffer(stacks)
	for {
		select {
		case <-done:
			return
		case <-timer.C:
		}
		if busy != nil && busy() {
			timer.Reset(delay)
			continue
		}
		var parked, found bool
		if parked, found, *stacks = Parked(id, *stacks); !found || parked && (busy == nil || !busy()) {
			return
		}
		delay = min(2*delay, maxDelay)
		timer.Reset(delay)
	}
}

// AwaitBlocked is Await for a caller that may itself hold a lock the goroutine needs, and that stops waiting when ctx is
// done. Besides a goroutine parked in a wait, one waiting for a sync.Mutex, sync.RWMutex or semaphore has entered: it has
// run up to a wait, and the caller, which cannot tell whose lock it is, must not wait for it. It returns the cause of ctx
// when ctx ended the wait, and nil otherwise.
func AwaitBlocked(ctx context.Context, id uint64, done <-chan struct{}) error {
	delay := grace
	timer := time.NewTimer(delay)
	defer timer.Stop()
	stacks := buffers.Get().(*[]byte)
	defer putBuffer(stacks)
	for {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-timer.C:
		}
		var parked, found bool
		if parked, found, *stacks = parkedOrBlocked(id, *stacks, true); !found || parked {
			return nil
		}
		delay = min(2*delay, maxDelay)
		timer.Reset(delay)
	}
}

// buffers are the pollers' goroutine dump buffers, reused across waits.
var buffers = sync.Pool{New: func() any { return new([]byte) }}

// hint is the size of the last full goroutine dump, so a poller's buffer starts big enough.
var hint atomic.Int64

// putBuffer returns a dump buffer to buffers unless it is more than twice the size the last dump needs (and over
// Parked's 16 KiB floor): a buffer grown for a burst of goroutines is dropped once the dumps shrink.
func putBuffer(stacks *[]byte) {
	if cap(*stacks) <= max(2*int(hint.Load()), 16<<10) {
		buffers.Put(stacks)
	}
}

// Parked reports whether goroutine id is parked in a wait and whether it exists, reusing (and returning) stacks as
// its buffer. It is not parked while Busy: running, runnable, in a system call (a synchronous system call is work, as
// upstream's synchronous I/O) or stopped by the Go runtime; nor while waiting for a lock.
func Parked(id uint64, stacks []byte) (parked, found bool, buffer []byte) {
	return parkedOrBlocked(id, stacks, false)
}

// parkedOrBlocked is Parked; with locks, a goroutine waiting for a lock is parked too.
func parkedOrBlocked(id uint64, stacks []byte, locks bool) (parked, found bool, buffer []byte) {
	if want := int(hint.Load()); cap(stacks) < want {
		stacks = make([]byte, want)
	}
	if len(stacks) < 16<<10 {
		stacks = make([]byte, 16<<10)
	}
	stacks = stacks[:cap(stacks)]
	for {
		n := runtime.Stack(stacks, true)
		if n < len(stacks) {
			hint.Store(int64(n + n/4))
			stacks = stacks[:n]
			break
		}
		stacks = make([]byte, 2*len(stacks))
	}
	header := []byte("goroutine " + strconv.FormatUint(id, 10) + " [")
	start := bytes.Index(stacks, header)
	if start == -1 {
		return false, false, stacks
	}
	// The dump's entry of the goroutine: its header line, then its frames, up to the blank line before the next entry.
	entry := stacks[start:]
	if end := bytes.Index(entry, []byte("\n\n")); end != -1 {
		entry = entry[:end]
	}
	state, frames, _ := bytes.Cut(entry[len(header):], []byte("\n"))
	switch Classify(state, frames) {
	case Busy:
		return false, true, stacks
	case Lock:
		return locks, true, stacks
	}
	return true, true, stacks
}

// Class is how a goroutine dump shows a goroutine that is awaited to enter a wait of the program's.
type Class uint8

const (
	// Busy is a goroutine that has yet to reach a wait of the program's: it is running, runnable, in a system call, or stopped by
	// the Go runtime for its own work and goes on without anything outside the process acting. A goroutine that allocates while
	// a collection starts waits on the world lock ("semacquire"), one that allocates while it marks waits for assist credit ("GC
	// assist wait"), and the other runtime states are the stages of a collection and of a stop of the world.
	Busy Class = iota
	// Lock is a goroutine waiting for a sync.Mutex, a sync.RWMutex or the lock of a file descriptor another goroutine is using.
	Lock
	// Waiting is a goroutine in any other wait: a channel, select, sleep, sync.Cond, sync.WaitGroup, IO wait and the like.
	Waiting
)

// Classify returns the Class of a goroutine from its entry of a goroutine dump: header is the text of the first line after the
// "[" that opens the state (the state, then a suffix such as " (scan)", ", 2 minutes" or ", locked to thread", then "]:"), and
// frames are the lines that follow, up to the blank line before the next entry. "semacquire" is the world lock of a collection
// (Busy) and also the wait of a goroutine that needs the lock of a file descriptor another goroutine is using, which only that
// use ends (Lock): the frames of internal/poll's two waits (fdMutex.rwlock, and the semaphore FD.Close waits on), which the dump
// lists where it hides the runtime's own, tell the two apart. Any other frame of internal/poll is code that allocates, and waits
// for the collection.
func Classify(header, frames []byte) Class {
	state := header
	if end := bytes.IndexAny(state, ",]"); end != -1 {
		state = state[:end]
	}
	// Go prints "(scan)" while a collection scans the goroutine's stack and "(leaked)" for a goroutine the runtime found leaked,
	// inside the brackets and after the state.
	if end := bytes.Index(state, []byte(" (")); end != -1 {
		state = state[:end]
	}
	switch string(state) {
	case "running", "runnable", "syscall", "copystack", "preempted",
		"GC assist marking", "GC assist wait", "garbage collection", "garbage collection scan", "GC mark termination",
		"stopping the world", "flushing proc caches", "wait for GC cycle", "GC weak to strong wait",
		"trace goroutine status", "trace proc status", "page trace flush", "dumping heap":
		return Busy
	case "semacquire":
		if bytes.Contains(frames, []byte("internal/poll.(*fdMutex)")) || bytes.Contains(frames, []byte("internal/poll.runtime_Semacquire")) {
			return Lock
		}
		return Busy
	case "sync.Mutex.Lock", "sync.RWMutex.Lock", "sync.RWMutex.RLock":
		return Lock
	}
	return Waiting
}
