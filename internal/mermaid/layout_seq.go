// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import "sort"

// Sequence diagram layout, ported from lovely-mermaid layout-seq.ts. Participants
// get one column each with lifelines the full height and a box at top and
// bottom; column gaps solve from the widest thing between any two columns.

const seqGap = 5 // minimum columns between adjacent lifelines

func ceilDiv2(n int) int { return (n + 1) / 2 }

// noteGeometry returns where a note box sits, given the lifeline positions.
func noteGeometry(xs []int, anchor noteAnchor, textW int) (x, w int) {
	if anchor.kind == noteOver {
		center := half(xs[anchor.from] + xs[anchor.to])
		w = max(xs[anchor.to]-xs[anchor.from]+5, textW+2*pad+2)
		return satSub(center, half(w)), w
	}
	w = textW + 2*pad + 2
	if anchor.kind == noteLeft {
		return satSub(xs[anchor.at], 2+w-1), w
	}
	return xs[anchor.at] + 2, w
}

func itemTextW(text *string) int {
	if text == nil {
		return 0
	}
	return stringWidth(*text)
}

func layoutSequence(seq *sequence) *canvas {
	n := len(seq.labels)
	labels := make([]string, n)
	boxW := make([]int, n)
	for i, l := range seq.labels {
		labels[i] = fitLabel(l, wrapWidth)
		boxW[i] = max(1, stringWidth(labels[i])) + 2*pad + 2
	}
	const boxH = 3

	gaps := make([]int, satSub(n, 1))
	for i := range gaps {
		gaps[i] = max(seqGap, ceilDiv2(boxW[i])+ceilDiv2(boxW[i+1])+1)
	}

	// Each requirement: columns l..r together need at least `need` cells.
	var reqs [][3]int
	for _, item := range seq.items {
		switch item.kind {
		case seqMessage:
			tw := itemTextW(item.text)
			if item.from != item.to {
				reqs = append(reqs, [3]int{min(item.from, item.to), max(item.from, item.to), max(tw+2, 4)})
			} else if item.from+1 < n {
				reqs = append(reqs, [3]int{item.from, item.from + 1, 5 + tw + 2})
			}
		case seqNote:
			tw := stringWidth(item.str)
			a := item.anchor
			switch {
			case a.kind == noteOver && a.from < a.to:
				reqs = append(reqs, [3]int{a.from, a.to, satSub(tw, 1)})
			case a.kind == noteOver:
				need := ceilDiv2(tw+4) + 2
				if a.from > 0 {
					reqs = append(reqs, [3]int{a.from - 1, a.from, need})
				}
				if a.from+1 < n {
					reqs = append(reqs, [3]int{a.from, a.from + 1, need})
				}
			case a.kind == noteLeft && a.at > 0:
				reqs = append(reqs, [3]int{a.at - 1, a.at, tw + 7})
			case a.kind == noteRight && a.at+1 < n:
				reqs = append(reqs, [3]int{a.at, a.at + 1, tw + 7})
			}
		}
	}
	// Narrowest spans first, so a wide requirement absorbs what they already gave.
	sort.SliceStable(reqs, func(a, b int) bool {
		return reqs[a][1]-reqs[a][0] < reqs[b][1]-reqs[b][0]
	})
	for _, req := range reqs {
		l, r, need := req[0], req[1], req[2]
		cur := 0
		for i := l; i < r; i++ {
			cur += gaps[i]
		}
		if cur < need {
			gaps[r-1] += need - cur
		}
	}

	xs := make([]int, n)
	xs[0] = half(boxW[0])
	for i := 1; i < n; i++ {
		xs[i] = xs[i-1] + gaps[i-1]
	}

	// A note left of the first participant has no gap to grow; shift the whole
	// diagram right instead so the note box lands beside the lifeline, not on it.
	leftPad := 0
	for _, item := range seq.items {
		if item.kind == seqNote && item.anchor.kind == noteLeft && item.anchor.at == 0 {
			w := stringWidth(item.str) + 2*pad + 2
			leftPad = max(leftPad, satSub(w+1, xs[0]))
		}
	}
	if leftPad > 0 {
		for i := range n {
			xs[i] += leftPad
		}
	}

	canvasW := xs[n-1] + ceilDiv2(boxW[n-1]) + 1
	for _, item := range seq.items {
		switch item.kind {
		case seqMessage:
			if item.from == item.to {
				canvasW = max(canvasW, xs[item.from]+5+itemTextW(item.text)+1)
			}
		case seqNote:
			gx, gw := noteGeometry(xs, item.anchor, stringWidth(item.str))
			canvasW = max(canvasW, gx+gw+1)
		case seqDivider:
			canvasW = max(canvasW, stringWidth(item.str)+4)
		}
	}

	rows := make([]int, len(seq.items))
	y := boxH + 1
	for k, item := range seq.items {
		rows[k] = y
		y += rowHeight(item)
	}
	bottomTop := y
	canvasH := bottomTop + boxH

	if canvasW*canvasH > maxCanvasCells {
		return nil
	}

	c := newCanvas(canvasW, canvasH)

	for i := range n {
		for _, by := range []int{0, bottomTop} {
			drawBox(c, seqBox(satSub(xs[i], half(boxW[i])), by, boxW[i], boxH), []string{labels[i]}, shapeRect, false)
		}
	}
	for k, item := range seq.items {
		if item.kind != seqNote {
			continue
		}
		gx, gw := noteGeometry(xs, item.anchor, stringWidth(item.str))
		drawBox(c, seqBox(gx, rows[k], gw, 3), []string{item.str}, shapeRect, false)
	}

	for _, x := range xs {
		c.junction(x, boxH-1, bitD)
		c.segV(x, boxH, bottomTop-1)
		c.junction(x, bottomTop, bitU)
	}

	for k, item := range seq.items {
		r := rows[k]
		switch item.kind {
		case seqMessage:
			drawMessage(c, item, xs, r)
		case seqDivider:
			drawDivider(c, item.str, r, canvasW)
		}
	}

	// Activations turn the lifeline into a double line from the activating
	// message's arrow row to the deactivating one's (to the bottom while still
	// open): two rails, echoing mermaid's slim activation rectangle. Setting the
	// glyph directly leaves the mask bits for finalizeMask, whose double-tee pass
	// resolves message junctions on the run (║ -> ╟ ╢ ╫).
	startRow := func(k int) int {
		item := seq.items[k]
		labeled := item.kind == seqMessage && item.from != item.to && item.text != nil
		if labeled {
			return rows[k] + 1
		}
		return rows[k]
	}
	endRow := func(k int) int {
		item := seq.items[k]
		// A self-message stub returns two rows below where it left.
		if item.kind == seqMessage && item.from == item.to {
			return rows[k] + 2
		}
		return startRow(k)
	}
	for _, a := range seq.activations {
		y1 := bottomTop - 1
		if a.to != activationOpen {
			y1 = endRow(a.to)
		}
		for y := startRow(a.from); y <= y1; y++ {
			i := c.idx(xs[a.at], y)
			if c.mask[i] != 0 {
				c.ch[i] = "║"
			}
		}
	}

	c.finalizeMask()
	return c
}

