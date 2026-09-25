package tui

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports pi-tui's stack.ts, h-stack.ts, and v-stack.ts: flexbox-style horizontal
// and vertical stacks plus the pure size allocator the layout engine consumes.
// Names and structure mirror upstream 1:1. Forced Go mechanics: the
// StackChild = Component | StackEntry input union collapses to one struct
// (a bare component is an entry with zero-value options, behaviorally identical),
// and the "grow" | "shrink" mode literal stays a string parameter.

// maxSafeInteger mirrors JS Number.MAX_SAFE_INTEGER (2^53-1), used as the
// default maxSize and the intrinsic viewport height.
const maxSafeInteger = 1<<53 - 1

// StackEntryOptions are the per-child flexbox options. A nil pointer field is
// upstream's "undefined": the allocator applies the default at read time
// (Basis nil == "auto" -> intrinsic, Grow 0, Shrink 1, MinSize 0,
// MaxSize maxSafeInteger).
type StackEntryOptions struct {
	Basis   *int // nil == "auto"/undefined -> intrinsic size
	Grow    *int
	Shrink  *int
	MinSize *int
	MaxSize *int
	Visible func(viewport LayoutViewport) bool
}

// StackChild is one child passed to a stack constructor. A bare component is
// StackChild{Component: c} with zero-value options (upstream's Component arm of
// the Component | StackEntry union); a configured child sets the options too.
type StackChild struct {
	Component Component
	StackEntryOptions
}

// StackOptions configure a stack. Gap nil defaults to 0; Align "" defaults to
// "stretch".
type StackOptions struct {
	Gap   *int
	Align string // "stretch" | "start" | "center" | "end"
}

// normalizeSize mirrors upstream normalizeSize: undefined (nil) falls back,
// otherwise max(0, floor(value)). Go ints are always finite, so the
// !Number.isFinite branch is unreachable.
func normalizeSize(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return max(0, *value)
}

func derefOr(p *int, d int) int {
	if p == nil {
		return d
	}
	return *p
}

// Stack is the abstract base shared by HStack and VStack. It embeds pig's
// Container and maintains the parallel entries slice with flexbox metadata.
type Stack struct {
	*Container
	entries    []StackLayoutEntry
	gap        int
	align      string
	layoutType string // "vstack" | "hstack"
}

func newStack(layoutType string, children []StackChild, options StackOptions) *Stack {
	align := options.Align
	if align == "" {
		align = "stretch"
	}
	s := &Stack{
		Container:  NewContainer(),
		gap:        normalizeSize(options.Gap, 0),
		align:      align,
		layoutType: layoutType,
	}
	for _, child := range children {
		s.AddChild(child.Component, child.StackEntryOptions)
	}
	return s
}

// AddChild mirrors upstream Stack.addChild: it adds the component to the
// container and records its normalized flexbox entry. A provided option is
// normalized and stored; an omitted (nil) option is left unset so the allocator
// applies its default.
func (s *Stack) AddChild(component Component, options StackEntryOptions) {
	s.Container.Add(component)
	entry := StackLayoutEntry{Component: component, Visible: options.Visible}
	if options.Basis != nil { // basis is stored raw (no normalize), matching upstream
		entry.Basis = options.Basis
	}
	if options.Grow != nil {
		g := normalizeSize(options.Grow, 0)
		entry.Grow = &g
	}
	if options.Shrink != nil {
		sh := normalizeSize(options.Shrink, 1)
		entry.Shrink = &sh
	}
	if options.MinSize != nil {
		mn := normalizeSize(options.MinSize, 0)
		entry.MinSize = &mn
	}
	if options.MaxSize != nil {
		mx := normalizeSize(options.MaxSize, maxSafeInteger)
		entry.MaxSize = &mx
	}
	s.entries = append(s.entries, entry)
}

// RemoveChild mirrors upstream Stack.removeChild: remove the component and its
// first matching entry.
func (s *Stack) RemoveChild(component Component) {
	s.Container.Remove(component)
	for i := range s.entries {
		if s.entries[i].Component == component {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return
		}
	}
}

// Add shadows the promoted Container.Add so an options-free add still records an
// entry (equivalent to addChild with no options), keeping entries consistent.
func (s *Stack) Add(component Component) { s.AddChild(component, StackEntryOptions{}) }

// Remove shadows the promoted Container.Remove to keep entries consistent.
func (s *Stack) Remove(component Component) { s.RemoveChild(component) }

