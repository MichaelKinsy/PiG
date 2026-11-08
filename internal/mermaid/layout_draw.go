// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import "slices"

// Box drawing and edge routing, ported from lovely-mermaid layout.ts (drawBox
// .. placeLabel).

func drawBox(c *canvas, p placed, lines []string, sh shape, mirrored bool) {
	x, y, w, h := p.x, p.y, p.w, p.h
	right := x + w - 1
	bottom := y + h - 1

	// A diamond is a double-line box: the terminal's nod to `A{...}`.
	var tl, tr, bl, br string
	switch sh {
	case shapeDiamond:
		tl, tr, bl, br = "╔", "╗", "╚", "╝"
	case shapeRound:
		tl, tr, bl, br = "╭", "╮", "╰", "╯"
	default:
		tl, tr, bl, br = "┌", "┐", "└", "┘"
	}
	c.set(x, y, tl, RoleBorder)
	c.set(right, y, tr, RoleBorder)
	c.set(x, bottom, bl, RoleBorder)
	c.set(right, bottom, br, RoleBorder)

	if sh == shapeDiamond {
		// Double lines have no direction bits; edges tee into them through the
		// mixed junctions (`╤` `╧` `╟` `╢`) that finalizeMask resolves.
		for cx := x + 1; cx < right; cx++ {
			c.set(cx, y, "═", RoleBorder)
			c.set(cx, bottom, "═", RoleBorder)
		}
		for cy := y + 1; cy < bottom; cy++ {
			c.set(x, cy, "║", RoleBorder)
			c.set(right, cy, "║", RoleBorder)
		}
	} else {
		// The perimeter is drawn as bits so edges can tee into it, but it is the
		// box outline, so it claims border rather than edge.
		for cx := x + 1; cx < right; cx++ {
			c.addBits(cx, y, bitL|bitR, RoleBorder)
			c.addBits(cx, bottom, bitL|bitR, RoleBorder)
		}
		for cy := y + 1; cy < bottom; cy++ {
			c.addBits(x, cy, bitU|bitD, RoleBorder)
			c.addBits(right, cy, bitU|bitD, RoleBorder)
		}
	}

	for cy := y; cy <= bottom; cy++ {
		for cx := x; cx <= right; cx++ {
			i := c.idx(cx, cy)
			c.occupied[i] = 1
			if c.curTag != "" {
				c.tag[i] = c.curTag
			}
			if c.curHref != "" {
				c.href[i] = c.curHref
			}
		}
	}

	inner := max(1, satSub(w, 2*pad+2))
	ordered := lines
	if mirrored {
		ordered = slices.Clone(lines)
		slices.Reverse(ordered)
	}
	for li, line := range ordered {
		text := fitLabel(line, inner)
		textX := x + 1 + pad + half(satSub(inner, stringWidth(text)))
		drawText(c, text, textX, y+1+li, RoleText)
	}
}

// drawClassBox draws a class or ER box: sections separated by horizontal
// rules, title centred.
func drawClassBox(c *canvas, p placed, sections [][]string, mirrored bool) {
	drawBox(c, p, nil, shapeRect, false)
	inner := max(1, satSub(p.w, 2*pad+2))
	type boxRow struct {
		sep    bool
		text   string
		center bool
	}
	var rows []boxRow
	for si, section := range sections {
		if len(section) == 0 {
			continue
		}
		if len(rows) > 0 {
			rows = append(rows, boxRow{sep: true})
		}
		for _, line := range section {
			rows = append(rows, boxRow{text: fitLabel(line, inner), center: si == 0})
		}
	}
	if mirrored {
		slices.Reverse(rows)
	}
	for ri, r := range rows {
		row := p.y + 1 + ri
		if r.sep {
			c.set(p.x, row, "├", RoleBorder)
			for x := p.x + 1; x < p.x+p.w-1; x++ {
				c.set(x, row, "─", RoleBorder)
			}
			c.set(p.x+p.w-1, row, "┤", RoleBorder)
		} else {
			tx := p.x + 1 + pad
			if r.center {
				tx = p.x + 1 + pad + half(satSub(inner, stringWidth(r.text)))
			}
			drawTextOverEdges(c, r.text, tx, row, RoleText)
		}
	}
}

