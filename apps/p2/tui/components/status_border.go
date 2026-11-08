package components

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Render is the editor's rows with the top border of p2's CustomEditor.renderTopBorder: the working status indicator in the border, left of a
// centered "↑ N more" label when both fit. The TypeScript overrides renderTopBorder, which calls super for the plain border; Go has no virtual
// dispatch, so this takes the editor's own top row as that super call and draws over it.
func (e *CustomEditor) Render(width int) []string {
	rows := e.Editor.Render(width)
	if e.indicator == nil || width <= 0 || len(rows) == 0 {
		return rows
	}
	rows[0] = e.topBorder(width, rows[0])
	return rows
}

// IsDirty reports a change of the editor or of the indicator, which animates and relabels itself without touching the editor.
func (e *CustomEditor) IsDirty() bool {
	return e.Editor.IsDirty() || e.indicator != nil && e.indicator.IsDirty()
}

// NeedsRedraw consumes the dirty flags of the editor and of the indicator.
func (e *CustomEditor) NeedsRedraw() bool {
	dirty := e.Editor.NeedsRedraw()
	if e.indicator != nil {
		dirty = e.indicator.NeedsRedraw() || dirty
	}
	return dirty
}

// SurfaceLive reports an indicator, which changes without invalidating the editor, drawing in it (D91).
func (e *CustomEditor) SurfaceLive() bool { return e.indicator != nil }

// borderOverflow reads the hidden-lines label from the editor's own top border (the plain border is only "─"): overflow reports that lines are
// hidden above, label is " ↑ N more " when the border shows it whole. The editor truncates the label exactly when `labelWidth + 2 > width`,
// which is also what makes canFitOverflow false in renderTopBorder, so the label that is not there is never needed.
func borderOverflow(plainBorder string) (label string, overflow bool) {
	text := widthx.StripTerminalSequences(plainBorder)
	if strings.Trim(text, "─") == "" {
		return "", false
	}
	_, after, _ := strings.Cut(text, "↑ ")
	digits, _, whole := strings.Cut(after, " more ")
	if _, err := strconv.Atoi(digits); !whole || err != nil {
		return "", true
	}
	return " ↑ " + digits + " more ", true
}

// topBorder is CustomEditor.renderTopBorder; plain is `super.renderTopBorder(width, hiddenLineCount)`.
func (e *CustomEditor) topBorder(width int, plain string) string {
	color := e.borderColor
	status := e.indicator.RenderInBorder(max(1, width-5))
	statusWidth := widthx.VisibleWidth(status)
	if statusWidth == 0 {
		return plain
	}
	overflowLabel, overflow := borderOverflow(plain)
	overflowWidth := widthx.VisibleWidth(overflowLabel)
	overflowStart := (width - overflowWidth) / 2
	canFitOverflow := func() bool {
		return overflowLabel != "" && overflowWidth+2 <= width && overflowStart-(3+statusWidth+1) >= 1
	}
	if overflow && !canFitOverflow() {
		status = e.indicator.RenderSpinnerInBorder(width)
		statusWidth = widthx.VisibleWidth(status)
	}
	if canFitOverflow() {
		leftWidth := 3 + statusWidth + 1
		return color("── ") + status + color(" "+strings.Repeat("─", overflowStart-leftWidth)+overflowLabel+strings.Repeat("─", width-overflowStart-overflowWidth))
	}
	if width >= statusWidth+5 {
		return color("── ") + status + color(" "+strings.Repeat("─", width-statusWidth-4))
	}
	status = e.indicator.RenderSpinnerInBorder(width)
	statusWidth = widthx.VisibleWidth(status)
	prefixWidth := min(3, max(0, width-statusWidth))
	return color(strings.Repeat("─", prefixWidth)) + status + color(strings.Repeat("─", max(0, width-prefixWidth-statusWidth)))
}
