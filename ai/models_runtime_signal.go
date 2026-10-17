package ai

import (
	"context"
	"runtime"
	"sync"
	"time"
	"weak"
)

// A completed refresh signal remains live, like AbortSignal.any: completion does not abort it. Weak parent forwarding releases unused signals without changing retained signals' cancellation behavior.
type modelsRefreshSignal struct {
	context.Context
	cancel context.CancelCauseFunc
	parent context.Context
}

func (signal *modelsRefreshSignal) syncParent() {
	if signal.parent.Err() != nil {
		signal.cancel(context.Cause(signal.parent))
	}
}
func (signal *modelsRefreshSignal) Err() error { signal.syncParent(); return signal.Context.Err() }
func (signal *modelsRefreshSignal) Done() <-chan struct{} {
	signal.syncParent()
	return signal.Context.Done()
}
func (signal *modelsRefreshSignal) Value(key any) any {
	signal.syncParent()
	return signal.Context.Value(key)
}

func (signal *modelsRefreshSignal) Deadline() (time.Time, bool) { return signal.parent.Deadline() }

func newModelsRefreshSignal(parent context.Context) *modelsRefreshSignal {
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	signal := &modelsRefreshSignal{Context: ctx, cancel: cancel, parent: parent}
	if parent.Err() != nil {
		cancel(context.Cause(parent))
		return signal
	}
	done := parent.Done()
	if done == nil {
		return signal
	}
	refreshForwarders.Lock()
	forwarder := refreshForwarders.byDone[done]
	if forwarder == nil {
		forwarder = &refreshForwarder{done: done, signals: map[uint64]weak.Pointer[modelsRefreshSignal]{}}
		refreshForwarders.byDone[done] = forwarder
		forwarder.stop = context.AfterFunc(parent, forwarder.fire)
	}
	forwarder.next++
	id := forwarder.next
	forwarder.signals[id] = weak.Make(signal)
	refreshForwarders.Unlock()
	runtime.AddCleanup(signal, forwarder.release, id)
	return signal
}

// refreshForwarders forwards a caller's cancellation to the refresh signals derived from it that are still reachable, through one context.AfterFunc per caller Done channel (as modelTaskOwner binds one context per caller). An AfterFunc per signal started one goroutine per signal the collector had not freed yet when the caller was cancelled: 82 at the exit of a print run with no GC cycle.
var refreshForwarders = struct {
	sync.Mutex
	byDone map[<-chan struct{}]*refreshForwarder
}{byDone: map[<-chan struct{}]*refreshForwarder{}}

// refreshForwarder lives while its caller is not cancelled and a signal it forwards to is reachable. Its fields are guarded by refreshForwarders.
type refreshForwarder struct {
	done    <-chan struct{}
	stop    func() bool
	next    uint64
	signals map[uint64]weak.Pointer[modelsRefreshSignal] // nil once fired
}

func (forwarder *refreshForwarder) fire() {
	refreshForwarders.Lock()
	if refreshForwarders.byDone[forwarder.done] == forwarder {
		delete(refreshForwarders.byDone, forwarder.done)
	}
	signals := forwarder.signals
	forwarder.signals = nil
	refreshForwarders.Unlock()
	for _, reference := range signals {
		if signal := reference.Value(); signal != nil {
			signal.cancel(context.Cause(signal.parent))
		}
	}
}

// release drops a signal the collector freed, and the forwarder with its registration on the caller after the last one.
func (forwarder *refreshForwarder) release(id uint64) {
	refreshForwarders.Lock()
	if forwarder.signals == nil {
		refreshForwarders.Unlock()
		return
	}
	delete(forwarder.signals, id)
	last := len(forwarder.signals) == 0
	if last {
		delete(refreshForwarders.byDone, forwarder.done)
		forwarder.signals = nil
	}
	refreshForwarders.Unlock()
	if last {
		forwarder.stop()
	}
}