// drawFrame draws a subgraph frame: a titled box with a finished sub-canvas
// centred inside.
func drawFrame(c *canvas, p placed, title string, sub *canvas, mirrored bool) {
	drawBox(c, p, nil, shapeRect, false)
	// An unlabelled frame (a state `--` region) keeps its border unbroken.
	if title != "" {
		t := fitLabel(title, satSub(p.w, 4))
		// Mirrored: the bottom border becomes the top after the flip.
		row := p.y
		if mirrored {
			row = p.y + p.h - 1
		}
		drawTextOverEdges(c, " "+t+" ", p.x+1, row, RoleText)
	}
	c.blit(sub, p.x+1+half(p.w-2-sub.w), p.y+1+half(p.h-2-sub.h))
}

// ------------------------------------------------------------------- routing

func headGlyph(h head, arrow string) string {
	switch h {
	case headCircle:
		return "o"
	case headCross:
		return "×"
	case headDiamondFill:
		return "◆"
	case headDiamondOpen:
		return "◇"
	case headTriangle:
		switch arrow {
		case "▼":
			return "▽"
		case "▲":
			return "△"
		case "◄":
			return "◁"
		case "▶":
			return "▷"
		}
		return arrow
	default:
		return arrow
	}
}

// routeForward routes adjacent ranks, top-down: drop, jog along the bus row,
// drop into the head. entryX overrides the centre entry column when the
// target's top is shared with skip entries (-1: none); labelLeft renders the
// label left of the arrowhead.
func routeForward(c *canvas, from, to placed, e edge, bus, entryX int, labelLeft bool) {
	tx := to.cx
	if entryX != -1 {
		tx = entryX
	}
	// A jog of one column reads as a kink; snap straight instead.
	bx := from.cx
	if absInt(from.cx-tx) <= 1 {
		bx = tx
	}
	by := from.y + from.h - 1
	headRow := to.y - 1

	c.junction(bx, by, bitD)
	c.segV(bx, by, bus)
	if bx == tx {
		c.segV(bx, bus, headRow)
	} else {
		c.segH(bus, bx, tx)
		c.segV(tx, bus, headRow)
	}

	if e.headTo == headNone {
		c.addBits(tx, headRow, bitU, RoleEdge)
	} else {
		c.set(tx, headRow, headGlyph(e.headTo, "▼"), RoleEdge)
	}
	if e.headFrom != headNone {
		c.set(bx, by, headGlyph(e.headFrom, "▲"), RoleEdge)
	}

	if e.cardFrom == "" && e.cardTo == "" {
		if e.label != nil {
			start := tx + 1
			if labelLeft {
				start = satSub(tx, min(stringWidth(*e.label), maxLabel))
			}
			placeLabel(c, *e.label, headRow, start)
		}
		return
	}
	// Cardinalities sit at their own ends; the verb takes the row above the
	// head, falling back beside the target card when the gap has no spare row.
	srcRow := by + 1
	if e.cardFrom != "" {
		placeLabel(c, e.cardFrom, srcRow, bx+1)
	}
	if e.cardTo != "" {
		placeLabel(c, e.cardTo, headRow, tx+1)
	}
	if e.label != nil {
		midRow := headRow - 1
		if midRow > srcRow {
			lineX := bx
			if midRow > bus {
				lineX = tx
			}
			placeLabel(c, *e.label, midRow, lineX+1)
		} else {
			off := 0
			if e.cardTo != "" {
				off = stringWidth(e.cardTo) + 1
			}
			placeLabel(c, *e.label, headRow, tx+1+off)
		}
	}
}

