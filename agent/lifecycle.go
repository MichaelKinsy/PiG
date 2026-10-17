package agent

import (
	"context"
	"slices"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/agent/src/agent.ts.

// agentListener retains subscription identity independently of callback values.
type agentListener struct {
	callback func(context.Context, AgentEvent) error
	// observesToolUpdates reports whether the listener does anything with a tool_execution_update event; nil means it always may.
	observesToolUpdates func() bool
	// observesMessageUpdates reports whether the listener does anything with a message_update event; nil means it always may.
	observesMessageUpdates func() bool
	removed                atomic.Bool
}

// Subscribe registers a lifecycle listener. Listeners run in subscription order
// with the active run context; the run waits for each callback to return.
// The returned function removes this subscription and can be called repeatedly.
func (a *Agent) Subscribe(listener func(context.Context, AgentEvent) error) func() {
	return a.SubscribeObserving(listener, nil)
}

// SubscribeObserving is Subscribe for a listener that can say whether it currently does anything with a
// tool_execution_update event. observesToolUpdates is asked when a tool execution starts and again while it runs
// (ToolUpdatesObserved); nil means the listener always may. The listener still receives every event.
func (a *Agent) SubscribeObserving(listener func(context.Context, AgentEvent) error, observesToolUpdates func() bool) func() {
	return a.SubscribeObservingUpdates(listener, observesToolUpdates, nil)
}

// SubscribeObservingUpdates is SubscribeObserving for a listener that can also say whether it currently does anything
// with a message_update event. observesMessageUpdates is asked for every message_update; nil means the listener always
// may. While neither it nor anything else the Agent delivers events to may observe one (messageUpdatesObserved), the
// update is not delivered at all: no listener runs for it and the run does not await it. Once one may, every later update
// reaches every listener as before.
func (a *Agent) SubscribeObservingUpdates(listener func(context.Context, AgentEvent) error, observesToolUpdates, observesMessageUpdates func() bool) func() {
	entry := &agentListener{callback: listener, observesToolUpdates: observesToolUpdates, observesMessageUpdates: observesMessageUpdates}
	a.stateMu.Lock()
	a.listeners = append(a.listeners, entry)
	a.stateMu.Unlock()
	return func() {
		entry.removed.Store(true)
		a.stateMu.Lock()
		defer a.stateMu.Unlock()
		if !a.streaming {
			a.listeners = slices.DeleteFunc(a.listeners, func(listener *agentListener) bool { return listener.removed.Load() })
		}
	}
}

// toolUpdatesObserved reports whether anything the Agent delivers events to could see a tool_execution_update: the
// OnEvent, EventCh and AfterEvent options, or a live listener that may observe it.
func (a *Agent) toolUpdatesObserved() bool {
	if a.opts.OnEvent != nil || a.opts.EventCh != nil || a.opts.AfterEvent != nil {
		return true
	}
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	for _, listener := range a.listeners {
		if listener.removed.Load() {
			continue
		}
		if listener.observesToolUpdates == nil || listener.observesToolUpdates() {
			return true
		}
	}
	return false
}

// messageUpdatesObserved reports whether anything the Agent delivers events to could see a message_update now: the
// OnEvent, EventCh and AfterEvent options, or a live listener that may observe it. It is asked for every update, so a
// listener added or starting to observe during a stream sees every update from the next one on.
func (a *Agent) messageUpdatesObserved() bool {
	if a.opts.OnEvent != nil || a.opts.EventCh != nil || a.opts.AfterEvent != nil {
		return true
	}
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	for _, listener := range a.listeners {
		if listener.removed.Load() {
			continue
		}
		if listener.observesMessageUpdates == nil || listener.observesMessageUpdates() {
			return true
		}
	}
	return false
}

// runSignalWatcher retains an observer's identity independently of its callback value.
type runSignalWatcher struct {
	observe func()
	removed atomic.Bool
}

// ObserveRunSignal registers observe to run after a run claims the agent, when Signal already returns the run's context, and after the run releases it, when Signal already returns nil. Pi's `agent.signal` is a getter (agent.ts:336-338), so an owner that replicates the signal elsewhere learns of both instants here.
// observe runs on the goroutine that changes the run, after the agent's lock is released, so it may call Signal; the run waits for observe to return. The returned function removes the observer and can be called repeatedly.
func (a *Agent) ObserveRunSignal(observe func()) func() {
	watcher := &runSignalWatcher{observe: observe}
	a.stateMu.Lock()
	a.runSignalWatchers = append(a.runSignalWatchers, watcher)
	a.stateMu.Unlock()
	return func() {
		watcher.removed.Store(true)
		a.stateMu.Lock()
		defer a.stateMu.Unlock()
		a.runSignalWatchers = slices.DeleteFunc(a.runSignalWatchers, func(w *runSignalWatcher) bool { return w == watcher })
	}
}

// notifyRunSignalObservers runs the observers registered when the run changed, in registration order, skipping one removed meanwhile.
func (a *Agent) notifyRunSignalObservers() {
	a.stateMu.RLock()
	watchers := slices.Clone(a.runSignalWatchers)
	a.stateMu.RUnlock()
	for _, watcher := range watchers {
		if !watcher.removed.Load() {
			watcher.observe()
		}
	}
}

// Signal returns the active run's cancellation context, or nil when idle.
func (a *Agent) Signal() context.Context {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.runContext
}

// Abort cancels the active run. Calling Abort on an idle agent does nothing.
func (a *Agent) Abort() {
	a.stateMu.RLock()
	cancel := a.runCancel
	a.stateMu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

// WaitForIdle waits for the current run and its synchronous listeners to settle.
// It returns immediately when no run is active.
func (a *Agent) WaitForIdle() {
	a.stateMu.RLock()
	done := a.runDone
	a.stateMu.RUnlock()
	if done != nil {
		<-done
	}
}

// StreamingMessage returns a snapshot of the message currently streaming.
func (a *Agent) StreamingMessage() *AgentMessage {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if a.streamingMessage == nil {
		return nil
	}
	if a.streamingDeferred {
		// The last update was not delivered (reduceUnobservedUpdate): build the message it would have carried now, once.
		a.streamingStorage = AgentMessage{Assistant: shallowAssistantMessageFrom(&a.streamingCapture)}
		a.dropStreamingCapture()
	}
	message := startEventMessage(*a.streamingMessage)
	return &message
}

// reduceUnobservedUpdate is reduceEventState for a message_update that nothing observes: the state records the capture
// of the update's partial and StreamingMessage builds the message from it when asked, so the state reads exactly as if
// the update had been delivered.
func (a *Agent) reduceUnobservedUpdate(capture *ai.ShallowCapture) {
	a.stateMu.Lock()
	a.streamingCapture, a.streamingDeferred = *capture, true
	a.streamingMessage = &a.streamingStorage
	a.stateMu.Unlock()
}

// dropStreamingCapture forgets the capture of an undelivered update. The caller holds stateMu.
func (a *Agent) dropStreamingCapture() {
	if a.streamingDeferred {
		a.streamingCapture, a.streamingDeferred = ai.ShallowCapture{}, false
	}
}

// PendingToolCalls returns the active tool call IDs in start order.
func (a *Agent) PendingToolCalls() []string {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return slices.Clone(a.pendingToolCalls)
}

// ErrorMessage returns the last failed turn's error, cleared at the next run or reset.
func (a *Agent) ErrorMessage() string {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.errorMessage
}

// runDeadlineContext preserves the caller's deadline while cancellation forwarding
// is detached when the run settles. A successfully settled Pi signal is not aborted.
type runDeadlineContext struct {
	context.Context
	parent      context.Context
	active      atomic.Bool
	deadline    time.Time
	hasDeadline bool
}

func (ctx *runDeadlineContext) Deadline() (time.Time, bool) { return ctx.deadline, ctx.hasDeadline }

func (ctx *runDeadlineContext) Err() error {
	if ctx.active.Load() {
		if err := ctx.parent.Err(); err != nil {
			return err
		}
	}
	return ctx.Context.Err()
}

func (a *Agent) reduceEventState(ev AgentEvent) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	switch event := ev.(type) {
	case MessageStartEvent:
		a.streamingStorage = event.Message
		a.streamingMessage = &a.streamingStorage
		a.dropStreamingCapture()
	case MessageUpdateEvent:
		a.streamingStorage = event.Message
		a.streamingMessage = &a.streamingStorage
		a.dropStreamingCapture()
	case MessageEndEvent, AgentEndEvent:
		a.streamingMessage = nil
		a.streamingStorage = AgentMessage{}
		a.dropStreamingCapture()
	case ToolExecutionStartEvent:
		if !slices.Contains(a.pendingToolCalls, event.ToolCallID) {
			a.pendingToolCalls = append(a.pendingToolCalls, event.ToolCallID)
		}
	case ToolExecutionEndEvent:
		a.pendingToolCalls = slices.DeleteFunc(a.pendingToolCalls, func(id string) bool { return id == event.ToolCallID })
	case TurnEndEvent:
		if event.Message.Assistant != nil && event.Message.Assistant.ErrorMessage != "" {
			a.errorMessage = event.Message.Assistant.ErrorMessage
		}
	}
}

func (a *Agent) notifyListeners(ev AgentEvent) {
	for index := 0; ; index++ {
		a.stateMu.RLock()
		if index >= len(a.listeners) {
			a.stateMu.RUnlock()
			return
		}
		listener, ctx := a.listeners[index], a.runContext
		a.stateMu.RUnlock()
		if listener.removed.Load() {
			continue
		}
		if err := listener.callback(ctx, ev); err != nil {
			panic(runFailure{err: err})
		}
	}
}
