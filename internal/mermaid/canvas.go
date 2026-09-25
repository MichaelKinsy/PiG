// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import "strings"

// Grid canvas, ported from grok-mermaid canvas.ts. Edges accumulate as direction
// bits (resolved to box-drawing glyphs by finalizeMask) so crossings and
// junctions resolve correctly whatever order they are drawn in.

// cont occupies the trailing column of a wide glyph; never emitted (toLines
// skips it), so a CJK character claims two layout cells but one output char.
const cont = "\x00"

// Connection direction bits, combined into a glyph by maskChar.
const (
	bitU = 1
	bitD = 2
	bitL = 4
	bitR = 8
)

// Line styles, tracked per cell so crossing edges keep their own stroke.
const (
	styDot   = 1
	styThick = 2
	stySolid = 4
)

type canvas struct {
	w, h     int
	ch       []string
	cls      []Cls
	mask     []byte
	style    []byte
	occupied []byte
	curStyle byte
	// splitWord records that a label was broken inside a word to fit its box.
	// Narrowing to fit an area must not do this: a box of sliced fragments is
	// less readable than the source it replaced.
	splitWord bool
}

func newCanvas(w, h int) *canvas {
	n := w * h
	ch := make([]string, n)
	cls := make([]Cls, n)
	for i := range ch {
		ch[i] = " "
		cls[i] = ClsNone
	}
	return &canvas{
		w: w, h: h, ch: ch, cls: cls,
		mask: make([]byte, n), style: make([]byte, n), occupied: make([]byte, n),
		curStyle: stySolid,
	}
}

func (c *canvas) idx(x, y int) int { return y*c.w + x }

func (c *canvas) set(x, y int, ch string, cls Cls) {
	if x >= c.w || y >= c.h {
		return
	}
	i := c.idx(x, y)
	c.ch[i] = ch
	c.cls[i] = cls
}

// addBits accumulates direction bits on a free cell. border cells are never
// reclassified, so a connector meeting a box keeps the box's styling.
func (c *canvas) addBits(x, y, bits int, cls Cls) {
	if x >= c.w || y >= c.h {
		return
	}
	i := c.idx(x, y)
	if c.occupied[i] != 0 {
		return
	}
	c.mask[i] |= byte(bits)
	c.style[i] |= c.curStyle
	if c.cls[i] != ClsBorder {
		c.cls[i] = cls
	}
}

// blit stamps a finished sub-canvas (a subgraph frame's contents) at an offset.
func (c *canvas) blit(sub *canvas, ox, oy int) {
	for sy := 0; sy < sub.h; sy++ {
		for sx := 0; sx < sub.w; sx++ {
			x, y := ox+sx, oy+sy
			if x >= c.w || y >= c.h {
				continue
			}
			si := sub.idx(sx, sy)
			di := c.idx(x, y)
			c.ch[di] = sub.ch[si]
			c.cls[di] = sub.cls[si]
			c.style[di] = sub.style[si]
			c.occupied[di] = 1
		}
	}
}

// junction adds direction bits even to an occupied cell, so an edge can meet a
// border.
func (c *canvas) junction(x, y, bits int) {
	if x >= c.w || y >= c.h {
		return
	}
	i := c.idx(x, y)
	c.mask[i] |= byte(bits)
	if c.cls[i] != ClsBorder {
		c.cls[i] = ClsEdge
	}
}

func (c *canvas) segV(x, y0, y1 int) {
	a, b := min(y0, y1), max(y0, y1)
	for y := a; y <= b; y++ {
		bits := 0
		if y > a {
			bits |= bitU
		}
		if y < b {
			bits |= bitD
		}
		c.addBits(x, y, bits, ClsEdge)
	}
}

func (c *canvas) segH(y, x0, x1 int) {
	a, b := min(x0, x1), max(x0, x1)
	for x := a; x <= b; x++ {
		bits := 0
		if x > a {
			bits |= bitL
		}
		if x < b {
			bits |= bitR
		}
		c.addBits(x, y, bits, ClsEdge)
	}
}

// finalizeMask resolves accumulated direction bits into glyphs, honouring style.
func (c *canvas) finalizeMask() {
	for i := range c.ch {
		if c.mask[i] != 0 && c.ch[i] == " " {
			ch := maskChar(int(c.mask[i]))
			switch c.style[i] {
			case styDot:
				ch = dottedChar(ch)
			case styThick:
				ch = thickChar(ch)
			}
			c.ch[i] = ch
		}
	}
}

// flipVertical mirrors top-to-bottom for BT.
func (c *canvas) flipVertical() {
	for y := 0; y < c.h/2; y++ {
		y2 := c.h - 1 - y
		for x := 0; x < c.w; x++ {
			i, j := c.idx(x, y), c.idx(x, y2)
			c.ch[i], c.ch[j] = c.ch[j], c.ch[i]
			c.cls[i], c.cls[j] = c.cls[j], c.cls[i]
		}
	}
	for i := range c.ch {
		c.ch[i] = flipGlyphV(c.ch[i])
	}
}

// flipHorizontal mirrors left-to-right for RL, then reverses each text/label run
// back to reading order.
func (c *canvas) flipHorizontal() {
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w/2; x++ {
			x2 := c.w - 1 - x
			i, j := c.idx(x, y), c.idx(x2, y)
			c.ch[i], c.ch[j] = c.ch[j], c.ch[i]
			c.cls[i], c.cls[j] = c.cls[j], c.cls[i]
		}
	}
	for i := range c.ch {
		c.ch[i] = flipGlyphH(c.ch[i])
	}
	for y := 0; y < c.h; y++ {
		x := 0
		for x < c.w {
			cls := c.cls[c.idx(x, y)]
			if cls == ClsText || cls == ClsEdgeLabel {
				start := c.idx(x, y)
				for x < c.w && c.cls[c.idx(x, y)] == cls {
					x++
				}
				reverseSlice(c.ch, start, c.idx(x, y))
			} else {
				x++
			}
		}
	}
}

