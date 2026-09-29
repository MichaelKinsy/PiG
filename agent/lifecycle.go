package agent

import (
	"context"
	"slices"
	"sync/atomic"
	"time"
)

// Ports packages/agent/src/agent.ts.

// agentListener retains subscription identity independently of callback values.
type agentListener struct {
	callback func(context.Context, AgentEvent) error
	removed  atomic.Bool
}

// Subscribe registers a lifecycle listener. Listeners run in subscription order
// with the active run context; the run waits for each callback to return.
// The returned function removes this subscription and can be called repeatedly.
func (a *Agent) Subscribe(listener func(context.Context, AgentEvent) error) func() {
	entry := &agentListener{callback: listener}
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
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	if a.streamingMessage == nil {
		return nil
	}
	message := startEventMessage(*a.streamingMessage)
	return &message
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
		message := event.Message
		a.streamingMessage = &message
	case MessageUpdateEvent:
		message := event.Message
		a.streamingMessage = &message
	case MessageEndEvent, AgentEndEvent:
		a.streamingMessage = nil
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
