package tui

// pi: packages/coding-agent/src/modes/interactive/components/model-selector.ts (`void this.refreshModels()` and dispose)

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// ownedModelRuntime is a ModelSelectorRuntime that owns the selector's refresh task, as the interactive mode does.
type ownedModelRuntime struct {
	fakeModelRuntime
	start func(refresh func(context.Context)) bool
}

func (r *ownedModelRuntime) StartModelSelectorTask(refresh func(context.Context)) bool {
	return r.start(refresh)
}

// taskOwner admits tasks until its context ends, and joins them.
type taskOwner struct {
	ctx    context.Context
	cancel context.CancelFunc
	tasks  sync.WaitGroup
}

func newTaskOwner(t *testing.T) *taskOwner {
	ctx, cancel := context.WithCancel(t.Context())
	return &taskOwner{ctx: ctx, cancel: cancel}
}

func (o *taskOwner) start(refresh func(context.Context)) bool {
	if o.ctx.Err() != nil {
		return false
	}
	o.tasks.Go(func() { refresh(o.ctx) })
	return true
}

// lingeringRefresh is a refresh that notices its context ending and then finishes what it was doing, as a catalog store flushing a write does.
func lingeringRefresh(started chan<- struct{}, returned *atomic.Bool) func(context.Context) ai.ModelsRefreshResult {
	return func(ctx context.Context) ai.ModelsRefreshResult {
		started <- struct{}{}
		<-ctx.Done()
		time.Sleep(time.Second)
		returned.Store(true)
		return ai.ModelsRefreshResult{Aborted: true}
	}
}

// Pi starts the refresh unawaited, so nothing joins it. In Go that work is an owned task: the owner that admitted it ends its context on shutdown
// and waits for it, so the refresh call has returned, with nothing posted to the loop, when the owner's join does.
func TestModelSelectorRefreshIsJoinedByItsTaskOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ui, queue := newModelSelectorTUI(t)
		owner := newTaskOwner(t)
		started := make(chan struct{}, 1)
		var returned atomic.Bool
		runtime := &ownedModelRuntime{refresh: lingeringRefresh(started, &returned), start: owner.start}
		selector := NewModelSelectorComponent(ui, nil, runtime, nil, nil, nil, "", nil, nil)
		defer selector.Dispose()
		<-started

		owner.cancel()
		owner.tasks.Wait()

		if !returned.Load() {
			t.Error("the owner's join returned while the selector's catalog refresh was still running")
		}
		for len(queue.tasks) > 0 {
			(<-queue.tasks)()
		}
		if rendered := strings.Join(selectorRows(selector), "\n"); strings.Contains(rendered, "Could not refresh") || strings.Contains(rendered, "timed out") {
			t.Errorf("a refresh the owner ended changed the picker: %q", rendered)
		}
	})
}

// Dispose aborts the refresh's signal whether or not the owner has started the task yet (model-selector.ts dispose aborts refreshAbortController).
func TestModelSelectorDisposeBeforeTheOwnerStartsTheTask(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ui, queue := newModelSelectorTUI(t)
		owner := newTaskOwner(t)
		var pending func(context.Context)
		var sawCancelled atomic.Bool
		runtime := &ownedModelRuntime{
			refresh: func(ctx context.Context) ai.ModelsRefreshResult {
				sawCancelled.Store(ctx.Err() != nil)
				return ai.ModelsRefreshResult{Aborted: true}
			},
			start: func(refresh func(context.Context)) bool { pending = refresh; return true },
		}
		selector := NewModelSelectorComponent(ui, nil, runtime, nil, nil, nil, "", nil, nil)
		selector.Dispose()
		owner.tasks.Go(func() { pending(owner.ctx) })
		owner.tasks.Wait()

		if !sawCancelled.Load() {
			t.Error("a refresh started after Dispose ran with a live signal")
		}
		for len(queue.tasks) > 0 {
			(<-queue.tasks)()
		}
		if rendered := strings.Join(selectorRows(selector), "\n"); strings.Contains(rendered, "Could not refresh") || strings.Contains(rendered, "timed out") {
			t.Errorf("a disposed selector received its refresh: %q", rendered)
		}
	})
}

// An owner that no longer admits work runs no refresh, and the picker does not keep promising one.
func TestModelSelectorWithAShutDownOwnerShowsNoRefreshStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ui, _ := newModelSelectorTUI(t)
		var refreshed atomic.Bool
		runtime := &ownedModelRuntime{
			refresh: func(context.Context) ai.ModelsRefreshResult { refreshed.Store(true); return ai.ModelsRefreshResult{} },
			start:   func(func(context.Context)) bool { return false },
		}
		selector := NewModelSelectorComponent(ui, nil, runtime, nil, nil, nil, "", nil, nil)
		defer selector.Dispose()
		synctest.Wait()

		if refreshed.Load() {
			t.Error("a shut-down owner ran the refresh")
		}
		if rendered := strings.Join(selectorRows(selector), "\n"); strings.Contains(rendered, "Refreshing model catalogs") {
			t.Errorf("the picker promises a refresh that never runs: %q", rendered)
		}
	})
}

// A runtime with no task owner leaves the picker to own its refresh: Wait returns once Dispose has ended it and the refresh call has returned.
func TestModelSelectorWaitJoinsItsOwnRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ui, _ := newModelSelectorTUI(t)
		started := make(chan struct{}, 1)
		var returned atomic.Bool
		selector := NewModelSelectorComponent(ui, nil, &fakeModelRuntime{refresh: lingeringRefresh(started, &returned)}, nil, nil, nil, "", nil, nil)
		<-started
		selector.Dispose()
		selector.Wait()
		if !returned.Load() {
			t.Error("Wait returned while the refresh was still running")
		}
	})
}
