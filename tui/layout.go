package tui

import (
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports pi-tui's layout.ts: the fullscreen layout engine that lays a component
// tree into a fixed viewport and paints it, including scroll viewports and their
// scrollbars. Names/structure mirror upstream. Forced Go mechanics: the
// number|undefined allocated height is *int (nil == undefined/auto); the
// Map<Component, Map<number, string[]>> render cache is a nested Go map keyed by
// component identity (pig components are pointers); the discriminated LayoutNode
// union is dispatched with a type switch; JS Math.round becomes math.Round
// (identical for the non-negative values here).

// osc133ZonePrefix matches a run of OSC 133 A/B/C shell-integration zone marks
// at the start of a line, which the paint path strips. Mirrors upstream
// OSC133_ZONE_PREFIX.
var osc133ZonePrefix = regexp.MustCompile("^(?:\x1b\\]133;[ABC](?:\x07|\x1b\\\\))+")

// osc133ZoneMark is the fixed start of every mark osc133ZonePrefix matches.
const osc133ZoneMark = "\x1b]133;"

// stripOsc133ZonePrefix removes a leading run of OSC 133 zone marks, as
// line.replace(OSC133_ZONE_PREFIX, "") does upstream. The pattern is anchored
// at the start of the line, so a line not starting with a mark is returned
// as is without running the regexp, whose ReplaceAllString copies the line
// even when nothing matches. This runs for every painted row of every frame.
func stripOsc133ZonePrefix(line string) string {
	if !strings.HasPrefix(line, osc133ZoneMark) {
		return line
	}
	return osc133ZonePrefix.ReplaceAllString(line, "")
}

// LayoutRect is an axis-aligned rectangle in terminal cells.
type LayoutRect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// LayoutBox is one node in the laid-out tree. Lines (non-nil) marks a leaf whose
// rendered lines paint directly; ScrollView/ScrollContentLines mark a scroll
// viewport. Optional upstream fields are zero values here.
type LayoutBox struct {
	Component          Component
	Rect               LayoutRect
	Clip               LayoutRect
	Children           []*LayoutBox
	Parent             *LayoutBox
	Lines              []string
	LineOffset         int
	ScrollView         *ScrollView
	ScrollContentLines []string
	Layer              int
}

// LayoutFrame is a fully laid-out and painted viewport.
type LayoutFrame struct {
	Root              *LayoutBox
	Width             int
	Height            int
	Lines             []string
	PrimaryScrollView *ScrollView
}

// ScrollbarGeometry describes where a scroll viewport's scrollbar paints.
type ScrollbarGeometry struct {
	Column       int
	TrackTop     int
	TrackHeight  int
	ThumbTop     int
	ThumbHeight  int
	MaxScrollTop int
}

type layoutContext struct {
	viewport          LayoutViewport
	renderCache       map[Component]map[int][]string
	requestRender     func()
	primaryScrollView *ScrollView
}

func intersect(a, b LayoutRect) LayoutRect {
	x := max(a.X, b.X)
	y := max(a.Y, b.Y)
	right := min(a.X+a.Width, b.X+b.Width)
	bottom := min(a.Y+a.Height, b.Y+b.Height)
	return LayoutRect{X: x, Y: y, Width: max(0, right-x), Height: max(0, bottom-y)}
}

// renderCached memoizes component.Render per (component, width) for one frame.
func renderCached(context *layoutContext, component Component, width int) []string {
	safeWidth := max(1, width)
	widths := context.renderCache[component]
	if widths == nil {
		widths = map[int][]string{}
		context.renderCache[component] = widths
	}
	lines, ok := widths[safeWidth]
	if !ok {
		switch concrete := component.(type) {
		case *Container:
			lines = concrete.renderBorrowed(safeWidth)
		case *altScreenDocument:
			lines = concrete.renderBorrowed(safeWidth)
		default:
			lines = component.Render(safeWidth)
		}
		if lines == nil {
			lines = []string{}
		}
		widths[safeWidth] = lines
	}
	return lines
}

func measureHeight(context *layoutContext, component Component, width int) int {
	return len(renderCached(context, component, width))
}

func measureWidth(context *layoutContext, component Component, width int) int {
	m := 0
	for _, line := range renderCached(context, component, width) {
		m = max(m, widthx.VisibleWidth(line))
	}
	return m
}

func withParent(box, parent *LayoutBox) *LayoutBox {
	box.Parent = parent
	return box
}

func translateBox(box *LayoutBox, deltaY int) {
	box.Rect.Y += deltaY
	for _, child := range box.Children {
		translateBox(child, deltaY)
	}
}

func updateClips(box *LayoutBox, parentClip LayoutRect) {
	box.Clip = intersect(parentClip, box.Rect)
	for _, child := range box.Children {
		updateClips(child, box.Clip)
	}
}

// intrinsicSize resolves a stack entry's basis: a fixed basis, else measured.
func intrinsicSize(context *layoutContext, entry StackLayoutEntry, measure func() int) int {
	if entry.Basis != nil {
		return *entry.Basis
	}
	return measure()
}

func layoutComponent(context *layoutContext, component Component, x, y, width int, height *int, clip LayoutRect) *LayoutBox {
	safeWidth := max(1, width)
	node := getLayoutNode(component)
	if node == nil {
		return layoutLeaf(context, component, x, y, safeWidth, height, clip)
	}
	if scroll, ok := node.(ScrollLayoutNode); ok {
		return layoutScroll(context, component, scroll, x, y, safeWidth, height, clip)
	}
	stack := node.(StackLayoutNode)
	if stack.Type == "vstack" {
		return layoutVStack(context, component, stack, x, y, safeWidth, height, clip)
	}
	return layoutHStack(context, component, stack, x, y, safeWidth, height, clip)
}

func layoutLeaf(context *layoutContext, component Component, x, y, safeWidth int, height *int, clip LayoutRect) *LayoutBox {
	lines := renderCached(context, component, safeWidth)
	allocatedHeight := len(lines)
	if height != nil {
		allocatedHeight = max(0, *height)
	}
	lineOffset := 0
	if len(lines) > allocatedHeight && allocatedHeight > 0 {
		cursorLine := -1
		for i, line := range lines {
			if strings.Contains(line, widthx.CursorMarker) {
				cursorLine = i
				break
			}
		}
		if cursorLine >= allocatedHeight {
			lineOffset = cursorLine - allocatedHeight + 1
		}
	}
	rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}
	return &LayoutBox{
		Component:  component,
		Rect:       rect,
		Clip:       intersect(clip, rect),
		Children:   nil,
		Lines:      lines,
		LineOffset: lineOffset,
	}
}