// toLines groups each row into runs of one class, dropping wide-glyph
// continuations, and trims leading/trailing blank rows.
func (c *canvas) toLines() (plain []string, styled [][]Span, width int) {
	for y := 0; y < c.h; y++ {
		last := 0
		for x := c.w - 1; x >= 0; x-- {
			if c.ch[c.idx(x, y)] != " " {
				last = x + 1
				break
			}
		}
		if last > width {
			width = last
		}
		var spans []Span
		var plainRow strings.Builder
		run := ""
		runCls := ClsNone
		for x := 0; x < last; x++ {
			i := c.idx(x, y)
			ch := c.ch[i]
			if ch == cont {
				continue
			}
			cls := c.cls[i]
			plainRow.WriteString(ch)
			if cls != runCls && run != "" {
				spans = append(spans, Span{Text: run, Cls: runCls})
				run = ""
			}
			runCls = cls
			run += ch
		}
		if run != "" {
			spans = append(spans, Span{Text: run, Cls: runCls})
		}
		styled = append(styled, spans)
		// Only trailing ASCII spaces; a trailing NBSP that styled keeps must stay.
		plain = append(plain, strings.TrimRight(plainRow.String(), " "))
	}
	first := 0
	for first < len(plain) && plain[first] == "" {
		first++
	}
	end := len(plain)
	for end > first && plain[end-1] == "" {
		end--
	}
	return plain[first:end], styled[first:end], width
}

func reverseSlice(arr []string, start, end int) {
	for i, j := start, end-1; i < j; i, j = i+1, j-1 {
		arr[i], arr[j] = arr[j], arr[i]
	}
}

// drawText paints text at x,y, one grapheme cluster per cell. A wide cluster
// claims a second cell marked cont.
func drawText(c *canvas, text string, x, y int, cls Cls) {
	cur := x
	for _, mc := range measured(text) {
		if mc.width == 0 {
			continue
		}
		c.set(cur, y, mc.cluster, cls)
		for k := 1; k < mc.width; k++ {
			c.set(cur+k, y, cont, cls)
		}
		cur += mc.width
	}
}

// drawTextOverEdges paints text at x,y, clearing any edge bits underneath first
// (sequence messages, dividers, compartment rows that must win over a line).
func drawTextOverEdges(c *canvas, text string, x, y int, cls Cls) {
	cur := x
	for _, mc := range measured(text) {
		if mc.width == 0 {
			continue
		}
		for k := 0; k < mc.width; k++ {
			if cur+k < c.w && y < c.h {
				c.mask[c.idx(cur+k, y)] = 0
			}
			ch := cont
			if k == 0 {
				ch = mc.cluster
			}
			c.set(cur+k, y, ch, cls)
		}
		cur += mc.width
	}
}

func maskChar(mask int) string {
	switch mask {
	case 0:
		return " "
	case bitU, bitD, bitU | bitD:
		return "│"
	case bitL, bitR, bitL | bitR:
		return "─"
	case bitD | bitR:
		return "┌"
	case bitD | bitL:
		return "┐"
	case bitU | bitR:
		return "└"
	case bitU | bitL:
		return "┘"
	case bitU | bitD | bitR:
		return "├"
	case bitU | bitD | bitL:
		return "┤"
	case bitD | bitL | bitR:
		return "┬"
	case bitU | bitL | bitR:
		return "┴"
	default:
		return "┼"
	}
}

var dottedGlyph = map[string]string{"─": "╌", "│": "╎"}

var thickGlyph = map[string]string{
	"─": "━", "│": "┃", "┌": "┏", "┐": "┓", "└": "┗", "┘": "┛",
	"├": "┣", "┤": "┫", "┬": "┳", "┴": "┻", "┼": "╋",
}

var flipVGlyph = map[string]string{
	"┌": "└", "└": "┌", "┐": "┘", "┘": "┐", "┏": "┗", "┗": "┏", "┓": "┛", "┛": "┓",
	"╭": "╰", "╰": "╭", "╮": "╯", "╯": "╮", "┬": "┴", "┴": "┬", "┳": "┻", "┻": "┳",
	"▼": "▲", "▲": "▼", "▽": "△", "△": "▽",
}

var flipHGlyph = map[string]string{
	"┌": "┐", "┐": "┌", "└": "┘", "┘": "└", "┏": "┓", "┓": "┏", "┗": "┛", "┛": "┗",
	"╭": "╮", "╮": "╭", "╰": "╯", "╯": "╰", "├": "┤", "┤": "├", "┣": "┫", "┫": "┣",
	"▶": "◄", "◄": "▶", "▷": "◁", "◁": "▷",
}

func mapGlyph(m map[string]string, c string) string {
	if v, ok := m[c]; ok {
		return v
	}
	return c
}

func dottedChar(c string) string { return mapGlyph(dottedGlyph, c) }
func thickChar(c string) string  { return mapGlyph(thickGlyph, c) }
func flipGlyphV(c string) string { return mapGlyph(flipVGlyph, c) }
func flipGlyphH(c string) string { return mapGlyph(flipHGlyph, c) }