// routeBackAdjacent routes adjacent ranks, top-down, against the flow: up out
// of the source's top, jog along the band, arrow into the target's bottom. The
// short local return mermaid draws, not a trip around the diagram.
func routeBackAdjacent(c *canvas, from, to placed, e edge, bus int) {
	// Attach right of centre so the return does not merge with the forward
	// exits and arrivals that own the centre column.
	tx := min(to.x+to.w-2, to.cx+2)
	bx0 := min(from.x+from.w-2, from.cx+2)
	bx := bx0
	if absInt(bx0-tx) <= 1 {
		bx = tx
	}
	fy := from.y
	headRow := to.y + to.h

	c.junction(bx, fy, bitU)
	if bx == tx {
		c.segV(bx, headRow, fy)
	} else {
		c.segV(bx, bus, fy)
		c.segH(bus, bx, tx)
		c.segV(tx, headRow, bus)
	}

	if e.headTo == headNone {
		c.addBits(tx, headRow, bitD, RoleEdge)
	} else {
		c.set(tx, headRow, headGlyph(e.headTo, "▲"), RoleEdge)
	}
	if e.headFrom != headNone {
		c.set(bx, fy, headGlyph(e.headFrom, "▼"), RoleEdge)
	}

	if text, ok := edgeText(e); ok {
		placeLabel(c, text, headRow, tx+1)
	}
}

// routeSelf draws a self-edge: a stub loop hanging below the box.
func routeSelf(c *canvas, p placed, e edge) {
	bottom := p.y + p.h - 1
	exitX := p.cx + 1
	retX := p.x + p.w - 2
	if retX <= exitX || bottom+2 >= c.h {
		return
	}

	var v, h, bl, br string
	switch e.line {
	case lineDotted:
		v, h, bl, br = "╎", "╌", "╰", "╯"
	case lineThick:
		v, h, bl, br = "┃", "━", "┗", "┛"
	default:
		v, h, bl, br = "│", "─", "╰", "╯"
	}

	c.junction(exitX, bottom, bitD)
	c.set(exitX, bottom+1, v, RoleEdge)
	c.set(exitX, bottom+2, bl, RoleEdge)
	for x := exitX + 1; x < retX; x++ {
		c.set(x, bottom+2, h, RoleEdge)
	}
	c.set(retX, bottom+2, br, RoleEdge)
	c.set(retX, bottom+1, headGlyph(e.headTo, "▲"), RoleEdge)
	if text, ok := edgeText(e); ok {
		placeLabel(c, text, bottom+1, p.x+p.w+1)
	}
}

// skipAt is the geometry a forward skip edge routes through.
type skipAt struct {
	laneX, entryX, busY, approachY int
	straight, labelLeft            bool
}

// routeSkip routes a forward skip edge, top-down: out the source's *bottom*
// onto its band's bus row (endpoint sharing folds it onto its siblings' row:
// one `┴` origin split), then either straight down an unobstructed column into
// the target's *top*, or around: down the lane and in along the reserved
// approach row above the target's rank, over the arrival heads and labels.
func routeSkip(c *canvas, from, to placed, e edge, at skipAt) {
	laneX, entryX, busY, approachY := at.laneX, at.entryX, at.busY, at.approachY
	bx := from.cx
	bottom := from.y + from.h - 1

	c.junction(bx, bottom, bitD)
	c.segV(bx, bottom, busY)
	if at.straight {
		c.segH(busY, bx, entryX)
		c.segV(entryX, busY, to.y-1)
	} else {
		c.segH(busY, bx, laneX)
		c.segV(laneX, busY, approachY)
		c.segH(approachY, entryX, laneX)
		c.segV(entryX, approachY, to.y-1)
	}

	if e.headTo == headNone {
		c.addBits(entryX, to.y-1, bitD, RoleEdge)
	} else {
		c.set(entryX, to.y-1, headGlyph(e.headTo, "▼"), RoleEdge)
	}
	if e.headFrom != headNone {
		c.set(bx, bottom, headGlyph(e.headFrom, "▲"), RoleEdge)
	}

	if text, ok := edgeText(e); ok {
		start := entryX + 1
		if at.labelLeft {
			start = satSub(entryX, min(stringWidth(text), maxLabel))
		}
		placeLabel(c, text, to.y-1, start)
	}
}

