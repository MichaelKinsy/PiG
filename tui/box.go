package tui

// box.go: padding/background container.
//
// Ports upstream packages/tui/src/components/box.ts.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// boxRenderCache holds the unpadded child lines and the padding they were rendered with. Upstream box.ts padding is fixed at construction; Box's exported padding fields can change between frames, so the padding is part of the cache key. The background is keyed by its sample and by whether there is one, as box.ts keys undefined and an empty string apart.
type boxRenderCache struct {
	childLines         []string
	width              int
	paddingX, paddingY int
	hasBg              bool
	bgSample           string
	lines              []string
}

// Box component - a container that applies padding and background to children.
type Box struct {
	invalidatable
	children []Component
	PaddingX int
	PaddingY int
	BgFn     func(string) string
	cache    *boxRenderCache
	// last is the last render, which Clear, AddChild and RemoveChild keep while they drop cache; Invalidate drops it. Its key (width, padding,
	// background sample) is checked when it serves rows.
	last *boxRenderCache
	// mouseLayout records child heights from the last render for mouse
	// dispatch. Mirrors upstream Box.mouseLayout.
	mouseLayout *mouseLayout
}

func NewBox() *Box { return &Box{PaddingX: 1, PaddingY: 1} }

func NewPaddedBox(paddingX, paddingY int, bgFn func(string) string) *Box {
	return &Box{PaddingX: paddingX, PaddingY: paddingY, BgFn: bgFn}
}

// AddChild, RemoveChild and Clear drop the render cache and mark the box dirty; they do not invalidate the children (box.ts addChild, removeChild,
// clear call only invalidateCache).
func (b *Box) AddChild(component Component) {
	b.children = append(b.children, component)
	b.cache = nil
	b.invalidatable.Invalidate()
}

func (b *Box) RemoveChild(component Component) {
	for i, child := range b.children {
		if child == component {
			b.children = append(b.children[:i], b.children[i+1:]...)
			b.cache = nil
			b.invalidatable.Invalidate()
			return
		}
	}
}

func (b *Box) Clear() {
	b.children = nil
	b.cache = nil
	b.invalidatable.Invalidate()
}

// SetBgFn sets the background. box.ts setBgFn invalidates nothing, because Pi's Container renders every child on every frame and the render cache is keyed by the
// background's output; a type that embeds the Box is a child a Go container may serve from its own cache, so the Box's dirty flag is set.
func (b *Box) SetBgFn(bgFn func(string) string) {
	b.BgFn = bgFn
	b.invalidatable.Invalidate()
}

// SetPaddingX sets the horizontal padding and drops the render cache (box.ts setPaddingX); it sets the Box's dirty flag for the reason SetBgFn does.
func (b *Box) SetPaddingX(paddingX int) {
	b.PaddingX = paddingX
	b.cache = nil
	b.invalidatable.Invalidate()
}

func (b *Box) Invalidate() {
	b.invalidatable.Invalidate()
	b.cache, b.last = nil, nil
	for _, child := range b.children {
		child.Invalidate()
	}
}

func (b *Box) matchCache(width int, childLines []string, hasBg bool, bgSample string) bool {
	cache := b.cache
	if cache == nil || cache.width != width || cache.paddingX != b.PaddingX || cache.paddingY != b.PaddingY || cache.hasBg != hasBg || cache.bgSample != bgSample || len(cache.childLines) != len(childLines) {
		return false
	}
	for i := range childLines {
		if cache.childLines[i] != childLines[i] {
			return false
		}
	}
	return true
}

func (b *Box) Render(width int) []string {
	if len(b.children) == 0 {
		return []string{}
	}
	contentWidth := max(1, width-b.PaddingX*2)
	leftPad := strings.Repeat(" ", b.PaddingX)
	// Keep the child lines unpadded: children usually return the same string values every frame, so the cache check below compares identical strings. Padding here would create new strings that must be compared byte by byte.
	childLines := make([]string, 0, 8)
	children := make([]mouseChild, len(b.children))
	for i, child := range b.children {
		lines := child.Render(contentWidth)
		children[i] = mouseChild{component: child, height: len(lines)}
		childLines = append(childLines, lines...)
	}
	b.mouseLayout = &mouseLayout{width: contentWidth, children: children}
	if len(childLines) == 0 {
		return []string{}
	}
	bgSample := ""
	if b.BgFn != nil {
		bgSample = b.BgFn("test")
	}
	if b.matchCache(width, childLines, b.BgFn != nil, bgSample) {
		return b.cache.lines
	}
	// A row whose child line is the one the last render drew keeps the last render's row: the cache above is dropped by every
	// change of the children, but a card that draws again after each streamed update changes one line of the many it holds.
	prev := b.last
	if prev != nil && (b.PaddingY < 0 || prev.width != width || prev.paddingX != b.PaddingX || prev.paddingY != b.PaddingY || prev.hasBg != (b.BgFn != nil) || prev.bgSample != bgSample) {
		prev = nil
	}
	result := make([]string, 0, capHint(len(childLines), 2*b.PaddingY))
	for i := range b.PaddingY {
		if prev != nil {
			result = append(result, prev.lines[i])
		} else {
			result = append(result, b.applyBg("", width))
		}
	}
	for i, line := range childLines {
		if prev != nil && i < len(prev.childLines) && prev.childLines[i] == line {
			result = append(result, prev.lines[prev.paddingY+i])
		} else {
			result = append(result, b.applyBg(leftPad+line, width))
		}
	}
	for i := range b.PaddingY {
		if prev != nil {
			result = append(result, prev.lines[prev.paddingY+len(prev.childLines)+i])
		} else {
			result = append(result, b.applyBg("", width))
		}
	}
	b.cache = &boxRenderCache{childLines: childLines, width: width, paddingX: b.PaddingX, paddingY: b.PaddingY, hasBg: b.BgFn != nil, bgSample: bgSample, lines: result}
	b.last = b.cache
	return result
}

func (b *Box) applyBg(line string, width int) string {
	padded := line + strings.Repeat(" ", max(0, width-widthx.VisibleWidth(line)))
	if b.BgFn != nil {
		return b.BgFn(padded)
	}
	return padded
}
