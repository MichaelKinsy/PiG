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
	previous := m.tuiInst.GetFocusedComponent()
	box := tui.NewContainer(
		tui.NewDynamicBorder(), tui.NewSpacer(1),
		themedNotice("muted", "Reloading keybindings, extensions, skills, prompts, themes, and context files...", 1),
		tui.NewSpacer(1), tui.NewDynamicBorder(),
	)
	m.editorContainer.SetChildren(box)
	m.tuiInst.SetFocus(box)
	m.tuiInst.Render()
	return func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.SetFocus(previous)
		m.tuiInst.RequestRender()
	}
}

// awaitReloadStep keeps UI mutations on the current owner while an awaited resource or extension operation runs off-loop. Cancellation joins the operation before its caller can restore focus or replace state.
func (m *InteractiveMode) awaitReloadStep(parent context.Context, work func(context.Context) error) error {
	if err := parent.Err(); err != nil {
		return context.Cause(parent)
	}
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
		select {
		case <-done:
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return err
		case <-cancelled:
			cancelled = nil
		case <-input:
			// The reload box owns input; it has no key actions.
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
