package codingagent

import (
	"fmt"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

type workingIndicatorOptions struct {
	Frames     []string `json:"frames"`
	IntervalMs float64  `json:"intervalMs"`
}

func (m *InteractiveMode) showStatusIndicator(indicator *tui.StatusIndicator) {
	m.clearStatusIndicator("")
	m.activeStatusIndicator = indicator
	m.activeWorkingIndicatorEmbedded = m.editor != nil && m.editor.EmbedWorkingStatus
	m.statusLastFrame = time.Now()
	m.statusContainer.Clear()
	if m.activeWorkingIndicatorEmbedded {
		m.editor.SetWorkingStatusIndicator(indicator)
	} else {
		m.statusContainer.Add(indicator)
	}
	m.resetSpinnerInterval()
}

func (m *InteractiveMode) clearStatusIndicator(kind string) {
	previous := m.activeStatusIndicator
	if kind != "" && (previous == nil || previous.Kind != kind) {
		return
	}
	embedded := m.activeWorkingIndicatorEmbedded
	if previous != nil && previous.Kind == "retry" && m.retryCountdownStop != nil {
		m.retryCountdownStop()
		m.retryCountdownStop = nil
	}
	m.activeStatusIndicator = nil
	m.activeWorkingIndicatorEmbedded = false
	if m.editor != nil {
		m.editor.SetWorkingStatusIndicator(nil)
	}
	if m.statusContainer != nil {
		m.statusContainer.Clear()
		if previous != nil && !embedded && m.opts.Settings.TuiMode != "fullscreen" && m.tuiInst != nil && m.statusClearOnShrink() {
			m.statusContainer.Add(&tui.IdleStatus{})
		}
	}
	m.resetSpinnerInterval()
}

func (m *InteractiveMode) statusCancelHint() string {
	key := "escape"
	if m.keybindings != nil {
		key = m.keybindings.KeyText("app.interrupt")
	}
	return "(" + key + " to cancel)"
}

func (m *InteractiveMode) showCompactionStatusIndicator(reason string) {
	label := "Auto-compacting... "
	switch reason {
	case "manual":
		label = "Compacting context... "
	case "overflow":
		label = "Context overflow detected, Auto-compacting... "
	}
	m.showStatusIndicator(&tui.StatusIndicator{Kind: "compaction", Loader: tui.NewStyledLoader(tui.ActiveTheme().Accent, tui.ActiveTheme().Muted, label+m.statusCancelHint(), nil)})
}

func (m *InteractiveMode) showBranchSummaryStatusIndicator() {
	m.showStatusIndicator(&tui.StatusIndicator{Kind: "branchSummary", Loader: tui.NewStyledLoader(tui.ActiveTheme().Accent, tui.ActiveTheme().Muted, "Summarizing branch... "+m.statusCancelHint(), nil)})
}

// showRetryStatusIndicator owns one countdown until replacement, disposal, or shutdown.
// The owner loop applies its queued labels; disposal joins the worker even if it is waiting for queue space.
func (m *InteractiveMode) showRetryStatusIndicator(attempt, maxAttempts, delayMs int) {
	m.clearStatusIndicator("")
	remaining := int((delayMs + 999) / 1000)
	cancelHint := m.statusCancelHint()
	message := func(seconds int) string {
		return fmt.Sprintf("Retrying (%d/%d) in %ds... %s", attempt, maxAttempts, seconds, cancelHint)
	}
	m.setStatusContainerLabel(message(remaining))
	stop := make(chan struct{})
	stopped := make(chan struct{})
	m.retryCountdownStop = func() {
		close(stop)
		<-stopped
	}
	var done <-chan struct{}
	if m.runCtx != nil {
		done = m.runCtx.Done()
	}
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-done:
				return
			case <-ticker.C:
				remaining--
				m.postRetryStatusUpdate(stop, message(remaining))
				if remaining <= 0 {
					return
				}
			}
		}
	}()
}

func (m *InteractiveMode) statusFrameInterval() time.Duration {
	if m.activeStatusIndicator != nil && m.activeStatusIndicator.Kind == "working" && m.workingIndicatorOptions != nil && m.workingIndicatorOptions.IntervalMs > 0 {
		return max(time.Millisecond, time.Duration(m.workingIndicatorOptions.IntervalMs*float64(time.Millisecond)))
	}
	return 80 * time.Millisecond // upstream: tui/src/components/loader.ts:DEFAULT_INTERVAL_MS
}

func (m *InteractiveMode) resetSpinnerInterval() {
	if m.spinnerIntervalCh == nil {
		return
	}
	select {
	case <-m.spinnerIntervalCh:
	default:
	}
	select {
	case m.spinnerIntervalCh <- m.statusFrameInterval():
	default:
	}
}

func (m *InteractiveMode) applyWorkingIndicatorOptions(indicator *tui.StatusIndicator) {
	var frames []string
	if options := m.workingIndicatorOptions; options != nil {
		frames = options.Frames
	}
	indicator.SetIndicator(frames, m.workingIndicatorOptions != nil)
}

func (m *InteractiveMode) setWorkingIndicator(options *workingIndicatorOptions) {
	m.workingIndicatorOptions = options
	if indicator := m.activeStatusIndicator; indicator != nil && indicator.Kind == "working" {
		m.applyWorkingIndicatorOptions(indicator)
		m.statusLastFrame = time.Now()
	}
	m.resetSpinnerInterval()
	if m.tuiInst != nil {
		m.tuiInst.RequestRender()
	}
}

func (m *InteractiveMode) setWorkingMessage(message string) {
	m.workingMessage = message
	if m.statusLine != nil {
		m.statusLine.SetWorkingMessage(message)
	}
	if indicator := m.activeStatusIndicator; indicator != nil && indicator.Kind == "working" {
		if message == "" {
			message = "Working"
		}
		indicator.SetMessage(message)
		if m.tuiInst != nil {
			m.tuiInst.RequestRender()
		}
	}
}

func (m *InteractiveMode) updateStatusOnOwner(update func()) {
	if m.runCtx == nil {
		update()
		return
	}
	m.runOnMain(m.runCtx, update)
}

// postRetryStatusUpdate delivers one countdown second to the owner loop. Like
// upstream CountdownTimer's setInterval callback, every second runs, late
// under load but never skipped: the countdown goroutine waits for queue space
// (its ticker coalesces seconds that come due meanwhile) until the countdown
// is stopped or the session ends. Queued frames belong to their original
// operation, even if a new status replaces it before the owner loop runs them.
func (m *InteractiveMode) postRetryStatusUpdate(stop <-chan struct{}, label string) {
	var done <-chan struct{}
	if m.runCtx != nil {
		done = m.runCtx.Done()
	}
	update := func() {
		select {
		case <-stop:
			return
		default:
		}
		if m.activeStatusIndicator == nil || m.activeStatusIndicator.Kind != "retry" {
			return
		}
		m.activeStatusIndicator.SetMessage(label)
		m.tuiInst.Render()
	}
	select {
	case m.uiTaskCh <- update:
	case <-stop:
	case <-done:
	}
}

func (m *InteractiveMode) statusClearOnShrink() bool {
	if renderer, ok := m.tuiInst.(interface{ GetClearOnShrink() bool }); ok {
		return renderer.GetClearOnShrink()
	}
	return m.opts.Settings.GetClearOnShrink()
}
