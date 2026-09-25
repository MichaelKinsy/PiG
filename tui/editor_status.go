package tui

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// SetWorkingStatusIndicator selects the status displayed by an opted-in editor.
func (e *Editor) SetWorkingStatusIndicator(indicator *StatusIndicator) {
	e.workingStatusIndicator = indicator
	e.Invalidate()
}

// IsDirty reports whether the editor's rendered lines changed since the last
// consume. The embedded status indicator is animated and relabelled by its
// owner (spinner ticks, working message, retry countdown) without touching the
// editor, so its own dirty flag counts too; otherwise the per-child render
// cache would keep the old border row until a keystroke dirtied the editor.
func (e *Editor) IsDirty() bool {
	if e.invalidatable.IsDirty() {
		return true
	}
	indicator := e.workingStatusIndicator
	return e.EmbedWorkingStatus && indicator != nil && indicator.IsDirty()
}

// NeedsRedraw consumes the editor's and the embedded indicator's dirty flags.
func (e *Editor) NeedsRedraw() bool {
	dirty := e.invalidatable.NeedsRedraw()
	if indicator := e.workingStatusIndicator; e.EmbedWorkingStatus && indicator != nil {
		dirty = indicator.NeedsRedraw() || dirty
	}
	return dirty
}

// renderStatusBorder mirrors CustomEditor.renderTopBorder, retaining a centered
// overflow label when the complete status or spinner can fit to its left.
func (e *Editor) renderStatusBorder(width, hiddenLineCount int, fallback string) string {
	if !e.EmbedWorkingStatus || e.workingStatusIndicator == nil || width <= 0 {
		return fallback
	}
	indicator := e.workingStatusIndicator
	if indicator.Kind == "working" {
		loader := &Loader{Message: indicator.Message, Frame: indicator.Frame, Frames: indicator.Frames, IndicatorVerbatim: indicator.IndicatorVerbatim, SpinnerColor: e.borderSGR(), MessageColor: e.borderSGR()}
		indicator = &StatusIndicator{Loader: loader, Kind: indicator.Kind}
	}
	status := indicator.RenderInBorder(max(1, width-5))
	statusWidth := widthx.VisibleWidth(status)
	if statusWidth == 0 {
		return fallback
	}
	overflowLabel := ""
	if hiddenLineCount > 0 {
		overflowLabel = fmt.Sprintf(" ↑ %d more ", hiddenLineCount)
	}
	overflowWidth := widthx.VisibleWidth(overflowLabel)
	overflowStart := (width - overflowWidth) / 2
	canFitOverflow := func() bool {
		return overflowLabel != "" && overflowWidth+2 <= width && overflowStart-(3+statusWidth+1) >= 1
	}
	if overflowLabel != "" && !canFitOverflow() {
		status = indicator.RenderSpinnerInBorder(width)
		statusWidth = widthx.VisibleWidth(status)
	}
	color := func(text string) string { return e.borderSGR() + text + "\x1b[0m" }
	if canFitOverflow() {
		leftWidth := 3 + statusWidth + 1
		return color("── ") + status + color(" "+strings.Repeat("─", overflowStart-leftWidth)+overflowLabel+strings.Repeat("─", width-overflowStart-overflowWidth))
	}
	if width >= statusWidth+5 {
		return color("── ") + status + color(" "+strings.Repeat("─", width-statusWidth-4))
	}
	status = indicator.RenderSpinnerInBorder(width)
	statusWidth = widthx.VisibleWidth(status)
	prefixWidth := min(3, max(0, width-statusWidth))
	return color(strings.Repeat("─", prefixWidth)) + status + color(strings.Repeat("─", max(0, width-prefixWidth-statusWidth)))
}
