package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// errSessionReplacementCancelled reports that a session_before_switch or
// session_before_fork handler cancelled the replacement. The command then ends
// without output, as upstream's { cancelled: true } results do.
var errSessionReplacementCancelled = errors.New("session replacement cancelled by an extension")

// emitSessionStartWithUI awaits handler completion without blocking its UI callbacks.
func (m *InteractiveMode) emitSessionStartWithUI(ctx context.Context, reason string) error {
	if m.newRunner == nil || !m.newRunner.HasHandlers(EventSessionStart) {
		return nil
	}
	return m.awaitExtensionUI(ctx, func(ctx context.Context) error {
		_, err := m.newRunner.Emit(ctx, extension.SessionStartEvent{Type: EventSessionStart, Reason: reason})
		return err
	})
}

// beforeSessionSwitch awaits session_before_switch without blocking its UI
// callbacks and reports whether a handler cancelled the switch.
// Mirrors upstream agent-session-runtime.ts emitBeforeSwitch.
func (m *InteractiveMode) beforeSessionSwitch(ctx context.Context, reason, targetSessionFile string) (bool, error) {
	return m.awaitSessionBefore(ctx, EventSessionBeforeSwitch, extension.SessionBeforeSwitchEvent{
		Type:              EventSessionBeforeSwitch,
		Reason:            reason,
		TargetSessionFile: targetSessionFile,
	})
}

// beforeSessionFork awaits session_before_fork without blocking its UI
// callbacks and reports whether a handler cancelled the fork.
// Mirrors upstream agent-session-runtime.ts emitBeforeFork.
func (m *InteractiveMode) beforeSessionFork(ctx context.Context, entryID, position string) (bool, error) {
	return m.awaitSessionBefore(ctx, EventSessionBeforeFork, extension.SessionBeforeForkEvent{
		Type:     EventSessionBeforeFork,
		EntryID:  entryID,
		Position: position,
	})
}

func (m *InteractiveMode) awaitSessionBefore(ctx context.Context, eventType string, event extension.ExtensionEvent) (bool, error) {
	runner := m.newRunner
	if runner == nil || !runner.HasHandlers(eventType) {
		return false, nil
	}
	var result any
	err := m.awaitExtensionUI(ctx, func(ctx context.Context) error {
		// The runner reports handler failures and continues the chain.
		result, _ = runner.Emit(ctx, event)
		return nil
	})
	if err != nil {
		return false, err
	}
	// An in-process handler returns a typed result and a subprocess one a
	// JSON object; both carry upstream's cancel field.
	data, err := json.Marshal(result)
	if err != nil {
		return false, nil
	}
	var value struct {
		Cancel bool `json:"cancel"`
	}
	return json.Unmarshal(data, &value) == nil && value.Cancel, nil
}

// awaitExtensionUI preserves an awaited extension phase while the owner loop continues servicing its UI calls. Extension code never runs on that loop.
func (m *InteractiveMode) awaitExtensionUI(ctx context.Context, work func(context.Context) error) error {
	if m.runCtx == nil {
		return work(ctx)
	}
	m.currentInputTicket.resume()
	m.currentInputTicket.settle()
	done := make(chan struct{})
	var result error
	m.backgroundTasks.Go(func() { defer close(done); result = work(ctx) })
	if err := m.inputLoopUntil(ctx, os.Stdin, done); err != nil {
		return err
	}
	select {
	case <-done:
		return result
	case <-ctx.Done():
		return ctx.Err()
	}
}
