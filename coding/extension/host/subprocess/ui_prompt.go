package subprocess

import (
	"context"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// UIPromptScope records blocking extension UI prompts for the runner that
// emits ui_prompt_start and ui_prompt_end. [inproc.Runner] implements it.
//
// upstream: runner.ts withUIPrompt wraps every ctx.ui select/confirm/input/
// editor/custom call. Subprocess calls reach the terminal through this
// bridge rather than the runner's UI surface, so the bridge opens the scope.
type UIPromptScope interface {
	BeginUIPrompt(kind extension.UIPromptKind, title string) (end func())
	// AwaitUIPromptEvents returns once every prompt event queued so far has reached its handlers, or when ctx ends.
	AwaitUIPromptEvents(ctx context.Context)
}

// SetUIPromptScope binds the runner that reports subprocess prompts. The
// host rebinds it when a reload replaces the runner. nil stops reporting.
func (b *UIBridge) SetUIPromptScope(scope UIPromptScope) {
	b.mu.Lock()
	b.promptScope = scope
	b.mu.Unlock()
}

// beginUIPrompt opens the prompt scope for a blocking dialog call. It runs
// when the call arrives, before the call waits for terminal focus, because
// the extension is already waiting on the user from that point. Without a
// real UI surface the dialog answers immediately and, as upstream's no-op
// surface does, reports nothing.
func (b *UIBridge) beginUIPrompt(call *CallPayload) (end func()) {
	kind, ok := uiPromptKinds[call.Method]
	if !ok {
		return func() {}
	}
	b.mu.RLock()
	scope, ready := b.promptScope, b.uiReady
	b.mu.RUnlock()
	if scope == nil || !ready {
		return func() {}
	}
	title := ""
	if kind != extension.UIPromptKindCustom {
		var args struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal(call.Args, &args) // the dialog handler reports malformed args
		title = args.Title
	}
	return scope.BeginUIPrompt(kind, title)
}

// uiPromptKinds maps each blocking dialog call to its prompt kind. upstream
// wrapUIPromptContext passes no title for custom.
var uiPromptKinds = map[string]extension.UIPromptKind{
	"ui.select":  extension.UIPromptKindSelect,
	"ui.confirm": extension.UIPromptKindConfirm,
	"ui.input":   extension.UIPromptKindInput,
	"ui.editor":  extension.UIPromptKindEditor,
	CallUICustom: extension.UIPromptKindCustom,
}

// awaitUIPromptEvents holds a dialog call's answer until the prompt events it queued have reached their handlers, as Pi runs them in microtasks before the dialog's promise settles.
func (b *UIBridge) awaitUIPromptEvents(ctx context.Context, call *CallPayload) {
	if _, ok := uiPromptKinds[call.Method]; !ok {
		return
	}
	b.mu.RLock()
	scope := b.promptScope
	b.mu.RUnlock()
	if scope != nil {
		scope.AwaitUIPromptEvents(ctx)
	}
}