func layoutScroll(context *layoutContext, component Component, node ScrollLayoutNode, x, y, safeWidth int, height *int, clip LayoutRect) *LayoutBox {
	previousScrollTop := node.State.ScrollTop()
	contentWidth := node.State.GetContentWidth(safeWidth)
	childBox := layoutComponent(context, node.Component, x, y-previousScrollTop, contentWidth, nil, clip)
	contentHeight := childBox.Rect.Height
	viewportHeight := contentHeight
	if height != nil {
		viewportHeight = max(0, *height)
	}
	node.State.UpdateLayout(contentHeight, viewportHeight, context.requestRender)
	translateBox(childBox, previousScrollTop-node.State.ScrollTop())
	scrollView := node.State.(*ScrollView)
	if node.State.Primary() || context.primaryScrollView == nil {
		context.primaryScrollView = scrollView
	}
	rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: viewportHeight}
	childClip := intersect(clip, rect)
	box := &LayoutBox{
		Component:          component,
		Rect:               rect,
		Clip:               childClip,
		Children:           []*LayoutBox{childBox},
		ScrollView:         scrollView,
		ScrollContentLines: renderCached(context, node.Component, contentWidth),
	}
	childBox.Parent = box
	updateClips(childBox, childClip)
	return box
}

func layoutVStack(context *layoutContext, component Component, node StackLayoutNode, x, y, safeWidth int, height *int, clip LayoutRect) *LayoutBox {
	entries := visibleStackEntries(node.Entries, context.viewport)
	gapTotal := max(0, len(entries)-1) * node.Gap
	intrinsicHeights := make([]int, len(entries))
	for i, entry := range entries {
		intrinsicHeights[i] = intrinsicSize(context, entry, func() int { return measureHeight(context, entry.Component, safeWidth) })
	}
	sizes := allocateStackSizes(entries, intrinsicHeights, height, node.Gap)
	naturalHeight := gapTotal
	for _, size := range sizes {
		naturalHeight += size
	}
	allocatedHeight := naturalHeight
	if height != nil {
		allocatedHeight = max(0, *height)
	}
	rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}
	box := &LayoutBox{Component: component, Rect: rect, Clip: intersect(clip, rect)}
	childY := y
	for i, entry := range entries {
		size := sizes[i]
		box.Children = append(box.Children, withParent(
			layoutComponent(context, entry.Component, x, childY, safeWidth, &size, box.Clip), box))
		childY += sizes[i] + node.Gap
	}
	return box
}

