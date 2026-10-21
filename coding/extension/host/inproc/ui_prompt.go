package inproc

import (
	"context"
	"encoding/json"
	"strconv"
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
	// enqueued counts the events queued and admitted the events whose handlers were invoked; waiters wait for admitted to reach a count.
	enqueued int
	admitted int
	waiters  []uiPromptWaiter
}

type uiPromptWaiter struct {
	target int
	done   chan struct{}
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
	t.enqueued++
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
	for _, snapshot := range snapshotEventHandlers(r.extensions, eventType) {
		ext := snapshot.ext
		for _, handler := range snapshot.handlers {
			if _, err := callHandler(handler, event, dispatchCtx); err != nil {
				r.recordHandlerError(ctx, ext.Path, eventType, err)
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
			t.admitLocked(t.enqueued)
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
		t.mu.Lock()
		t.admitLocked(t.admitted + 1)
		t.mu.Unlock()
	}
}

// admitLocked records admitted events up to count and releases the waiters it satisfies. Caller holds t.mu.
func (t *uiPromptTracker) admitLocked(count int) {
	t.admitted = count
	waiting := t.waiters[:0]
	for _, waiter := range t.waiters {
		if waiter.target <= t.admitted {
			//portlint:allow doubleclose the caller holds t.mu, and a released waiter leaves t.waiters in this pass, so its done channel is closed once
			close(waiter.done)
			continue
		}
		waiting = append(waiting, waiter)
	}
	t.waiters = waiting
}

// AwaitUIPromptEvents returns once the handlers of every ui_prompt event queued before the call have been invoked, or when ctx ends.
//
// upstream: runner.ts emitUIPromptEvent queues the emission in a microtask before withUIPrompt's promise settles, so each handler's synchronous part runs before the prompting extension continues. The subprocess bridge waits here before it answers a dialog call.
func (r *Runner) AwaitUIPromptEvents(ctx context.Context) {
	t := &r.uiPrompts
	t.mu.Lock()
	if t.admitted >= t.enqueued {
		t.mu.Unlock()
		return
	}
	waiter := uiPromptWaiter{target: t.enqueued, done: make(chan struct{})}
	t.waiters = append(t.waiters, waiter)
	t.mu.Unlock()
	select {
	case <-waiter.done:
	case <-ctx.Done():
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

// SelectValue, ConfirmValue, InputValue and EditorValue are [extension.DialogValueUIContext]: they report the prompt as the string-typed dialogs do and
// pass the bound UI's JSON result through. A bound UI without them resolves through its string-typed method, and the result is that string as JSON (confirm: the bool).
func (c *uiPromptContext) SelectValue(ctx context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	defer c.runner.BeginUIPrompt(extension.UIPromptKindSelect, title)()
	if ui, ok := c.UIContext.(extension.DialogValueUIContext); ok {
		return ui.SelectValue(ctx, title, options, opts)
	}
	return stringResult(c.UIContext.Select(ctx, title, options, opts))
}

func (c *uiPromptContext) ConfirmValue(ctx context.Context, title, message string, opts extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	defer c.runner.BeginUIPrompt(extension.UIPromptKindConfirm, title)()
	if ui, ok := c.UIContext.(extension.DialogValueUIContext); ok {
		return ui.ConfirmValue(ctx, title, message, opts)
	}
	confirmed, err := c.UIContext.Confirm(ctx, title, message, opts)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(strconv.FormatBool(confirmed)), nil
}

// A nil placeholder or prefill is Pi's undefined: a bound UI without a value-typed dialog opens it through its omitter, or with an empty member.
func (c *uiPromptContext) InputValue(ctx context.Context, title string, placeholder *string, opts extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	defer c.runner.BeginUIPrompt(extension.UIPromptKindInput, title)()
	if ui, ok := c.UIContext.(extension.DialogValueUIContext); ok {
		return ui.InputValue(ctx, title, placeholder, opts)
	}
	if omitter, ok := c.UIContext.(extension.PlaceholderOmitter); ok && placeholder == nil {
		return stringResult(omitter.InputWithoutPlaceholder(ctx, title, opts))
	}
	text := ""
	if placeholder != nil {
		text = *placeholder
	}
	return stringResult(c.UIContext.Input(ctx, title, text, opts))
}

func (c *uiPromptContext) EditorValue(ctx context.Context, title string, prefill *string) (json.RawMessage, error) {
	defer c.runner.BeginUIPrompt(extension.UIPromptKindEditor, title)()
	if ui, ok := c.UIContext.(extension.DialogValueUIContext); ok {
		return ui.EditorValue(ctx, title, prefill)
	}
	if omitter, ok := c.UIContext.(extension.PrefillOmitter); ok && prefill == nil {
		return stringResult(omitter.EditorWithoutPrefill(ctx, title))
	}
	text := ""
	if prefill != nil {
		text = *prefill
	}
	return stringResult(c.UIContext.Editor(ctx, title, text))
}

func stringResult(text string, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(text)
	return encoded, nil
}

func (c *uiPromptContext) Custom(ctx context.Context, factory extension.CustomFactory, opts *extension.CustomOptions) (any, error) {
	defer c.runner.BeginUIPrompt(extension.UIPromptKindCustom, "")()
	return c.UIContext.Custom(ctx, factory, opts)
}

// ClearWorkingMessage restores the default working label (Pi's setWorkingMessage() with no argument) through the bound UI's own
// [extension.WorkingMessageClearer]; for a bound UI without one it sends the empty message, as the subprocess bridge does. Embedding the bound UI
// would otherwise hide its clearer from in-process extensions.
func (c *uiPromptContext) ClearWorkingMessage() {
	if clearer, ok := c.UIContext.(extension.WorkingMessageClearer); ok {
		clearer.ClearWorkingMessage()
		return
	}
	c.SetWorkingMessage("")
}

// ClearHiddenThinkingLabel restores the default hidden-thinking label (Pi's setHiddenThinkingLabel() with no argument) through the bound UI's own
// [extension.HiddenThinkingLabelClearer], or sends the empty label for a bound UI without one.
func (c *uiPromptContext) ClearHiddenThinkingLabel() {
	if clearer, ok := c.UIContext.(extension.HiddenThinkingLabelClearer); ok {
		clearer.ClearHiddenThinkingLabel()
		return
	}
	c.SetHiddenThinkingLabel("")
}

// ClearStatus is Pi's setStatus(key, undefined) through the bound UI's own [extension.StatusClearer], or the empty text for a bound UI without one.
func (c *uiPromptContext) ClearStatus(key string) {
	if clearer, ok := c.UIContext.(extension.StatusClearer); ok {
		clearer.ClearStatus(key)
		return
	}
	c.SetStatus(key, "")
}

// InputWithoutPlaceholder opens the input dialog without a placeholder through the bound UI's own [extension.PlaceholderOmitter], or with an empty
// placeholder for a bound UI without one. Like Input it is a ui_prompt scope.
func (c *uiPromptContext) InputWithoutPlaceholder(ctx context.Context, title string, opts extension.ExtensionUIDialogOptions) (string, error) {
	omitter, ok := c.UIContext.(extension.PlaceholderOmitter)
	if !ok {
		return c.Input(ctx, title, "", opts)
	}
	defer c.runner.BeginUIPrompt(extension.UIPromptKindInput, title)()
	return omitter.InputWithoutPlaceholder(ctx, title, opts)
}

// EditorWithoutPrefill opens the editor dialog without a prefill through the bound UI's own [extension.PrefillOmitter], or with an empty prefill for a
// bound UI without one. Like Editor it is a ui_prompt scope.
func (c *uiPromptContext) EditorWithoutPrefill(ctx context.Context, title string) (string, error) {
	omitter, ok := c.UIContext.(extension.PrefillOmitter)
	if !ok {
		return c.Editor(ctx, title, "")
	}
	defer c.runner.BeginUIPrompt(extension.UIPromptKindEditor, title)()
	return omitter.EditorWithoutPrefill(ctx, title)
}
