package codingagent

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/tui"
)

// copySelectedTreeMessage owns the clipboard write off-loop and reports completion on the UI loop.
func (m *InteractiveMode) copySelectedTreeMessage(text *string) {
	if text == nil || *text == "" {
		m.showError("Selected entry has no text to copy")
		return
	}
	value := *text
	copyClipboard := m.effectiveCopyClipboard()
	ctx := m.backgroundCtx
	if ctx == nil {
		ctx = m.runCtx
	}
	m.backgroundTasks.Go(func() {
		if ctx != nil && ctx.Err() != nil {
			return
		}
		err := copyClipboard(value)
		m.runOnMain(ctx, func() {
			if ctx != nil && ctx.Err() != nil {
				return
			}
			if err != nil {
				m.showError(err.Error())
				return
			}
			m.showStatus("Copied selected message to clipboard")
		})
	})
}

// navigateTree awaits the Session off the input loop while the owner continues
// dispatching editor input and events. Its cancellation belongs to the
// navigation, not the agent turn, including while a retry replaces the status.
//
// pig divergence (D270): the frames of a /tree navigation are held until it returns.
// The frames of the choice that starts the navigation stay held until it returns, so the first frame after the choice is the fork point.
// This diverges from Pi (D270). Pi's tree handler awaits session.navigateTree and then runs `chatContainer.clear(); renderInitialMessages()`
// (interactive-mode.ts:5750, 5768), but the key that closes the prompt asks for an immediate frame (tui.ts:1158-1161) that runs on
// process.nextTick (tui.ts:1044-1058), before the microtask that resumes the await: Pi paints the old transcript with the prompt gone first, and
// keeps it for as long as the navigation takes, which is milliseconds unless an extension handler awaits. Here the work runs on a worker, and
// it took seconds on a large session. A summary shows its status at once, so its hold ends where the status is painted; a dialog or an overlay
// an extension opens during the navigation ends the hold, because its frames must reach the screen.
func (m *InteractiveMode) navigateTree(ctx context.Context, targetID string, summarize bool, customInstructions string) (NavigateTreeResult, error) {
	handle := m.opts.SessionHandle
	if handle == nil {
		return NavigateTreeResult{Cancelled: true}, nil
	}
	if m.runStreaming() {
		// Aborting the run can take a while, and its events and tool output must keep painting.
		m.releaseFrames()
		m.restoreQueuedMessagesToEditor(false)
		if err := m.settleActiveRun(); err != nil {
			return NavigateTreeResult{}, err
		}
	}
	if m.isCompacting {
		return NavigateTreeResult{}, errors.New("Wait for the current compaction or tree navigation to finish before navigating the session tree.")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	navCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	flushCtx := ctx
	if m.runCtx != nil {
		stop := context.AfterFunc(m.runCtx, cancel)
		defer stop()
		flushCtx = m.runCtx
	}
	m.isCompacting = true
	defer func() { m.isCompacting = false }()
	if !summarize {
		m.holdFrames()
	} else {
		m.releaseFrames()
		m.branchSummaryCancel = cancel
		m.appendToChat(tui.NewSpacer(1))
		m.showBranchSummaryStatusIndicator()
		m.tuiInst.Render()
		defer func() {
			m.clearStatusIndicator("branchSummary")
			m.branchSummaryCancel = nil
			m.tuiInst.RequestRender()
		}()
	}

	defer m.releaseFrames()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	type navigationResult struct {
		result NavigateTreeResult
		err    error
		// entries are the transcript's entries, read on the worker once the navigation moved the leaf, so the repaint decodes nothing on the owner loop.
		entries []SessionEntry
	}
	session, showCacheMisses := m.currentSession(), m.showCacheMissNotices()
	done := make(chan navigationResult, 1)
	go func() {
		result, err := handle.NavigateTreeHandle(navCtx, targetID, NavigateTreeOptions{Summarize: summarize, CustomInstructions: customInstructions})
		var entries []SessionEntry
		if err == nil && !result.Cancelled && !result.Aborted {
			entries = transcriptEntries(session, showCacheMisses)
		}
		// Session events pass through an ordered dispatcher. Await its FIFO
		// barrier before clearing the status, including after an Esc abort.
		if flusher, ok := handle.(interface{ FlushEvents(context.Context) error }); ok {
			if flushErr := flusher.FlushEvents(flushCtx); err == nil {
				err = flushErr
			}
		}
		done <- navigationResult{result, err, entries}
	}()
	var inputErr error
	cancelled := navCtx.Done()
	for {
		if m.requestExit.Load() {
			cancel()
		}
		select {
		case err := <-m.inputErrCh:
			inputErr = err
			cancel()
			inputCh = nil
		case outcome := <-done:
			if inputErr != nil {
				return outcome.result, inputErr
			}
			if outcome.err == nil && !outcome.result.Cancelled && !outcome.result.Aborted {
				m.repaintInitialMessagesFrom(outcome.entries)
			}
			return outcome.result, outcome.err
		case <-cancelled:
			// Keep draining events and UI callbacks until the worker joins.
			cancelled = nil
		case buf := <-inputCh:
			if err := m.dispatchKey(ctx, string(buf)); err != nil {
				inputErr = err
				cancel()
				inputCh = nil
			}
			if m.requestExit.Load() {
				cancel()
			}
			if !m.frameHold {
				m.tuiInst.Render()
			}
		case event, ok := <-m.eventCh:
			if !ok {
				m.eventCh = nil
				continue
			}
			m.handleAgentEvent(event)
		case apply := <-m.uiTaskCh:
			apply()
			// What needs the screen besides the editor ends the hold: a dialog, or an overlay (an extension's ctx.ui.custom), which takes the keys.
			if len(m.extensionDialogs) > 0 || m.tuiInst.HasOverlay() {
				m.releaseFrames()
			}
		case <-m.renderWakeCh:
			m.runScheduledRender()
		case <-m.extensionErrorWakeCh:
			m.showPendingExtensionErrors()
		}
	}
}
