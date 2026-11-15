package codingagent

import (
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Only the active editor can opt into border status.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:setEditorWorkingStatusIndicator
func (m *InteractiveMode) setEditorWorkingStatusIndicator(indicator *tui.StatusIndicator) bool {
	if m.editor == nil {
		return false
	}
	if m.remoteEditor != nil {
		embeds := m.remoteEditor.editor.EmbedWorkingStatus()
		// A delegated component draws its border through the host editor's own render (its `super.render`), so the indicator goes to that editor when the component opts in.
		if embeds && m.editor.IsDelegated() {
			m.editor.SetWorkingStatusIndicator(indicator)
		} else {
			m.editor.SetWorkingStatusIndicator(nil)
		}
		return embeds
	}
	if !m.editor.EmbedWorkingStatus {
		indicator = nil
	}
	m.editor.SetWorkingStatusIndicator(indicator)
	return m.editor.EmbedWorkingStatus
}

func (m *InteractiveMode) showStatusIndicator(indicator *tui.StatusIndicator) {
	m.clearStatusIndicator("")
	m.activeStatusIndicator = indicator
	m.activeWorkingIndicatorEmbedded = m.setEditorWorkingStatusIndicator(indicator)
	m.statusLastFrame = time.Now()
	m.statusContainer.Clear()
	if !m.activeWorkingIndicatorEmbedded {
		m.statusContainer.Add(indicator)
	}
	m.reconfigureRemoteEditor()
	m.resetSpinnerInterval()
}

func (m *InteractiveMode) clearStatusIndicator(kind string) {
	previous := m.activeStatusIndicator
	if kind != "" && (previous == nil || previous.Kind != kind) {
		return
	}
	embedded := m.activeWorkingIndicatorEmbedded
	if (kind == "" || (previous != nil && previous.Kind == "retry")) && m.retryCountdownStop != nil {
		m.retryCountdownStop()
		m.retryCountdownStop = nil
		m.retryEscapeHandler = false
	}
	m.activeStatusIndicator = nil
	m.activeWorkingIndicatorEmbedded = false
	m.setEditorWorkingStatusIndicator(nil)
	if m.statusContainer != nil {
		m.statusContainer.Clear()
		if previous != nil && !embedded && m.opts.TuiMode != "fullscreen" && m.tuiInst != nil && m.statusClearOnShrink() {
			m.statusContainer.Add(&tui.IdleStatus{})
		}
	}
	m.reconfigureRemoteEditor()
	m.resetSpinnerInterval()
	// pig divergence (D619): the removed indicator's step frame, if still waiting, would be drawn after the frame that removed it.
	if ui, ok := m.tuiInst.(tui.FrameWithinCanceler); ok && previous != nil && len(m.bashOrder) == 0 {
		ui.CancelRenderWithin()
	}
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
	// pig divergence (D652): the label's elapsed time and streamed size are rewritten by the status tick (tickCompactionMessage); a retry of the summary keeps the compaction's start.
	m.compactionLabel, m.compactionHint = label, m.statusCancelHint()
	m.compactionShownSecs, m.compactionShownTokens, m.compactionShownRoom = 0, 0, m.compactionRoom()
	m.showStatusIndicator(&tui.StatusIndicator{Kind: "compaction", Loader: tui.NewLoader(m.tuiInst, tui.ThemeFg("accent"), tui.ThemeFg("muted"), compactionStatusMessage(label, 0, 0, m.compactionHint, m.compactionShownRoom), nil)})
	if m.compactionStartedAt.IsZero() {
		m.compactionStartedAt = m.statusLastFrame
	}
}

// compactionRoom is the cells the editor border leaves the compaction message after its margins and the spinner: the terminal's width less 5 for the border and 2 for the spinner and its space, unbounded while the width is unknown.
func (m *InteractiveMode) compactionRoom() int {
	if width := m.tuiInst.Width(); width > 0 {
		return max(0, width-compactionBorderCells-compactionSpinnerCells)
	}
	return math.MaxInt
}

const (
	compactionBorderCells  = 5 // tui.Editor.renderStatusBorder gives a status the width less 5
	compactionSpinnerCells = 2 // the spinner frame and the space after it
)

// compactionStatusMessage is the compaction line: the label, the elapsed time from the first second, the streamed size once a delta has arrived, and the hint last, so that the old line's two halves still hold around them. When that does not fit room cells the size goes, then the elapsed time: the hint stays, Pi's line being the last form.
//
// pig divergence (D652): in a narrow terminal the elapsed time and the size give way before the cancel hint does.
func compactionStatusMessage(label string, secs, tokens int, hint string, room int) string {
	buf := make([]byte, 0, 128)
	buf = append(buf, label...)
	elapsedAt := len(buf)
	if secs > 0 {
		buf = appendCompactionElapsed(buf, secs)
	}
	tokensAt := len(buf)
	if tokens > 0 {
		if secs > 0 {
			buf = append(buf, " · ~"...)
		} else {
			buf = append(buf, '~')
		}
		buf = appendCompactionTokens(buf, tokens)
		buf = append(buf, " tokens"...)
	}
	hintAt := len(buf)
	if secs > 0 || tokens > 0 {
		buf = append(buf, ' ')
	}
	buf = append(buf, hint...)
	if compactionCells(buf) <= room {
		return string(buf)
	}
	if secs > 0 && tokens > 0 {
		buf = append(buf[:tokensAt], buf[hintAt:]...)
		if compactionCells(buf) <= room {
			return string(buf)
		}
	}
	buf = append(buf[:elapsedAt], hint...)
	return string(buf)
}

// compactionCells is the cells of the line in buf, each rune one: the line is the label, digits, "·", "~" and the key text of the hint. It converts nothing to a string, which utf8.RuneCount would for a line over 32 bytes.
func compactionCells(buf []byte) int {
	cells := 0
	for i := 0; i < len(buf); cells++ {
		if buf[i] < utf8.RuneSelf {
			i++
			continue
		}
		_, size := utf8.DecodeRune(buf[i:])
		i += size
	}
	return cells
}

// appendCompactionElapsed appends whole seconds as "12s", "1m 12s" or "1h 2m 3s": the form of the tool footer's tui.FormatToolDuration without its tenths, appended without an allocation.
func appendCompactionElapsed(buf []byte, secs int) []byte {
	if hours := secs / 3600; hours > 0 {
		buf = strconv.AppendInt(buf, int64(hours), 10)
		buf = append(buf, "h "...)
	}
	if minutes := secs % 3600 / 60; secs >= 60 {
		buf = strconv.AppendInt(buf, int64(minutes), 10)
		buf = append(buf, "m "...)
	}
	buf = strconv.AppendInt(buf, int64(secs%60), 10)
	return append(buf, 's')
}

// appendCompactionTokens appends the footer's token form: plain digits below a thousand, formatTokens above.
func appendCompactionTokens(buf []byte, tokens int) []byte {
	if tokens < 1000 {
		return strconv.AppendInt(buf, int64(tokens), 10)
	}
	return append(buf, formatTokens(tokens)...)
}

// tickCompactionMessage rewrites the compaction line when the elapsed seconds, the streamed size or the room for the line changed since it was last written: one string, on the status tick the owner loop already runs, with the tick's own clock reading. It reports whether the line changed.
//
// pig divergence (D652): the line carries the elapsed time and the streamed size, which Pi's does not.
func (m *InteractiveMode) tickCompactionMessage(now time.Time) bool {
	indicator := m.activeStatusIndicator
	if indicator == nil || indicator.Kind != "compaction" || m.compactionStartedAt.IsZero() {
		return false
	}
	secs := int(max(0, now.Sub(m.compactionStartedAt)) / time.Second)
	tokens := 0
	if handle := m.opts.SessionHandle; handle != nil {
		tokens = int(handle.CompactionStreamedTokens())
	}
	room := m.compactionRoom()
	if secs == m.compactionShownSecs && tokens == m.compactionShownTokens && room == m.compactionShownRoom {
		return false
	}
	m.compactionShownSecs, m.compactionShownTokens, m.compactionShownRoom = secs, tokens, room
	indicator.SetMessage(compactionStatusMessage(m.compactionLabel, secs, tokens, m.compactionHint, room))
	return true
}

func (m *InteractiveMode) showBranchSummaryStatusIndicator() {
	m.showStatusIndicator(&tui.StatusIndicator{Kind: "branchSummary", Loader: tui.NewLoader(m.tuiInst, tui.ThemeFg("accent"), tui.ThemeFg("muted"), "Summarizing branch... "+m.statusCancelHint(), nil)})
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
	if m.activeStatusIndicator != nil && m.activeStatusIndicator.Kind == "working" && m.workingIndicatorOptions != nil && m.workingIndicatorOptions.IntervalMs != nil && *m.workingIndicatorOptions.IntervalMs > 0 {
		return max(time.Millisecond, time.Duration(*m.workingIndicatorOptions.IntervalMs*float64(time.Millisecond)))
	}
	return 80 * time.Millisecond // upstream: tui/src/components/loader.ts:DEFAULT_INTERVAL_MS
}

func (m *InteractiveMode) resetSpinnerInterval() {
	if m.spinnerIntervalCh == nil {
		return
	}
	// An indicator change also invalidates any park request already in flight.
	m.spinnerWakeGen.Add(1)
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
	var options *tui.LoaderIndicatorOptions
	if working := m.workingIndicatorOptions; working != nil {
		options = &tui.LoaderIndicatorOptions{}
		if working.Frames != nil {
			options.Frames = *working.Frames
		}
		if working.IntervalMs != nil {
			options.IntervalMs = *working.IntervalMs
		}
	}
	indicator.SetIndicator(options)
}

func (m *InteractiveMode) setWorkingIndicator(options *extension.WorkingIndicatorOptions) {
	m.workingIndicatorOptions = options
	if indicator := m.activeStatusIndicator; indicator != nil && indicator.Kind == "working" {
		m.applyWorkingIndicatorOptions(indicator)
		m.statusLastFrame = time.Now()
		m.reconfigureRemoteEditor()
	}
	m.resetSpinnerInterval()
	if m.tuiInst != nil {
		m.tuiInst.RequestRender()
	}
}

// setWorkingMessage is Pi's setWorkingMessage (interactive-mode.ts:2641-2646): nil restores the default label, an empty string shows an empty one.
func (m *InteractiveMode) setWorkingMessage(message *string) {
	m.workingMessage = message
	if m.statusLine != nil {
		custom := ""
		if message != nil {
			custom = *message
		}
		m.statusLine.SetWorkingMessage(custom)
	}
	if indicator := m.activeStatusIndicator; indicator != nil && indicator.Kind == "working" {
		label := "Working"
		if message != nil {
			label = *message
		}
		indicator.SetMessage(label)
		m.reconfigureRemoteEditor()
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
		m.reconfigureRemoteEditor()
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
