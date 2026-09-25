package tui

import (
	"math"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// overlaySize is an internal absolute-cell or terminal-percentage value.
type overlaySize struct {
	value   float64
	percent bool
	set     bool
	invalid bool
}

func overlayCells(cells int) overlaySize {
	return overlaySize{value: float64(cells), set: true}
}

func overlayPercent(percent float64) overlaySize {
	return overlaySize{value: percent, percent: true, set: true}
}

type overlayAnchor string

const (
	overlayCenter       overlayAnchor = "center"
	overlayTopLeft      overlayAnchor = "top-left"
	overlayTopRight     overlayAnchor = "top-right"
	overlayBottomLeft   overlayAnchor = "bottom-left"
	overlayBottomRight  overlayAnchor = "bottom-right"
	overlayTopCenter    overlayAnchor = "top-center"
	overlayBottomCenter overlayAnchor = "bottom-center"
	overlayLeftCenter   overlayAnchor = "left-center"
	overlayRightCenter  overlayAnchor = "right-center"
)

type overlayMargin struct {
	Top, Right, Bottom, Left int
}

type overlayLayout struct {
	width, row, col int
	maxHeight       int
	hasMaxHeight    bool
}

func resolveOverlayLayout(opts OverlayOptions, overlayHeight, termWidth, termHeight int) overlayLayout {
	margin := opts.margin
	if opts.marginAll != nil {
		margin = overlayMargin{Top: *opts.marginAll, Right: *opts.marginAll, Bottom: *opts.marginAll, Left: *opts.marginAll}
	}
	margin.Top = max(0, margin.Top)
	margin.Right = max(0, margin.Right)
	margin.Bottom = max(0, margin.Bottom)
	margin.Left = max(0, margin.Left)
	availWidth := max(1, termWidth-margin.Left-margin.Right)
	availHeight := max(1, termHeight-margin.Top-margin.Bottom)

	width, ok := resolveOverlaySize(opts.width, termWidth)
	if !ok {
		if opts.WidthFraction > 0 {
			width = int(float64(termWidth) * opts.WidthFraction)
		} else {
			width = min(80, availWidth)
		}
	}
	width = max(width, opts.minWidth)
	width = max(1, min(width, availWidth))

	maxHeight, hasMaxHeight := resolveOverlaySize(opts.maxHeight, termHeight)
	if !hasMaxHeight && opts.HeightFraction > 0 {
		maxHeight = int(float64(termHeight) * opts.HeightFraction)
		hasMaxHeight = true
	}
	if hasMaxHeight {
		maxHeight = max(1, min(maxHeight, availHeight))
	}
	effectiveHeight := overlayHeight
	if hasMaxHeight {
		effectiveHeight = min(effectiveHeight, maxHeight)
	}

	anchor := opts.anchor
	if anchor == "" {
		anchor = overlayCenter
	}
	row := resolveOverlayRow(anchor, effectiveHeight, availHeight, margin.Top)
	if opts.row.set {
		if value, valid := resolvePosition(opts.row, max(0, availHeight-effectiveHeight)); valid {
			// Upstream offsets percentages by the margin but uses an
			// absolute row as given (the final clamp still applies).
			if opts.row.percent {
				row = margin.Top + value
			} else {
				row = value
			}
		} else {
			row = resolveOverlayRow(overlayCenter, effectiveHeight, availHeight, margin.Top)
		}
	}
	col := resolveOverlayCol(anchor, width, availWidth, margin.Left)
	if opts.col.set {
		if value, valid := resolvePosition(opts.col, max(0, availWidth-width)); valid {
			// Upstream offsets percentages by the margin but uses an
			// absolute col as given (the final clamp still applies).
			if opts.col.percent {
				col = margin.Left + value
			} else {
				col = value
			}
		} else {
			col = resolveOverlayCol(overlayCenter, width, availWidth, margin.Left)
		}
	}
	row += opts.offsetY
	col += opts.offsetX
	row = max(margin.Top, min(row, termHeight-margin.Bottom-effectiveHeight))
	col = max(margin.Left, min(col, termWidth-margin.Right-width))
	return overlayLayout{width: width, row: row, col: col, maxHeight: maxHeight, hasMaxHeight: hasMaxHeight}
}

func resolveOverlaySize(size overlaySize, reference int) (int, bool) {
	if !size.set || size.invalid || math.IsNaN(size.value) || math.IsInf(size.value, 0) {
		return 0, false
	}
	if size.percent {
		if size.value < 0 {
			return 0, false
		}
		return int(math.Floor(float64(reference) * size.value / 100)), true
	}
	return int(size.value), true
}

func resolvePosition(position overlaySize, available int) (int, bool) {
	if !position.set || position.invalid || math.IsNaN(position.value) || math.IsInf(position.value, 0) {
		return 0, false
	}
	if position.percent {
		if position.value < 0 {
			return 0, false
		}
		return int(math.Floor(float64(available) * position.value / 100)), true
	}
	return int(position.value), true
}

func resolveOverlayRow(anchor overlayAnchor, height, available, marginTop int) int {
	switch anchor {
	case overlayTopLeft, overlayTopCenter, overlayTopRight:
		return marginTop
	case overlayBottomLeft, overlayBottomCenter, overlayBottomRight:
		return marginTop + available - height
	default:
		return marginTop + (available-height)/2
	}
}

func resolveOverlayCol(anchor overlayAnchor, width, available, marginLeft int) int {
	switch anchor {
	case overlayTopLeft, overlayLeftCenter, overlayBottomLeft:
		return marginLeft
	case overlayTopRight, overlayRightCenter, overlayBottomRight:
		return marginLeft + available - width
	default:
		return marginLeft + (available-width)/2
	}
}

type modalOverlay struct {
	component Component
	title     string
}

func (m *modalOverlay) Render(width int) []string {
	return m.renderModal(width, 0)
}

func (m *modalOverlay) renderModal(width, height int) []string {
	if width < 3 || height == 1 {
		return m.component.Render(max(1, width))
	}
	lines := m.component.Render(max(1, width-2))
	if height <= 0 {
		height = len(lines) + 2
	}
	return drawBox(m.title, lines, width, height)
}

func (m *modalOverlay) Invalidate() { m.component.Invalidate() }

func renderOverlayEntry(entry *overlayEntry, width int, maxHeight int, hasMaxHeight bool) []string {
	var lines []string
	if entry.hasFrame {
		lines = entry.frame.lines
	} else if modal, ok := entry.component.(*modalOverlay); ok {
		height := 0
		if hasMaxHeight {
			height = maxHeight
		}
		lines = modal.renderModal(width, height)
	} else {
		lines = entry.component.Render(width)
	}
	if hasMaxHeight && len(lines) > maxHeight {
		lines = lines[:maxHeight]
	}
	return lines
}

type overlayCompositionStats struct {
	ComposedOutputBytes          int
	RetainedSnapshotPayloadBytes int
	// Layouts are the terminal-relative rectangles of the overlays painted this
	// frame, in paint order (bottom to top). Mirrors upstream
	// renderedOverlayLayouts.
	Layouts []renderedOverlayLayout
}

// renderedOverlayLayout is the last rendered terminal-relative rectangle of a
// visible overlay. Mirrors upstream RenderedOverlayLayout.
type renderedOverlayLayout struct {
	id        overlayID
	component Component
	focus     Component
	bounds    OverlayBounds
}

// OverlayBounds is an overlay's last rendered terminal-relative rectangle.
// Mirrors upstream OverlayBounds.
type OverlayBounds struct {
	Row    int
	Col    int
	Width  int
	Height int
}

func composeOverlaySnapshotWithStats(background []string, snapshot overlayStateSnapshot, termWidth, termHeight int) ([]string, overlayCompositionStats) {
	stats := overlayCompositionStats{RetainedSnapshotPayloadBytes: snapshot.RetainedBytes}
	if !snapshot.MountedAny {
		return background, stats
	}
	result := append([]string(nil), background...)
	type renderedOverlay struct {
		lines    []string
		row, col int
		width    int
		opaque   bool
	}
	rendered := make([]renderedOverlay, 0, len(snapshot.Entries))
	minimumLines := len(result)
	for i := range snapshot.Entries {
		entry := &snapshot.Entries[i]
		if !entry.visible() {
			continue
		}
		initial := resolveOverlayLayout(entry.opts, 0, termWidth, termHeight)
		lines := renderOverlayEntry(entry, initial.width, initial.maxHeight, initial.hasMaxHeight)
		final := resolveOverlayLayout(entry.opts, len(lines), termWidth, termHeight)
		_, opaque := entry.component.(*modalOverlay)
		rendered = append(rendered, renderedOverlay{
			lines: lines, row: final.row, col: final.col, width: final.width, opaque: opaque,
		})
		stats.Layouts = append(stats.Layouts, renderedOverlayLayout{
			id: entry.id, component: entry.component, focus: entry.focusComponent(),
			bounds: OverlayBounds{Row: final.row, Col: final.col, Width: final.width, Height: len(lines)},
		})
		minimumLines = max(minimumLines, final.row+len(lines))
	}
	workingHeight := max(len(result), termHeight, minimumLines)
	for len(result) < workingHeight {
		result = append(result, "")
	}
	viewportStart := max(0, workingHeight-termHeight)
	for _, overlay := range rendered {
		for row, line := range overlay.lines {
			index := viewportStart + overlay.row + row
			if index < 0 || index >= len(result) {
				continue
			}
			if widthx.IsImageLine(result[index]) {
				continue
			}
			if overlay.opaque {
				result[index] = overlayLine(result[index], line, overlay.col, termWidth)
			} else {
				result[index] = compositeTuiLine(result[index], line, overlay.col, overlay.width, termWidth)
			}
			stats.ComposedOutputBytes += len(result[index])
		}
	}
	return result, stats
}
