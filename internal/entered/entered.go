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
// on a lock, so a lock in Go code is part of its synchronous work, unless the caller may hold the lock.
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
// its buffer. It is not parked while running, runnable, in a system call (a synchronous system call is work, as
// upstream's synchronous I/O) or waiting for a sync.Mutex, sync.RWMutex or semaphore.
func Parked(id uint64, stacks []byte) (parked, found bool, buffer []byte) {
	return parkedOrBlocked(id, stacks, false)
}

// parkedOrBlocked is Parked; with locks, a goroutine waiting for a sync.Mutex, sync.RWMutex or semaphore is parked too.
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
	state := stacks[start+len(header):]
	if end := bytes.IndexAny(state, ",]"); end != -1 {
		state = state[:end]
	}
	switch string(state) {
	case "running", "runnable", "syscall", "copystack", "preempted":
		return false, true, stacks
	case "sync.Mutex.Lock", "sync.RWMutex.Lock", "sync.RWMutex.RLock", "semacquire":
		return locks, true, stacks
	}
	return true, true, stacks
}
