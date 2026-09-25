package inproc

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// uiPromptTracker is the runner-owned state behind ui_prompt_start and
// ui_prompt_end.
//
// upstream: runner.ts uiPromptDepth, activeUIPrompt, withUIPrompt, and
// emitUIPromptEvent. Only the outermost prompt reports: nested prompts
// raise the depth without an event, and the end event repeats the kind and
// title the start event carried.
//
// Async contract: upstream queues `void emit(event)` in a microtask. The runner
// launches notifications in FIFO order but never waits for a prior emission's
// handlers. Each emission awaits its own handlers in registration order. The
// runner cancels all emission contexts when invalidated; a handler may finish
// after another event's handler, just as upstream async callbacks can.
type uiPromptTracker struct {
	mu       sync.Mutex
	depth    int
	active   uiPrompt
	pending  []any
	draining bool
	ctx      context.Context
	cancel   context.CancelFunc
}

type uiPrompt struct {
	kind  extension.UIPromptKind
	title string
}

// BeginUIPrompt records that a blocking extension UI prompt started and
// returns the function that records its end. Call end exactly once when the
// prompt settles, whether it resolved, was cancelled, or failed; extra calls
// are ignored.
//
// The runner wraps the UI surface bound by [Runner.SetUIContext] with this
// scope for in-process extensions. Subprocess prompts reach the terminal
// through the subprocess UI bridge, which calls BeginUIPrompt when the host
// call arrives, before it waits for terminal focus.
//
// upstream: runner.ts withUIPrompt (private; pig exposes it because the
// subprocess bridge sits outside the runner).
func (r *Runner) BeginUIPrompt(kind extension.UIPromptKind, title string) (end func()) {
	t := &r.uiPrompts
	t.mu.Lock()
	t.depth++
	if t.depth == 1 {
		t.active = uiPrompt{kind: kind, title: title}
		r.enqueueUIPromptLocked(extension.UIPromptStartEvent{
			Type: "ui_prompt_start", Reason: "ui_prompt", Kind: kind, Title: title,
		})
	}
	t.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.depth--
			if t.depth > 0 {
				return
			}
			t.depth = 0
			prompt := t.active
			t.active = uiPrompt{}
			r.enqueueUIPromptLocked(extension.UIPromptEndEvent{
				Type: "ui_prompt_end", Reason: "ui_prompt", Kind: prompt.kind, Title: prompt.title,
			})
		})
	}
}

// enqueueUIPromptLocked appends one notification and starts the drain
// goroutine when none is running. Caller holds r.uiPrompts.mu.
func (r *Runner) enqueueUIPromptLocked(event any) {
	t := &r.uiPrompts
	if r.IsStale() {
		return
	}
	if t.ctx == nil {
		t.ctx, t.cancel = context.WithCancel(context.Background())
	}
	t.pending = append(t.pending, event)
	if t.draining {
		return
	}
	t.draining = true
	go r.drainUIPromptEvents()
}

// emitUIPromptEvent invokes handlers from one event snapshot. An in-process
// handler acknowledges after it returns. A subprocess transport acknowledges
// when the SDK reports an awaited operation or handler completion.
func (r *Runner) emitUIPromptEvent(ctx context.Context, event any) {
	if r.assertActive() != nil {
		invocation.Acknowledge(ctx)
		return
	}
	eventType, err := readEventType(event)
	if err != nil {
		invocation.Acknowledge(ctx)
		return
	}
	dispatchCtx := r.dispatchContext(ctx)
	for _, ext := range r.extensions {
		for _, handler := range ext.EventHandlers(eventType) {
			if _, err := callHandler(handler, event, dispatchCtx); err != nil {
				r.recordHandlerError(ext.Path, eventType, err)
			}
		}
	}
	invocation.Acknowledge(dispatchCtx)
}

// drainUIPromptEvents dispatches queued notifications in order until the
// queue is empty or the runner is invalidated.
func (r *Runner) drainUIPromptEvents() {
	t := &r.uiPrompts
	for {
		t.mu.Lock()
		if len(t.pending) == 0 || r.IsStale() {
			t.pending = nil
			t.draining = false
			t.mu.Unlock()
			return
		}
		event := t.pending[0]
		t.pending = t.pending[1:]
		t.mu.Unlock()
		// Launch emissions in queue order, without awaiting a prior event's
		// handlers. Upstream queues `void emit(event)`, so a suspended start
		// handler must not prevent the matching end event from running.
		invoked := make(chan struct{})
		dispatchCtx := invocation.WithAcknowledgment(t.ctx, func() { close(invoked) })
		go r.emitUIPromptEvent(dispatchCtx, event)
		<-invoked
	}
}

// uiPromptContext is the UI surface a runner hands to in-process
// extensions. Blocking prompts report ui_prompt_start/ui_prompt_end through
// the owning runner; every other method is the bound surface's own.
//
// upstream: runner.ts wrapUIPromptContext. custom carries no title there.
type uiPromptContext struct {
	extension.UIContext
	runner *Runner
}

func (c *uiPromptContext) Select(ctx context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions) (string, error) {
	defer c.runner.BeginUIPrompt(extension.UIPromptKindSelect, title)()
	return c.UIContext.Select(ctx, title, options, opts)
}

func (c *uiPromptContext) Confirm(ctx context.Context, title, message string, opts extension.ExtensionUIDialogOptions) (bool, error) {
	defer c.runner.BeginUIPrompt(extension.UIPromptKindConfirm, title)()
	return c.UIContext.Confirm(ctx, title, message, opts)
}

func (c *uiPromptContext) Input(ctx context.Context, title, placeholder string, opts extension.ExtensionUIDialogOptions) (string, error) {
	defer c.runner.BeginUIPrompt(extension.UIPromptKindInput, title)()
	return c.UIContext.Input(ctx, title, placeholder, opts)
}

func (c *uiPromptContext) Editor(ctx context.Context, title, prefill string) (string, error) {
	defer c.runner.BeginUIPrompt(extension.UIPromptKindEditor, title)()
	return c.UIContext.Editor(ctx, title, prefill)
}

func (c *uiPromptContext) Custom(ctx context.Context, factory any, opts any) (any, error) {
	defer c.runner.BeginUIPrompt(extension.UIPromptKindCustom, "")()
	return c.UIContext.Custom(ctx, factory, opts)
}