// routeBack routes a multi-rank back edge, top-down: out the right side, up a
// lane, back in through the target's right side. Side entry keeps returns
// recognisable at a glance.
func routeBack(c *canvas, from, to placed, e edge, laneX int) {
	sx := from.x + from.w - 1
	sy := from.cy
	backText, hasText := edgeText(e)

	c.junction(sx, sy, bitR)
	c.segH(sy, sx, laneX)
	if e.headFrom != headNone {
		c.set(sx, sy, headGlyph(e.headFrom, "◄"), RoleEdge)
	}

	tx := to.x + to.w - 1
	tyc := to.cy
	c.segV(laneX, sy, tyc)
	c.segH(tyc, tx+1, laneX)
	if e.headTo == headNone {
		c.addBits(tx+1, tyc, bitR, RoleEdge)
	} else {
		c.set(tx+1, tyc, headGlyph(e.headTo, "◄"), RoleEdge)
	}
	if hasText {
		placeLabel(c, backText, satSub(tyc, 1), satSub(laneX, stringWidth(backText)+1))
	}
}

// routeForwardLr routes adjacent ranks, left-to-right: out the right side, jog
// on the bus column.
func routeForwardLr(c *canvas, from, to placed, e edge, bus int) {
	rx := from.x + from.w - 1
	ry := from.cy
	ly := to.cy
	headCol := to.x - 1

	c.junction(rx, ry, bitR)
	c.segH(ry, rx, bus)
	if ry == ly {
		c.segH(ry, bus, headCol)
	} else {
		c.segV(bus, ry, ly)
		c.segH(ly, bus, headCol)
	}

	if e.headTo == headNone {
		c.addBits(headCol, ly, bitR, RoleEdge)
	} else {
		c.set(headCol, ly, headGlyph(e.headTo, "▶"), RoleEdge)
	}
	if e.headFrom != headNone {
		c.set(rx, ry, headGlyph(e.headFrom, "◄"), RoleEdge)
	}

	// The verb keeps its usual spot above the line; cardinalities hug their own
	// ends on the rows above the departure and arrival cells.
	if e.label != nil {
		placeLabel(c, *e.label, satSub(ly, 1), bus+1)
	}
	if e.cardFrom != "" {
		placeLabel(c, e.cardFrom, satSub(ry, 1), rx+1)
	}
	if e.cardTo != "" {
		placeLabel(c, e.cardTo, satSub(ly, 1), satSub(headCol, stringWidth(e.cardTo)))
	}
}

// routeSkipLr routes a straight forward skip, left-to-right: out the source's
// right-side fan, jog on its own band's bus column, then straight along the
// target's entry row into its left side, merging with the forward arrivals'
// head. Only used when that row crosses no intermediate box; the bottom lane
// is the fallback.
func routeSkipLr(c *canvas, from, to placed, e edge, bus int) {
	ry := from.cy
	ty := to.cy
	rx := from.x + from.w - 1
	headCol := to.x - 1

	c.junction(rx, ry, bitR)
	c.segH(ry, rx, bus)
	if ry != ty {
		c.segV(bus, ry, ty)
	}
	c.segH(ty, bus, headCol)

	if e.headTo == headNone {
		c.addBits(headCol, ty, bitR, RoleEdge)
	} else {
		c.set(headCol, ty, headGlyph(e.headTo, "▶"), RoleEdge)
	}
	if e.headFrom != headNone {
		c.set(rx, ry, headGlyph(e.headFrom, "◄"), RoleEdge)
	}

	// Label after the bus jog, where forward labels sit: the gap before the
	// target belongs to the arrivals that end there.
	if text, ok := edgeText(e); ok {
		placeLabel(c, text, satSub(ty, 1), bus+1)
	}
}