// Clear mirrors upstream Stack.clear: clear the container and the entries.
func (s *Stack) Clear() {
	s.Container.Clear()
	s.entries = nil
}

// LayoutNode mirrors upstream Stack[LAYOUT_NODE](): the stack's layout node.
func (s *Stack) LayoutNode() LayoutNode {
	return StackLayoutNode{Type: s.layoutType, Entries: s.entries, Gap: s.gap, Align: s.align}
}

// visibleStackEntries ports upstream visibleStackEntries: entries whose
// visibility predicate returns true (or is unset).
func visibleStackEntries(entries []StackLayoutEntry, viewport LayoutViewport) []StackLayoutEntry {
	out := make([]StackLayoutEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Visible == nil || entry.Visible(viewport) {
			out = append(out, entry)
		}
	}
	return out
}

// clampSize ports upstream clampSize: clamp a size into the entry's
// [minSize, maxSize] window (min wins when min > max).
func clampSize(size int, entry StackLayoutEntry) int {
	minv := max(0, derefOr(entry.MinSize, 0))
	maxv := max(minv, derefOr(entry.MaxSize, maxSafeInteger))
	return max(minv, min(maxv, max(0, size)))
}

// distribute ports upstream distribute: hand out (grow) or reclaim (shrink)
// `amount` across the entries, weighted by grow, or by shrink*max(1,size),
// until the amount is exhausted or no candidate can absorb more.
func distribute(sizes []int, entries []StackLayoutEntry, amount int, mode string) {
	weightOf := func(i int) int {
		if mode == "grow" {
			return derefOr(entries[i].Grow, 0)
		}
		return derefOr(entries[i].Shrink, 1) * max(1, sizes[i])
	}
	isCandidate := func(i int) bool {
		if mode == "grow" {
			return derefOr(entries[i].Grow, 0) > 0 && sizes[i] < derefOr(entries[i].MaxSize, maxSafeInteger)
		}
		return derefOr(entries[i].Shrink, 1) > 0 && sizes[i] > derefOr(entries[i].MinSize, 0)
	}

	remaining := amount
	for remaining > 0 {
		candidates := make([]int, 0, len(entries))
		for i := range entries {
			if isCandidate(i) {
				candidates = append(candidates, i)
			}
		}
		if len(candidates) == 0 {
			return
		}
		totalWeight := 0
		for _, i := range candidates {
			totalWeight += weightOf(i)
		}
		distributed := 0
		for _, i := range candidates {
			if remaining <= 0 {
				break
			}
			proposed := max(1, (remaining*weightOf(i))/totalWeight)
			var capacity int
			if mode == "grow" {
				capacity = derefOr(entries[i].MaxSize, maxSafeInteger) - sizes[i]
			} else {
				capacity = sizes[i] - derefOr(entries[i].MinSize, 0)
			}
			delta := min(remaining, min(proposed, capacity))
			if delta <= 0 {
				continue
			}
			if mode == "grow" {
				sizes[i] += delta
			} else {
				sizes[i] -= delta
			}
			remaining -= delta
			distributed += delta
		}
		if distributed == 0 {
			return
		}
	}
}

// allocateStackSizes ports upstream allocateStackSizes: seed each entry from its
// basis (or intrinsic size when auto), then grow or shrink toward the available
// content size (available minus inter-entry gaps). When availableSize is nil the
// intrinsic-clamped sizes are returned unchanged.
func allocateStackSizes(entries []StackLayoutEntry, intrinsicSizes []int, availableSize *int, gap int) []int {
	sizes := make([]int, len(entries))
	for i := range entries {
		basis := 0
		if entries[i].Basis == nil {
			if i < len(intrinsicSizes) {
				basis = intrinsicSizes[i]
			}
		} else {
			basis = *entries[i].Basis
		}
		sizes[i] = clampSize(basis, entries[i])
	}
	if availableSize == nil {
		return sizes
	}
	contentSize := max(0, *availableSize-max(0, len(entries)-1)*gap)
	total := 0
	for _, s := range sizes {
		total += s
	}
	if total < contentSize {
		distribute(sizes, entries, contentSize-total, "grow")
	} else if total > contentSize {
		distribute(sizes, entries, total-contentSize, "shrink")
	}
	return sizes
}

