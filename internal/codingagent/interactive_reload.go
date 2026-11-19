package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts.

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/ctxowner"
	"github.com/MichaelKinsy/PiG/tui"
)

// errReloadBlocked reports a reload refused while a response streams or a compaction runs. The refusal is already shown as a warning, and the caller's reload completes without error.
var errReloadBlocked = errors.New("reload blocked")

// reloadFromExtension runs /reload for an extension's ctx.reload(). The reloaded extension processes live under the context the reload runs with, and a call's context ends with its command, so the reload runs under the run's context: the command awaits it, and its return must not stop the extensions it started.
// As /reload, it shows the reload status, or the failure as an error, and then completes without error; only a cancelled call or a crashed mode returns one.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:handleReloadCommand
func (m *InteractiveMode) reloadFromExtension(ctx context.Context) error {
	extension.CallInitiated(ctx)
	owner := m.runCtx
	if owner == nil {
		owner = ctx
	}
	return m.runOnMainAndWait(ctx, func() error {
		err := reloadHandler(m.buildSlashContext(ctxowner.WithValuesOf(owner, ctx)))
		if err == nil || ctx.Err() != nil || errors.Is(err, ErrInteractiveCrashed) {
			return err
		}
		m.showError(err.Error())
		return nil
	})
}

// beginReloadBlocker keeps the editor unavailable until the awaited reload finishes, including session_start handlers.
func (m *InteractiveMode) beginReloadBlocker() func() {
	if m.editorContainer == nil || m.tuiInst == nil {
		return func() {}
	}
	box := tui.NewContainer(
		tui.NewDynamicBorder(), tui.NewSpacer(1),
		themedNotice("muted", "Reloading keybindings, extensions, skills, prompts, themes, and context files...", 1),
		tui.NewSpacer(1), tui.NewDynamicBorder(),
	)
	m.editorContainer.SetChildren(box)
	m.tuiInst.SetFocus(box)
	// Pi shows the box with `requestRender(true)` (interactive-mode.ts:6648): the one clearing redraw of a reload. The transcript repaint that follows is differential, as in Pi.
	m.tuiInst.ForceFullRender()
	m.tuiInst.Render()
	// Pi's dismissReloadBox focuses the editor (interactive-mode.ts:6425-6430), not a selector the reload displaced.
	return func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.SetFocus(m.editor)
		m.tuiInst.RequestRender()
	}
}

// awaitReloadStep keeps UI mutations on the current owner while an awaited resource or extension operation runs off-loop. Cancellation joins the operation before its caller can restore focus or replace state. A shutdown requested during the step, as by an extension's ctx.shutdown(), finishes the session as the input loop does and returns errInteractiveShutdown without joining it.
func (m *InteractiveMode) awaitReloadStep(parent context.Context, work func(context.Context) error) error {
	if err := parent.Err(); err != nil {
		return context.Cause(parent)
	}
	// Pi's awaits between the steps read the keys that arrived after a step and drop them, so no step hands a key on to the next.
	m.reloadTailInput = nil
	if m.runCtx != nil {
		m.currentInputTicket.resume()
		m.currentInputTicket.settle()
	}
	// Loading may retain the operation context for a process lifetime. Normal completion must not cancel that owner.
	ctx := parent
	input, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		err = work(ctx)
	}()
	cancelled := ctx.Done()
	for {
		// requestShutdown's wake reaches this loop instead of the input loop it interrupted.
		// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:shutdown
		if m.requestExit.Load() {
			if err := m.finishInteractiveShutdown(); err != nil {
				return err
			}
			return errInteractiveShutdown
		}
		select {
		case <-done:
			// Pi's step is an awaited call in one process, so what the step's handlers asked of the UI (an extension's setEditorComponent from session_start)
			// has happened when it returns. Here the handler's posts are queued ahead of this wake, and the select above takes either at random.
			m.runQueuedUITasks()
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return err
		case <-cancelled:
			cancelled = nil
		case key := <-input:
			// The reload box owns input; it has no key actions, and a key that arrives while the step is pending is dropped, as in Pi (packages/tui/src/tui.ts:1153-1158). A key taken once the step has completed arrived after the awaited work: Pi reads it after the synchronous tail that follows its last await (interactive-mode.ts:6671-6698) and the editor, which the dismissed box gives the input back to, receives it. The input loop dispatches it, in order, before any key routed later (takeReloadTailInput); a later step drops it as Pi's awaits between steps do.
			select {
			case <-done:
				m.reloadTailInput = append(m.reloadTailInput, key)
			default:
			}
		case task := <-m.uiTaskCh:
			task()
		case <-m.renderWakeCh:
			m.runScheduledRender()
		case <-m.extensionErrorWakeCh:
			m.showPendingExtensionErrors()
		case event, ok := <-m.eventCh:
			if !ok {
				m.eventCh = nil
			} else {
				m.handleAgentEvent(event)
			}
		}
	}
}

// runQueuedUITasks runs the UI tasks already queued, in order, and none posted meanwhile that this goroutine would have to wait for. Owner loop only.
func (m *InteractiveMode) runQueuedUITasks() {
	for queued := len(m.uiTaskCh); queued > 0; queued-- {
		select {
		case task := <-m.uiTaskCh:
			task()
		default:
			return
		}
	}
}

// takeReloadTailInput returns the oldest key awaitReloadStep kept for the editor, or false when none waits. Owner loop only.
func (m *InteractiveMode) takeReloadTailInput() ([]byte, bool) {
	if len(m.reloadTailInput) == 0 {
		return nil, false
	}
	key := m.reloadTailInput[0]
	m.reloadTailInput[0] = nil
	m.reloadTailInput = m.reloadTailInput[1:]
	return key, true
}
