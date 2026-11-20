package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts.

import (
	"context"
	"errors"
	"strings"

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

// pig additive (D658): reloadContinuationFromExtension admits the operation on the owner loop. A tool cannot await idle while the agent awaits that tool, so a busy call only queues the reload.
func (m *InteractiveMode) reloadContinuationFromExtension(ctx context.Context, continuation string) error {
	extension.CallInitiated(ctx)
	return m.runOnMainAndWait(ctx, func() error {
		if m.runStreaming() {
			m.pendingReloadContinuations = append(m.pendingReloadContinuations, continuation)
			m.showStatus("Reload scheduled after this turn.")
			return nil
		}
		return m.performReloadContinuation(m.runCtx, continuation, false)
	})
}

// runPendingReloadContinuations runs only after the agent and settled handlers finish. If another turn started first, its settlement owns the pending operations instead.
func (m *InteractiveMode) runPendingReloadContinuations() {
	if m.runStreaming() || m.isCompacting || len(m.pendingReloadContinuations) == 0 {
		return
	}
	pending := m.pendingReloadContinuations
	m.pendingReloadContinuations = nil
	for _, continuation := range pending {
		if err := m.performReloadContinuation(m.runCtx, continuation, true); err != nil {
			m.showError(err.Error())
		}
	}
}

func (m *InteractiveMode) performReloadContinuation(ctx context.Context, continuation string, deferred bool) error {
	if ctx == nil {
		return errors.New("reload continuation: interactive owner is not available")
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	// Call the real reload, not the slash wrapper that intentionally swallows a streaming refusal.
	err := m.buildSlashContext(ctx).Reload()
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if err == nil && len(m.reloadIssues) > 0 {
		err = errors.New(strings.Join(m.reloadIssues, "; "))
	}
	if err == nil && m.opts.SubprocessHost != nil {
		if report := m.opts.SubprocessHost.LastReloadReport(); report != nil && len(report.Issues) > 0 {
			err = errors.New(strings.Join(report.Issues, "; "))
		}
	}
	if err != nil && !deferred {
		return err
	}
	return m.deliverUserMessage(reloadContinuationText(continuation, err), extension.DeliverAsFollowUp)
}

func reloadContinuationText(continuation string, err error) string {
	if err == nil {
		return continuation
	}
	return "[reload failed: " + strings.Join(strings.Fields(err.Error()), " ") + "]\n" + continuation
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
