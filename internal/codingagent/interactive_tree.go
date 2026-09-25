package codingagent

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/tui"
)

// navigateTree awaits the Session off the input loop while the owner continues
// dispatching editor input, events, and renders. Its cancellation belongs to the
// navigation, not the agent turn, including while a retry replaces the status.
func (m *InteractiveMode) navigateTree(ctx context.Context, targetID string, summarize bool, customInstructions string) (NavigateTreeResult, error) {
	handle := m.opts.SessionHandle
	if handle == nil {
		return NavigateTreeResult{Cancelled: true}, nil
	}
	if m.runStreaming() {
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
	if summarize {
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

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	type navigationResult struct {
		result NavigateTreeResult
		err    error
	}
	done := make(chan navigationResult, 1)
	go func() {
		result, err := handle.NavigateTreeHandle(navCtx, targetID, summarize, customInstructions)
		// Session events pass through an ordered dispatcher. Await its FIFO
		// barrier before clearing the status, including after an Esc abort.
		if flusher, ok := handle.(interface{ FlushEvents(context.Context) error }); ok {
			if flushErr := flusher.FlushEvents(flushCtx); err == nil {
				err = flushErr
			}
		}
		done <- navigationResult{result, err}
	}()
	var inputErr error
	cancelled := navCtx.Done()
	for {
		select {
		case outcome := <-done:
			if inputErr != nil {
				return outcome.result, inputErr
			}
			if outcome.err == nil && !outcome.result.Cancelled && !outcome.result.Aborted {
				m.rebuildChatFromSession()
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
			m.tuiInst.Render()
		case event, ok := <-m.eventCh:
			if !ok {
				m.eventCh = nil
				continue
			}
			m.handleAgentEvent(event)
		case apply := <-m.uiTaskCh:
			apply()
		case <-m.renderWakeCh:
			m.runScheduledRender()
		case <-m.extensionErrorWakeCh:
			m.showPendingExtensionErrors()
		}
	}
}
