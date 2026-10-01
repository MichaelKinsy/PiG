package tui

// box.go: padding/background container.
//
// Ports upstream packages/tui/src/components/box.ts.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// boxRenderCache holds the unpadded child lines and the padding they were rendered with. Upstream box.ts padding is fixed at construction; Box's exported padding fields can change between frames, so the padding is part of the cache key.
type boxRenderCache struct {
	childLines         []string
	width              int
	paddingX, paddingY int
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
	// mouseLayout records child heights from the last render for mouse
	// dispatch. Mirrors upstream Box.mouseLayout.
	mouseLayout *mouseLayout
}

func NewBox() *Box { return &Box{PaddingX: 1, PaddingY: 1} }

func NewPaddedBox(paddingX, paddingY int, bgFn func(string) string) *Box {
	return &Box{PaddingX: paddingX, PaddingY: paddingY, BgFn: bgFn}
}

func (b *Box) AddChild(component Component) {
	b.children = append(b.children, component)
	b.cache = nil
	b.Invalidate()
}

func (b *Box) RemoveChild(component Component) {
	for i, child := range b.children {
		if child == component {
			b.children = append(b.children[:i], b.children[i+1:]...)
			b.cache = nil
			b.Invalidate()
			return
		}
	}
}

func (b *Box) Clear() {
	b.children = nil
	b.cache = nil
	b.Invalidate()
}

func (b *Box) SetBgFn(bgFn func(string) string) {
	b.BgFn = bgFn
}

func (b *Box) Invalidate() {
	b.invalidatable.Invalidate()
	b.cache = nil
	for _, child := range b.children {
		child.Invalidate()
	}
}

func (b *Box) matchCache(width int, childLines []string, bgSample string) bool {
	cache := b.cache
	if cache == nil || cache.width != width || cache.paddingX != b.PaddingX || cache.paddingY != b.PaddingY || cache.bgSample != bgSample || len(cache.childLines) != len(childLines) {
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
	if b.matchCache(width, childLines, bgSample) {
		return b.cache.lines
	}
	result := make([]string, 0, capHint(len(childLines), 2*b.PaddingY))
	for range b.PaddingY {
		result = append(result, b.applyBg("", width))
	}
	for _, line := range childLines {
		result = append(result, b.applyBg(leftPad+line, width))
	}
	for range b.PaddingY {
		result = append(result, b.applyBg("", width))
	}
	b.cache = &boxRenderCache{childLines: childLines, width: width, paddingX: b.PaddingX, paddingY: b.PaddingY, bgSample: bgSample, lines: result}
	return result
}

func (b *Box) applyBg(line string, width int) string {
	padded := line + strings.Repeat(" ", max(0, width-widthx.VisibleWidth(line)))
	if b.BgFn != nil {
		return b.BgFn(padded)
	}
	return padded
}