func rowHeight(item seqItem) int {
	switch item.kind {
	case seqNote:
		return 4
	case seqDivider:
		return 2
	}
	if item.from == item.to {
		return 4
	}
	if item.text != nil {
		return 3
	}
	return 2
}

// seqBox builds Placed geometry from position and size (ranks are irrelevant).
func seqBox(x, y, w, h int) placed {
	return placed{x: x, y: y, w: w, h: h, cx: x + half(w), cy: y + 1, rank: 0}
}

func drawMessage(c *canvas, item seqItem, xs []int, r int) {
	lineCh := "─"
	if item.dashed {
		lineCh = "╌"
	}

	if item.from == item.to {
		x := xs[item.from]
		c.junction(x, r, bitR)
		c.set(x+1, r, lineCh, RoleEdge)
		c.set(x+2, r, lineCh, RoleEdge)
		c.set(x+3, r, "╮", RoleEdge)
		c.set(x+3, r+1, "│", RoleEdge)
		headCh := "◄"
		if item.head == seqCross {
			headCh = "×"
		}
		c.set(x+1, r+2, headCh, RoleEdge)
		c.set(x+2, r+2, lineCh, RoleEdge)
		c.set(x+3, r+2, "╯", RoleEdge)
		if item.text != nil {
			drawTextOverEdges(c, *item.text, x+5, r+1, RoleText)
		}
		return
	}

	x0 := xs[item.from]
	x1 := xs[item.to]
	rightward := x1 > x0
	arrowRow := r
	if item.text != nil {
		arrowRow = r + 1
	}
	lo := min(x0, x1)
	hi := max(x0, x1)

	if rightward {
		c.junction(x0, arrowRow, bitR)
	} else {
		c.junction(x0, arrowRow, bitL)
	}
	for x := lo + 1; x < hi; x++ {
		c.set(x, arrowRow, lineCh, RoleEdge)
	}
	headCh := "◄"
	if rightward {
		headCh = "▶"
	}
	if item.head == seqCross {
		headCh = "×"
	}
	if rightward {
		c.set(x1-1, arrowRow, headCh, RoleEdge)
	} else {
		c.set(x1+1, arrowRow, headCh, RoleEdge)
	}

	if item.text != nil {
		span := hi - lo - 1
		t := fitLabel(*item.text, max(1, span))
		drawTextOverEdges(c, t, lo+1+half(satSub(span, stringWidth(t))), r, RoleText)
	}
}

// drawDivider draws a full-width rule labelling a loop/alt/opt block boundary.
func drawDivider(c *canvas, text string, r, canvasW int) {
	for x := range canvasW {
		c.set(x, r, "─", RoleEdge)
	}
	drawTextOverEdges(c, " "+fitLabel(text, satSub(canvasW, 4))+" ", 2, r, RoleEdgeLabel)
}
