package codemode

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/plugins/codemode/calls.ts: the codemode tool calls running in this worker, so that removing the plugin can
// end them.

// removedMessage is what a call removed mid-script reports, as its output, before Durable settles it aborted.
const removedMessage = "Code mode was removed while this script ran."

// reportWait is how long to wait for that output to be committed to the call's slot before aborting anyway.
const reportWait = 2 * time.Second

// runningCalls: removing the plugin by a hot reload closes the sandbox while the Harness stays open. Durable stores no
// outcome for a run the sandbox ended on its own (so a restart replays it), and the call then waits for its own
// cancellation: nothing would ever settle it. On removal (not on a worker stop, after which the next worker replays), each
// running call reports why in its output, then is aborted through Durable (`AbortTask`), which ends its script and nested
// calls bottom-up and settles it with an error result. Only then is the sandbox closed.
type runningCalls struct {
	mu    sync.Mutex
	calls map[durable.TaskId]durable.ToolExecutionApi
	idle_ []chan struct{}
}

func newRunningCalls() *runningCalls {
	return &runningCalls{calls: map[durable.TaskId]durable.ToolExecutionApi{}}
}

// track runs one codemode execute(), known as running until it returns.
func (r *runningCalls) track(api durable.ToolExecutionApi, run func() (durable.ToolExecutionResult, error)) (durable.ToolExecutionResult, error) {
	r.mu.Lock()
	r.calls[api.TaskId()] = api
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.calls, api.TaskId())
		if len(r.calls) == 0 {
			for _, waiting := range r.idle_ {
				close(waiting)
			}
			r.idle_ = nil
		}
		r.mu.Unlock()
	}()
	return run()
}

// idle returns once no call runs (at once when none does).
func (r *runningCalls) idle() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	done := make(chan struct{})
	if len(r.calls) == 0 {
		close(done)
		return done
	}
	r.idle_ = append(r.idle_, done)
	return done
}

// size is how many calls run.
func (r *runningCalls) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// cancel ends every running call with an error result; it returns once each is terminal.
func (r *runningCalls) cancel(ctx context.Context, session harness.Harness) {
	r.mu.Lock()
	running := make([]durable.ToolExecutionApi, 0, len(r.calls))
	for _, api := range r.calls {
		running = append(running, api)
	}
	r.mu.Unlock()
	var group sync.WaitGroup
	for _, api := range running {
		group.Add(1)
		go func() {
			defer group.Done()
			// Already settling: the abort below decides.
			func() {
				defer func() { _ = recover() }()
				api.Output(removedMessage + "\n")
				reported(ctx, session, api)
			}()
			_, _ = session.AbortTask(ctx, api.TaskId())
			_, _ = session.WaitForTask(ctx, api.TaskId())
		}()
	}
	group.Wait()
}

type liveObserver struct {
	done  func(*harness.LiveState) bool
	shown chan struct{}
	once  sync.Once
}

func (o *liveObserver) show() { o.once.Do(func() { close(o.shown) }) }

func (o *liveObserver) Advance(_ context.Context, call chord.WatchCall[harness.LiveState]) {
	if call.Kind != chord.WatchCallValue {
		return
	}
	if state := call.Value; o.done(&state) {
		o.show()
	}
}

func (o *liveObserver) End(chord.WatchEnd) { o.show() }

// reported returns once the call's slot holds the removal message (the output is committed with a throttle), or after a
// bound.
func reported(ctx context.Context, session harness.Harness, api durable.ToolExecutionApi) {
	done := func(live *harness.LiveState) bool {
		if live == nil {
			return true
		}
		for _, tool := range live.Tools {
			if tool.TaskId != nil && *tool.TaskId == api.TaskId() {
				return tool.Status == harness.ToolSlotDone || contains(tool.Output, removedMessage)
			}
		}
		return true
	}
	observer := &liveObserver{done: done, shown: make(chan struct{})}
	observation, err := durable.ObserveDoc[harness.LiveState](ctx, session, harness.LiveDoc, observer, api.ConversationId())
	if err != nil || observation == nil {
		return
	}
	defer observation.Detach()
	current := observation.Value()
	if done(&current) {
		return
	}
	select {
	case <-observer.shown:
	case <-time.After(reportWait):
	case <-ctx.Done():
	}
}

func contains(output *string, text string) bool {
	return output != nil && strings.Contains(*output, text)
}
