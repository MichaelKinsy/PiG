package codingagent

import (
	"context"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// extensionDialogReservedLines is the terminal budget kept for chrome around an
// open extension dialog: the status container, the footer, and a blank
// separator.
//
// Approximate on purpose. The exact figure would be the rendered height of
// every layout sibling, but reading it means rendering them on each dialog open
// and each resize, on the UI loop, and exposing the container's children. That
// cost buys at most a row or two of transcript.
//
// The approximation is the mechanism, not this number: upstream proportions the
// viewport natively and never computes a line budget, so SetMaxLines is a
// line-renderer stand-in for a layout pass pig does not have. A proportional
// layout would delete this constant rather than refine it.
const extensionDialogReservedLines = 3

// minExtensionDialogChatLines is the floor for transcript lines kept visible
// while an extension dialog is open. On a terminal too short for both, the
// dialog wins, but the transcript never renders empty and leaves the user
// answering with no context at all.
const minExtensionDialogChatLines = 3

// setExtensionDialogViewMode caps (or restores) the chat container so an open
// extension dialog has room without hiding the reasoning that motivated it
// (#667).
func (m *InteractiveMode) setExtensionDialogViewMode(on bool, dialogLines int) {
	if !on {
		m.chatContainer.SetMaxLines(0)
		m.tuiInst.Render()
		return
	}
	available := m.tuiInst.Height() - dialogLines - extensionDialogReservedLines
	m.chatContainer.SetMaxLines(max(available, minExtensionDialogChatLines))
	m.tuiInst.Render()
}

// onTerminalHeightChange keeps height-dependent state current across a resize.
// The extension-dialog chat cap is derived from terminal height, so without
// this a resize while a dialog is open leaves the previous height's cap in
// place.
func (m *InteractiveMode) onTerminalHeightChange(height int) {
	if m.opts.SubprocessHost != nil {
		m.opts.SubprocessHost.NotifyHeight(height)
	}
	if m.extensionDialog != nil {
		m.setExtensionDialogViewMode(true, m.extensionDialogLines(m.extensionDialog.component))
	}
}

// extensionDialogLines measures a dialog component at the current width so the
// chat cap reflects the dialog actually on screen rather than a fixed guess.
func (m *InteractiveMode) extensionDialogLines(component tui.Component) int {
	width := m.tuiInst.Width()
	if width <= 0 || component == nil {
		return 0
	}
	return len(component.Render(width))
}

func (m *InteractiveMode) setStatusContainerLabel(label string) {
	if m.activeStatusIndicator != nil && m.activeStatusIndicator.Kind == "retry" {
		m.activeStatusIndicator.SetMessage(label)
		return
	}
	m.showStatusIndicator(&tui.StatusIndicator{Kind: "retry", Loader: tui.NewStyledLoader(tui.ActiveTheme().Warning, tui.ActiveTheme().Muted, label, nil)})
}

func (m *InteractiveMode) startWorkingLoader() {
	if !m.workingVisible {
		return
	}
	message := m.workingMessage
	if message == "" {
		message = "Working"
	}
	indicator := &tui.StatusIndicator{Kind: "working", Loader: tui.NewStyledLoader(tui.ActiveTheme().Accent, tui.ActiveTheme().Muted, message, nil)}
	m.applyWorkingIndicatorOptions(indicator)
	m.showStatusIndicator(indicator)
	m.tuiInst.RequestRender()
}

func (m *InteractiveMode) stopWorkingLoader() { m.clearStatusIndicator("working") }

func (m *InteractiveMode) tickSpinner(ctx context.Context) {
	ticker := time.NewTicker(80 * time.Millisecond) // upstream: tui/src/components/loader.ts:DEFAULT_INTERVAL_MS
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case interval := <-m.spinnerIntervalCh:
			ticker.Reset(interval)
		case <-ticker.C:
			// Like a Node setInterval on a blocked event loop, ticks that come
			// due while one is still queued collapse into it; a stalled loop
			// must not wake to a backlog of frames or a task queue full of
			// ticks that crowds out other posts.
			if !m.spinnerTickQueued.CompareAndSwap(false, true) {
				continue
			}
			if !m.postUITask(func() {
				m.spinnerTickQueued.Store(false)
				if ctx.Err() != nil {
					return
				}
				m.tickStatusIndicators(time.Now())
			}) {
				m.spinnerTickQueued.Store(false)
			}
		}
	}
}

func (m *InteractiveMode) tickStatusIndicators(now time.Time) {
	if m.isIdle && m.activeStatusIndicator == nil {
		return
	}
	if m.statusLine != nil {
		m.statusLine.Invalidate()
	}
	if indicator := m.activeStatusIndicator; indicator != nil && now.Sub(m.statusLastFrame) >= m.statusFrameInterval() {
		if len(indicator.Frames) > 1 {
			indicator.Tick()
		}
		m.statusLastFrame = now
		// Node's setInterval rearms from callback entry, not the previous deadline. Rearm only a due callback so dispatch jitter cannot skip a frame or postpone a replacement indicator's first frame.
		m.resetSpinnerInterval()
	}
	for _, block := range m.bashOrder {
		if loader := block.Loader(); loader != nil {
			loader.Tick()
		}
	}
	m.tuiInst.Render()
}

// openExternalEditor hands the terminal to $VISUAL || $EDITOR with the
// current editor buffer content, then loads the result back.
//
// Wraps OpenExternalEditor (the pure helper) with the TUI lifecycle:
//  1. show cursor (editor needs it visible);
//  2. restore cooked-mode terminal (rawRestore);
//  3. run editor;
//  4. re-enter raw mode + hide cursor;
//  5. SetText if successful, flash status either way;
//  6. RepaintAll: the editor may have left arbitrary ANSI state.
