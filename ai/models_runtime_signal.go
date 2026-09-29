package ai

import (
	"context"
	"runtime"
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
	reference := weak.Make(signal)
	stop := context.AfterFunc(parent, func() {
		if signal := reference.Value(); signal != nil {
			signal.cancel(context.Cause(parent))
		}
	})
	runtime.AddCleanup(signal, func(stop func() bool) { stop() }, stop)
	if parent.Err() != nil {
		cancel(context.Cause(parent))
	}
	return signal
}