func layoutHStack(context *layoutContext, component Component, node StackLayoutNode, x, y, safeWidth int, height *int, clip LayoutRect) *LayoutBox {
	entries := visibleStackEntries(node.Entries, context.viewport)
	intrinsicWidths := make([]int, len(entries))
	for i, entry := range entries {
		intrinsicWidths[i] = intrinsicSize(context, entry, func() int { return measureWidth(context, entry.Component, safeWidth) })
	}
	widths := allocateStackSizes(entries, intrinsicWidths, &safeWidth, node.Gap)
	intrinsicHeights := make([]int, len(entries))
	for i, entry := range entries {
		intrinsicHeights[i] = measureHeight(context, entry.Component, max(1, widths[i]))
	}
	allocatedHeight := 0
	if height == nil {
		for _, h := range intrinsicHeights {
			allocatedHeight = max(allocatedHeight, h)
		}
	} else {
		allocatedHeight = max(0, *height)
	}
	rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}
	box := &LayoutBox{Component: component, Rect: rect, Clip: intersect(clip, rect)}
	childX := x
	for i, entry := range entries {
		naturalChildHeight := intrinsicHeights[i]
		childHeight := min(allocatedHeight, naturalChildHeight)
		if node.Align == "stretch" {
			childHeight = allocatedHeight
		}
		childY := y
		switch node.Align {
		case "center":
			childY += (allocatedHeight - childHeight) / 2
		case "end":
			childY += allocatedHeight - childHeight
		}
		childWidth := widths[i]
		if childWidth == 0 {
			box.Children = append(box.Children, &LayoutBox{
				Component: entry.Component,
				Rect:      LayoutRect{X: childX, Y: childY, Width: 0, Height: childHeight},
				Clip:      LayoutRect{X: childX, Y: childY, Width: 0, Height: 0},
				Parent:    box,
			})
		} else {
			box.Children = append(box.Children, withParent(
				layoutComponent(context, entry.Component, childX, childY, childWidth, &childHeight, box.Clip), box))
		}
		childX += childWidth + node.Gap
	}
	return box
}

// replaceScrollbarCell replaces the grapheme covering column with a scrollbar
// glyph. It resets styling (and any open OSC 8 link) before the glyph and, when
// preserveTargetBackground is set, re-applies the background active at the
// replaced cell. Mirrors upstream replaceScrollbarCell.
func replaceScrollbarCell(line string, column, totalWidth int, replacement string, preserveTargetBackground bool) string {
	if IsImageLine(line) {
		return line
	}
	start, end, ok := widthx.GraphemeCellRange(line, column)
	if !ok {
		start = column
		end = column + 1
	}
	before := widthx.SliceByColumn(line, 0, start, true)
	target := widthx.SliceByColumn(line, start, end-start, true)
	after := widthx.SliceByColumn(line, end, max(0, totalWidth-end), true)

	var targetPrefix strings.Builder
	targetIndex := 0
	for targetIndex < len(target) {
		code, n := widthx.ExtractAnsiCode(target, targetIndex)
		if n == 0 {
			break
		}
		targetPrefix.WriteString(code)
		targetIndex += n
	}
	beforePadding := strings.Repeat(" ", max(0, start-widthx.VisibleWidth(before)))
	cellPaddingBefore := strings.Repeat(" ", max(0, column-start))
	cellPaddingAfter := strings.Repeat(" ", max(0, end-column-1))
	targetStyle := "\x1b[0m\x1b]8;;\x07"
	if preserveTargetBackground {
		targetStyle += widthx.GetActiveBackgroundAnsi(targetPrefix.String())
	}
	return before + beforePadding + targetStyle + cellPaddingBefore + replacement + cellPaddingAfter + after
}

// GetScrollbarGeometry computes where a scroll box's scrollbar paints, or nil if
// it should not paint. Mirrors upstream getScrollbarGeometry(box).
func GetScrollbarGeometry(box *LayoutBox) *ScrollbarGeometry {
	return getScrollbarGeometry(box, false)
}