// laneLabel is a lane label waiting for every route to land before claiming its spot.
type laneLabel struct {
	text   string
	y      int
	lo, hi int
}

// routeBackLr routes a skip or back edge, left-to-right: down out the bottom,
// along a lane, back up.
func routeBackLr(c *canvas, from, to placed, e edge, laneY int, laneLabels *[]laneLabel) {
	sx := from.cx
	sy := from.y + from.h - 1
	tx := to.cx
	ty := to.y + to.h - 1

	c.junction(sx, sy, bitD)
	c.segV(sx, sy, laneY)
	c.segH(laneY, sx, tx)
	c.segV(tx, laneY, ty+1)

	if e.headTo == headNone {
		c.addBits(tx, ty+1, bitD, RoleEdge)
	} else {
		c.set(tx, ty+1, headGlyph(e.headTo, "▲"), RoleEdge)
	}
	if e.headFrom != headNone {
		c.set(sx, sy, headGlyph(e.headFrom, "▲"), RoleEdge)
	}

	// The label interrupts its own lane row: the row above belongs to the
	// neighbouring lane once several stack. Deferred until all edges landed, so
	// it can dodge the verticals that cross this row.
	if backText, ok := edgeText(e); ok {
		*laneLabels = append(*laneLabels, laneLabel{
			text: " " + fitLabel(backText, maxLabel) + " ",
			y:    laneY, lo: min(sx, tx), hi: max(sx, tx),
		})
	}
}

// placeLaneLabels writes each lane label onto its own row, centred on the run
// but slid to the nearest stretch free of crossing verticals, arrowheads and
// earlier labels: clearing a crossing line under a label would sever it.
func placeLaneLabels(c *canvas, labels []laneLabel) {
	for _, l := range labels {
		text, y, lo, hi := l.text, l.y, l.lo, l.hi
		tw := stringWidth(text)
		lastStart := hi - 1 - tw
		if lastStart < lo+1 || y >= c.h {
			continue
		}
		clear := func(start int) bool {
			for x := start; x < start+tw; x++ {
				i := c.idx(x, y)
				if c.occupied[i] == 1 {
					return false
				}
				if c.mask[i]&(bitU|bitD) != 0 {
					return false
				}
				if c.ch[i] != " " {
					return false
				}
			}
			return true
		}
		mid := min(max(half(lo+hi)-half(tw), lo+1), lastStart)
		at := mid
		for d := 0; ; d++ {
			left := mid - d
			right := mid + d
			if left < lo+1 && right > lastStart {
				break
			}
			if left >= lo+1 && clear(left) {
				at = left
				break
			}
			if right <= lastStart && clear(right) {
				at = right
				break
			}
		}
		drawTextOverEdges(c, text, at, y, RoleEdgeLabel)
	}
}

// placeLabel writes an edge label, stopping at the first cell already occupied.
func placeLabel(c *canvas, label string, row, startX int) {
	if row >= c.h {
		return
	}
	text := fitLabel(label, maxLabel)
	x := startX
	for _, mc := range measured(text) {
		if mc.width == 0 {
			continue
		}
		if x+mc.width > c.w {
			break
		}
		blocked := false
		for k := 0; k < mc.width; k++ {
			i := c.idx(x+k, row)
			if c.ch[i] != " " || c.mask[i] != 0 || c.occupied[i] != 0 {
				blocked = true
			}
		}
		if blocked {
			break
		}
		c.set(x, row, mc.cluster, RoleEdgeLabel)
		for k := 1; k < mc.width; k++ {
			c.set(x+k, row, cont, RoleEdgeLabel)
		}
		x += mc.width
	}
}
