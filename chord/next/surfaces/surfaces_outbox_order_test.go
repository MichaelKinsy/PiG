package surfaces

import (
	"runtime"
	"strings"
	"sync"
	"testing"
)

// pi: packages/chord/src/surfaces/active-theme.ts and active-language.ts: `update` is `changed(activeTheme(themes, pick, current))`
// on Pi's one thread, so the three updates (the registry, the pick, the mode) call `changed` in the order they read the state, and
// the last `changed` has the newest state. Here the updates come from several goroutines (the watches' and the app's): the outbox
// keeps that order.

// waitsOnOutboxLock says a goroutine is blocked on the lock of the outbox's queue, in the step that reads a follower's state.
func waitsOnOutboxLock() bool {
	for size := 1 << 20; ; size *= 2 {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n == size {
			continue
		}
		for stack := range strings.SplitSeq(string(buf[:n]), "\n\n") {
			header, _, _ := strings.Cut(stack, "\n")
			if strings.Contains(stack, "surfaces.(*outbox).enqueue(") && (strings.Contains(header, "sync.Mutex.Lock") || strings.Contains(header, "semacquire")) {
				return true
			}
		}
		return false
	}
}

// An update that read the state first is delivered first, though another update read newer state while the first one had not
// queued yet: the last delivery is the newest state, never an older one. The first update is held between its read and its
// queueing (its read function blocks on a channel, no clock); the second runs on another goroutine and is let finish or wait for the
// lock, whichever the outbox does, before the first is let go.
func TestAnOutboxDeliversChangesInTheOrderTheirStateWasRead(t *testing.T) {
	var o outbox
	var stateMu sync.Mutex
	state := "old"
	var deliveredMu sync.Mutex
	var delivered []string
	deliver := func(value string) func() {
		return func() { deliveredMu.Lock(); delivered = append(delivered, value); deliveredMu.Unlock() }
	}
	firstRead, resume, firstDone, secondDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(firstDone)
		o.update(func() func() {
			stateMu.Lock()
			value := state
			stateMu.Unlock()
			close(firstRead)
			<-resume
			return deliver(value)
		})
	}()
	<-firstRead
	go func() {
		defer close(secondDone)
		o.update(func() func() {
			stateMu.Lock()
			state = "new"
			stateMu.Unlock()
			return deliver("new")
		})
	}()
	for waited := false; !waited; {
		select {
		case <-secondDone:
			waited = true
		default:
			waited = waitsOnOutboxLock()
			runtime.Gosched()
		}
	}
	close(resume)
	<-firstDone
	<-secondDone
	deliveredMu.Lock()
	defer deliveredMu.Unlock()
	if got := strings.Join(delivered, ","); got != "old,new" {
		t.Errorf("delivered %s, want old,new: the state read last must be delivered last", got)
	}
}