// getScrollbarGeometry mirrors upstream getScrollbarGeometry(box,
// includeHiddenAuto): with includeHiddenAuto an "auto" scrollbar whose content
// overflows reports its geometry while hidden, so pointer hover can reveal it.
func getScrollbarGeometry(box *LayoutBox, includeHiddenAuto bool) *ScrollbarGeometry {
	if box.ScrollView == nil || box.Rect.Width <= 0 || box.Rect.Height <= 0 {
		return nil
	}
	contentHeight := 0
	if len(box.Children) > 0 {
		contentHeight = box.Children[0].Rect.Height
	} else if box.ScrollContentLines != nil {
		contentHeight = len(box.ScrollContentLines)
	}
	trackHeight := box.Rect.Height
	canRevealHiddenAuto := includeHiddenAuto && box.ScrollView.Scrollbar() == "auto" && contentHeight > trackHeight
	if !box.ScrollView.IsScrollbarVisible() && !canRevealHiddenAuto {
		return nil
	}
	minThumbHeight := min(2, trackHeight)
	thumbCandidate := trackHeight
	if contentHeight > 0 {
		thumbCandidate = int(math.Round(float64(trackHeight*trackHeight) / float64(contentHeight)))
	}
	thumbHeight := max(minThumbHeight, min(trackHeight, thumbCandidate))
	maxScrollTop := max(0, contentHeight-trackHeight)
	maxThumbTop := trackHeight - thumbHeight
	thumbOffset := 0
	if maxScrollTop != 0 {
		thumbOffset = int(math.Round(float64(box.ScrollView.ScrollTop()) / float64(maxScrollTop) * float64(maxThumbTop)))
	}
	column := box.Rect.X + box.Rect.Width - 1
	if column < box.Clip.X || column >= box.Clip.X+box.Clip.Width {
		return nil
	}
	return &ScrollbarGeometry{
		Column:       column,
		TrackTop:     box.Rect.Y,
		TrackHeight:  trackHeight,
		ThumbTop:     box.Rect.Y + thumbOffset,
		ThumbHeight:  thumbHeight,
		MaxScrollTop: maxScrollTop,
	}
}

func paintScrollbar(box *LayoutBox, screen []string, totalWidth int) {
	geometry := GetScrollbarGeometry(box)
	if geometry == nil || box.ScrollView == nil {
		return
	}
	scrollView := box.ScrollView
	thumbGlyph := "┃"
	if scrollView.IsScrollbarActive() {
		thumbGlyph = "█"
	}
	preserveTargetBackground := scrollView.Scrollbar() != "always"
	for offset := 0; offset < geometry.TrackHeight; offset++ {
		row := geometry.TrackTop + offset
		if row < box.Clip.Y || row >= box.Clip.Y+box.Clip.Height || row < 0 || row >= len(screen) {
			continue
		}
		replacement := scrollView.ScrollbarTrackStyle()("│")
		if row >= geometry.ThumbTop && row < geometry.ThumbTop+geometry.ThumbHeight {
			replacement = scrollView.ScrollbarThumbStyle()(thumbGlyph)
		}
		screen[row] = replaceScrollbarCell(screen[row], geometry.Column, totalWidth, replacement, preserveTargetBackground)
	}
}

func paintBox(box *LayoutBox, screen []string, totalWidth int) {
	if box.Lines != nil {
		offset := box.LineOffset
		firstRow := max(box.Rect.Y, box.Clip.Y, 0)
		lastRow := min(box.Rect.Y+box.Rect.Height, box.Clip.Y+box.Clip.Height, len(screen))
		for row := firstRow; row < lastRow; row++ {
			idx := offset + row - box.Rect.Y
			if idx < 0 || idx >= len(box.Lines) {
				continue
			}
			line := stripOsc133ZonePrefix(box.Lines[idx])
			if imageMetadata := GetKittyImageMetadata(line); imageMetadata != nil {
				clipBottom := min(len(screen), box.Clip.Y+box.Clip.Height)
				visibleRows := min(imageMetadata.Rows, clipBottom-row)
				if visibleRows < imageMetadata.Rows {
					line = CropKittyImageLine(line, 0, visibleRows)
				}
			}
			// Fast path: a full-width box painting onto an untouched row can use
			// the source line directly. Compositing would rebuild the row through
			// ANSI/grapheme segmentation every frame; padding is unnecessary
			// because rows are written with erase-line and the final width clamp
			// still truncates over-wide lines. Mirrors upstream paintBox.
			if box.Rect.X == 0 && box.Rect.Width >= totalWidth && (IsImageLine(line) || screen[row] == "") {
				screen[row] = line
			} else {
				screen[row] = compositeTuiLine(screen[row], line, box.Rect.X, box.Rect.Width, totalWidth)
			}
		}
	}
	for _, child := range box.Children {
		paintBox(child, screen, totalWidth)
	}

	if box.ScrollView != nil && box.ScrollContentLines != nil && box.Rect.Height > 0 {
		scrollTop := box.ScrollView.ScrollTop()
		if scrollTop > 0 {
			for imageRow := scrollTop - 1; imageRow >= 0; imageRow-- {
				imageLine := ""
				if imageRow < len(box.ScrollContentLines) {
					imageLine = box.ScrollContentLines[imageRow]
				}
				if metadata := GetKittyImageMetadata(imageLine); metadata != nil {
					hiddenRows := scrollTop - imageRow
					if hiddenRows < metadata.Rows {
						visibleRows := min(box.Rect.Height, metadata.Rows-hiddenRows)
						cropped := CropKittyImageLine(imageLine, hiddenRows, visibleRows)
						if box.Rect.X == 0 && box.Rect.Width >= totalWidth {
							screen[box.Rect.Y] = cropped
						}
					}
					break
				}
				if imageLine != "" {
					break
				}
			}
		}
	}

	paintScrollbar(box, screen, totalWidth)
}