// compositeTuiLine ports upstream compositeTuiLine (tui.ts): overlay a child's
// line onto a base line at startCol, padding to keep column alignment and
// clipping to totalWidth. Image base lines pass through untouched.
func compositeTuiLine(baseLine, overlayLine string, startCol, overlayWidth, totalWidth int) string {
	if IsImageLine(baseLine) {
		return baseLine
	}
	afterStart := startCol + overlayWidth
	base := widthx.ExtractSegments(baseLine, startCol, afterStart, totalWidth-afterStart, true)
	overlay := widthx.SliceWithWidth(overlayLine, 0, overlayWidth, true)
	beforePad := max(0, startCol-base.BeforeWidth)
	overlayPad := max(0, overlayWidth-overlay.Width)
	actualBeforeWidth := max(startCol, base.BeforeWidth)
	actualOverlayWidth := max(overlayWidth, overlay.Width)
	afterTarget := max(0, totalWidth-actualBeforeWidth-actualOverlayWidth)
	afterPad := max(0, afterTarget-base.AfterWidth)
	result := base.Before + strings.Repeat(" ", beforePad) + widthx.SegmentReset +
		overlay.Text + strings.Repeat(" ", overlayPad) + widthx.SegmentReset +
		base.After + strings.Repeat(" ", afterPad)
	if widthx.VisibleWidth(result) <= totalWidth {
		return result
	}
	return widthx.SliceByColumn(result, 0, totalWidth, true)
}

// HStack ports pi-tui's HStack: children laid out left to right with flexbox
// widths and vertical alignment.
type HStack struct {
	*Stack
}

// NewHStack constructs a horizontal stack.
func NewHStack(children []StackChild, options StackOptions) *HStack {
	return &HStack{Stack: newStack("hstack", children, options)}
}

// Render ports upstream HStack.render.
func (h *HStack) Render(width int) []string {
	safeWidth := max(1, width)
	viewport := LayoutViewport{Width: safeWidth, Height: maxSafeInteger}
	entries := visibleStackEntries(h.entries, viewport)
	if len(entries) == 0 {
		return []string{}
	}
	intrinsicWidths := make([]int, len(entries))
	for i, entry := range entries {
		mx := 0
		for _, line := range entry.Component.Render(safeWidth) {
			mx = max(mx, widthx.VisibleWidth(line))
		}
		intrinsicWidths[i] = mx
	}
	widths := allocateStackSizes(entries, intrinsicWidths, &safeWidth, h.gap)
	rendered := make([][]string, len(entries))
	for i, entry := range entries {
		if widths[i] == 0 {
			rendered[i] = []string{}
		} else {
			rendered[i] = entry.Component.Render(widths[i])
		}
	}
	height := 0
	for _, lines := range rendered {
		height = max(height, len(lines))
	}
	result := make([]string, height)
	x := 0
	for i := range rendered {
		lines := rendered[i]
		childWidth := widths[i]
		offset := 0
		switch h.align {
		case "center":
			offset = (height - len(lines)) / 2
		case "end":
			offset = height - len(lines)
		}
		for row := range lines {
			target := row + offset
			if target < 0 || target >= len(result) {
				continue
			}
			result[target] = compositeTuiLine(result[target], lines[row], x, childWidth, safeWidth)
		}
		x += childWidth + h.gap
	}
	return result
}

// VStack ports pi-tui's VStack: children laid out top to bottom with flexbox
// heights.
type VStack struct {
	*Stack
}

// NewVStack constructs a vertical stack.
func NewVStack(children []StackChild, options StackOptions) *VStack {
	return &VStack{Stack: newStack("vstack", children, options)}
}

// Render ports upstream VStack.render.
func (v *VStack) Render(width int) []string {
	viewport := LayoutViewport{Width: max(1, width), Height: maxSafeInteger}
	entries := visibleStackEntries(v.entries, viewport)
	rendered := make([][]string, len(entries))
	heights := make([]int, len(entries))
	for i, entry := range entries {
		rendered[i] = entry.Component.Render(viewport.Width)
		heights[i] = len(rendered[i])
	}
	sizes := allocateStackSizes(entries, heights, nil, v.gap)
	lines := []string{}
	for i := range entries {
		if i > 0 {
			for g := 0; g < v.gap; g++ {
				lines = append(lines, "")
			}
		}
		childLines := rendered[i]
		if sizes[i] < len(childLines) {
			childLines = childLines[:sizes[i]]
		}
		lines = append(lines, childLines...)
		for padding := len(childLines); padding < sizes[i]; padding++ {
			lines = append(lines, "")
		}
	}
	return lines
}

// Compile-time proof HStack and VStack are LayoutComponents.
var (
	_ LayoutComponent = (*HStack)(nil)
	_ LayoutComponent = (*VStack)(nil)
)