// RenderLayoutFrame lays out a component tree into a width×height viewport and
// paints it. Mirrors upstream renderLayoutFrame.
func RenderLayoutFrame(root Component, width, height int, requestRender func()) LayoutFrame {
	safeWidth := max(1, width)
	safeHeight := max(1, height)
	context := &layoutContext{
		viewport:      LayoutViewport{Width: safeWidth, Height: safeHeight},
		renderCache:   map[Component]map[int][]string{},
		requestRender: requestRender,
	}
	rootBox := layoutComponent(context, root, 0, 0, safeWidth, &safeHeight,
		LayoutRect{X: 0, Y: 0, Width: safeWidth, Height: safeHeight})
	lines := make([]string, safeHeight)
	paintBox(rootBox, lines, safeWidth)
	return LayoutFrame{
		Root:              rootBox,
		Width:             safeWidth,
		Height:            safeHeight,
		Lines:             lines,
		PrimaryScrollView: context.primaryScrollView,
	}
}

func containsPoint(rect LayoutRect, x, y int) bool {
	return x >= rect.X && x < rect.X+rect.Width && y >= rect.Y && y < rect.Y+rect.Height
}

// GetLayoutBoxesAt returns the visual hit path at a point, from the deepest
// component to the layout root, highest layer first. Mirrors upstream
// getLayoutBoxesAt.
func GetLayoutBoxesAt(frame LayoutFrame, x, y int) []*LayoutBox {
	type hit struct {
		box   *LayoutBox
		depth int
	}
	var result []hit
	var visit func(box *LayoutBox, depth int)
	visit = func(box *LayoutBox, depth int) {
		if !containsPoint(box.Clip, x, y) {
			return
		}
		result = append(result, hit{box, depth})
		for _, child := range box.Children {
			visit(child, depth+1)
		}
	}
	visit(frame.Root, 0)
	slices.SortStableFunc(result, func(a, b hit) int {
		if a.box.Layer != b.box.Layer {
			return b.box.Layer - a.box.Layer
		}
		return b.depth - a.depth
	})
	boxes := make([]*LayoutBox, len(result))
	for i, h := range result {
		boxes[i] = h.box
	}
	return boxes
}

// GetScrollViewBox finds the layout box wrapping a given ScrollView, or nil.
// Mirrors upstream getScrollViewBox.
func GetScrollViewBox(frame LayoutFrame, scrollView *ScrollView) *LayoutBox {
	var visit func(box *LayoutBox) *LayoutBox
	visit = func(box *LayoutBox) *LayoutBox {
		if box.ScrollView == scrollView {
			return box
		}
		for _, child := range box.Children {
			if match := visit(child); match != nil {
				return match
			}
		}
		return nil
	}
	return visit(frame.Root)
}

// GetScrollViewsAt returns the scroll views under a point, deepest first.
// Mirrors upstream getScrollViewsAt.
func GetScrollViewsAt(frame LayoutFrame, x, y int) []*ScrollView {
	type hit struct {
		scrollView *ScrollView
		depth      int
	}
	var result []hit
	var visit func(box *LayoutBox, depth int)
	visit = func(box *LayoutBox, depth int) {
		if !containsPoint(box.Clip, x, y) {
			return
		}
		if box.ScrollView != nil && containsPoint(box.Rect, x, y) {
			result = append(result, hit{box.ScrollView, depth})
		}
		for _, child := range box.Children {
			visit(child, depth+1)
		}
	}
	visit(frame.Root, 0)
	// Deepest first; stable so ties keep insertion order, matching upstream's
	// stable sort with comparator b.depth - a.depth.
	slices.SortStableFunc(result, func(a, b hit) int { return b.depth - a.depth })
	views := make([]*ScrollView, len(result))
	for i, h := range result {
		views[i] = h.scrollView
	}
	return views
}
