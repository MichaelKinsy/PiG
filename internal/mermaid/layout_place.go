// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"slices"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// Track packing and cross-axis/flow-axis placement, ported from lovely-mermaid
// layout.ts (assignTracks .. placeLr).

// edgeText is everything an edge says, joined: the fallback for routes that
// have no per-end placement (lanes, self-loops). Forward routes place cardFrom /
// cardTo at their own ends instead. ok=false when there is nothing to say.
func edgeText(e edge) (string, bool) {
	var parts []string
	if e.cardFrom != "" {
		parts = append(parts, e.cardFrom)
	}
	if e.label != nil && *e.label != "" {
		parts = append(parts, *e.label)
	}
	if e.cardTo != "" {
		parts = append(parts, e.cardTo)
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, " "), true
}

// trackSpan is a span competing for a track: the covered coordinate range plus its edge.
type trackSpan struct {
	start, end, from, to, edge int
	// labeled lanes refuse endpoint sharing: the label would appear to cover
	// every edge merged onto the row. Bus spans never set this: their labels sit
	// at the separate arrival ends, so fan merging stays safe.
	labeled bool
}

// assignTracks packs spans into as few parallel tracks as possible. Two spans
// share a track when they are two cells apart, or when they share an endpoint:
// edges fanning out of one node deliberately reuse a single row so a merge
// draws one arrowhead rather than a stack of them.
func assignTracks(spans []trackSpan, shortestFirst bool) (assigned [][2]int, count int) {
	// Lanes pack shortest-first: a span contained in another takes the inner
	// track, so exits and entries at rows the inner lane never reaches cross
	// nothing. Buses keep the start-ordered packing.
	sorted := slices.Clone(spans)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if shortestFirst {
			if d := (a.end - a.start) - (b.end - b.start); d != 0 {
				return d < 0
			}
		}
		for _, d := range [...]int{a.start - b.start, a.end - b.end, a.from - b.from, a.to - b.to, a.edge - b.edge} {
			if d != 0 {
				return d < 0
			}
		}
		return false
	})
	var tracks [][]trackSpan
	for _, span := range sorted {
		slot := -1
		for ti, members := range tracks {
			fits := true
			for _, m := range members {
				apart := m.end+2 <= span.start || span.end+2 <= m.start
				shared := (m.from == span.from || m.to == span.to) && !m.labeled && !span.labeled
				if !apart && !shared {
					fits = false
					break
				}
			}
			if fits {
				slot = ti
				break
			}
		}
		if slot == -1 {
			tracks = append(tracks, nil)
			slot = len(tracks) - 1
		}
		tracks[slot] = append(tracks[slot], span)
		assigned = append(assigned, [2]int{span.edge, slot})
	}
	return assigned, len(tracks)
}

// isAdjacentBack: an edge between adjacent ranks running against the
// direction. In top-down layouts these route locally through the same band as
// their forward siblings (the short return arrow mermaid draws) rather than
// around the diagram. Left-to-right boxes are three rows tall, leaving no room
// to offset the return off the centre row, so LR/RL keep the lane.
func isAdjacentBack(ranks []int, e edge) bool {
	return e.from != e.to && ranks[e.from] == ranks[e.to]+1
}

// busSpans are the edges crossing the band between rank r and r+1 that must
// jog sideways, so need a bus row. withBack admits adjacent back edges.
func busSpans(g *graph, ranks, centers []int, r int, exact, withBack bool) []trackSpan {
	var out []trackSpan
	for i, e := range g.edges {
		var jogs bool
		if exact {
			jogs = centers[e.from] != centers[e.to]
		} else {
			jogs = absInt(centers[e.from]-centers[e.to]) > 1
		}
		lo := min(ranks[e.from], ranks[e.to])
		var adjacent bool
		if withBack {
			adjacent = absInt(ranks[e.from]-ranks[e.to]) == 1
		} else {
			adjacent = ranks[e.to] == ranks[e.from]+1
		}
		if e.from != e.to && adjacent && lo == r && jogs {
			out = append(out, trackSpan{
				start: min(centers[e.from], centers[e.to]),
				end:   max(centers[e.from], centers[e.to]),
				from:  e.from, to: e.to, edge: i,
			})
		}
	}
	return out
}

// laneSpans are the edges skipping a rank or running backwards; these go
// around in a lane.
func laneSpans(g *graph, ranks []int, placedNodes []placed, vertical bool) []trackSpan {
	var out []trackSpan
	for i, e := range g.edges {
		if e.from == e.to || ranks[e.to] == ranks[e.from]+1 {
			continue
		}
		if vertical && isAdjacentBack(ranks, e) {
			continue
		}
		pf := placedNodes[e.from]
		pt := placedNodes[e.to]
		var a, b int
		if vertical {
			a, b = min(pf.cy, pt.cy), max(pf.cy, pt.cy)
		} else {
			a, b = min(pf.cx, pt.cx), max(pf.cx, pt.cx)
		}
		_, labeled := edgeText(e)
		out = append(out, trackSpan{start: a, end: b, from: e.from, to: e.to, edge: i, labeled: labeled})
	}
	return out
}

func filled[T any](n int, v T) []T {
	out := make([]T, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// labelCols is an edge label's width on its arrival end: -1 when it has none.
func tdLabelW(e edge) int {
	w := -1
	if e.label != nil {
		w = max(w, min(stringWidth(*e.label), maxLabel))
	}
	if e.cardTo != "" {
		w = max(w, min(stringWidth(e.cardTo), maxLabel))
	}
	return w
}

// ----------------------------------------------------------------- placement

func placeTd(ranks []int, maxRank int, byRank [][]int, sizes nodeSizes, g *graph, placedNodes []placed) routePlan {
	centers := assignPositions(byRank, sizes.layW, gapX, g.edges, ranks)

	// Top-entry geometry, derivable before placement. A node's entries (the
	// merged forward cluster plus each skip) spread evenly across the box top
	// (two entries land at ~1/3 and ~2/3). A forward arrival aligned with its
	// source stays at centre so chains keep no kinks, and the skips spread over
	// the right half instead. A label that does not fit before the next entry
	// renders left of its arrow. A skip whose entry column crosses no box on any
	// intermediate rank drops straight instead of taking a lane.
	boxL := func(j int) int { return satSub(centers[j], half(sizes.boxW[j])) }
	boxR := func(j int) int { return boxL(j) + sizes.boxW[j] - 1 }
	isSkip := func(e edge) bool { return e.from != e.to && ranks[e.to]-ranks[e.from] > 1 }
	edgeEntryX := filled(len(g.edges), -1)
	edgeStraight := filled(len(g.edges), false)
	fwdEntryX := filled(len(g.nodes), -1)
	edgeLabelLeft := filled(len(g.edges), false)
	skipsInto := make([][]int, len(g.nodes))
	fwdsInto := make([][]int, len(g.nodes))
	for i, e := range g.edges {
		if e.from == e.to {
			continue
		}
		if isSkip(e) {
			skipsInto[e.to] = append(skipsInto[e.to], i)
		} else if ranks[e.to] == ranks[e.from]+1 {
			fwdsInto[e.to] = append(fwdsInto[e.to], i)
		}
	}
	type entry struct{ slot, w, skip int } // skip is -1 for the forward cluster
	for t := range g.nodes {
		skips := skipsInto[t]
		if len(skips) == 0 {
			continue
		}
		fwds := fwdsInto[t]
		cx := centers[t]
		left := boxL(t)
		right := boxR(t)
		hasFwd := len(fwds) > 0
		aligned := false
		if hasFwd {
			for _, i := range fwds {
				if absInt(centers[g.edges[i].from]-cx) <= 1 {
					aligned = true
					break
				}
			}
		}
		spreadL := left
		if hasFwd && aligned {
			spreadL = cx
		}
		slots := len(skips)
		if hasFwd && !aligned {
			slots++
		}
		slot := func(i int) int {
			return spreadL + int(jsnumber.Round(float64((right-spreadL)*(i+1))/float64(slots+1)))
		}
		// Ordered entries: the forward cluster (if any), then each skip.
		var items []entry
		if hasFwd {
			fw := tdLabelW(g.edges[fwds[0]])
			for _, i := range fwds[1:] {
				fw = max(fw, tdLabelW(g.edges[i]))
			}
			s := slot(0)
			if aligned {
				s = cx
			}
			items = append(items, entry{slot: s, w: fw, skip: -1})
		}
		for j, si := range skips {
			k := j
			if hasFwd && !aligned {
				k = j + 1
			}
			items = append(items, entry{slot: slot(k), w: tdLabelW(g.edges[si]), skip: si})
		}
		// Walk left to right with a cursor over the free head-row cells: each
		// entry lands at its slot (or past the previous label), its own label
		// going right when the next slot leaves room, else left when the cells
		// behind the cursor allow. Overflow falls back to the legacy rule.
		var cols []int
		var lefts []bool
		cursor := left
		fits := true
		for i, item := range items {
			x := max(item.slot, cursor)
			if x > right-1 {
				fits = false
				break
			}
			next := 1<<53 - 1
			if i+1 < len(items) {
				next = items[i+1].slot
			}
			w := item.w
			if w >= 0 && x+w+2 > next && x-cursor >= w {
				lefts = append(lefts, true)
				cursor = x + 2
			} else {
				lefts = append(lefts, false)
				if w >= 0 {
					cursor = x + w + 2
				} else {
					cursor = x + 2
				}
			}
			cols = append(cols, x)
		}
		if fits {
			for i, item := range items {
				if item.skip == -1 {
					fwdEntryX[t] = cols[i]
					if lefts[i] {
						for _, fi := range fwds {
							edgeLabelLeft[fi] = true
						}
					}
				} else {
					edgeEntryX[item.skip] = cols[i]
					edgeLabelLeft[item.skip] = lefts[i]
				}
			}
			continue
		}
		// Legacy: forward stays centred; a skip lands past the arrival labels, or
		// left of centre with its own label flipped left.
		reach := -1
		if hasFwd {
			reach = cx
			for _, i := range fwds {
				reach = max(reach, cx+1+tdLabelW(g.edges[i]))
			}
		}
		for _, si := range skips {
			clear := cx + 2
			if reach != -1 {
				clear = reach + 2
			}
			capped := min(clear, right-1)
			if capped > max(cx, reach) {
				edgeEntryX[si] = capped
			} else {
				edgeEntryX[si] = max(left+1, cx-2)
				edgeLabelLeft[si] = true
			}
		}
	}
	for i, e := range g.edges {
		if !isSkip(e) {
			continue
		}
		entryX := edgeEntryX[i]
		straight := true
		for j := range g.nodes {
			if ranks[j] > ranks[e.from] && ranks[j] < ranks[e.to] && entryX >= boxL(j) && entryX <= boxR(j) {
				straight = false
				break
			}
		}
		edgeStraight[i] = straight
	}

	edgeBus := make([]int, len(g.edges))
	busTracks := make([]int, maxRank+1)
	for r := range maxRank {
		spans := busSpans(g, ranks, centers, r, false, true)
		// Skip departures ride the bus tracks of their own band: endpoint
		// sharing folds them onto their siblings' row, so a node's forward fan
		// and its skips split from one `┴` origin.
		for i, e := range g.edges {
			if !isSkip(e) || ranks[e.from] != r {
				continue
			}
			endX := 1 << 29
			if edgeStraight[i] {
				endX = edgeEntryX[i]
			}
			spans = append(spans, trackSpan{
				start: min(centers[e.from], endX),
				end:   max(centers[e.from], endX),
				from:  e.from, to: e.to, edge: i,
			})
		}
		if len(spans) == 0 {
			continue
		}
		// Back-edge arrowheads sit on the first band row, back buses right under
		// it, forward buses below those: with the attach columns offset right of
		// centre, a reciprocal pair then runs as two parallel staircases whose
		// verticals fall outside each other's horizontal spans: no crossings.
		var back, fwd []trackSpan
		for _, s := range spans {
			if isAdjacentBack(ranks, g.edges[s.edge]) {
				back = append(back, s)
			} else {
				fwd = append(fwd, s)
			}
		}
		base := 0
		for _, e := range g.edges {
			if isAdjacentBack(ranks, e) && ranks[e.to] == r {
				base = 1
				break
			}
		}
		bAssigned, bCount := assignTracks(back, false)
		for _, a := range bAssigned {
			edgeBus[a[0]] = base + a[1]
		}
		fAssigned, fCount := assignTracks(fwd, false)
		for _, a := range fAssigned {
			edgeBus[a[0]] = base + bCount + a[1]
		}
		busTracks[r] = base + bCount + fCount
	}
	// A laned skip re-enters along a reserved approach row above the target's
	// rank, over the arrival heads and their labels; straight drops need none.
	entersInto := make([]bool, maxRank+1)
	for i, e := range g.edges {
		if isSkip(e) && !edgeStraight[i] {
			entersInto[ranks[e.to]] = true
		}
	}
	for r := range maxRank {
		if entersInto[r+1] {
			busTracks[r]++
		}
	}

	rankH := make([]int, len(byRank))
	for r, row := range byRank {
		if len(row) == 0 {
			rankH[r] = 3
			continue
		}
		h := 0
		for k, i := range row {
			v := sizes.boxH[i] + sizes.extraH[i]
			if k == 0 || v > h {
				h = v
			}
		}
		rankH[r] = h
	}
	// Per-end cardinalities want a row each around the verb: source card,
	// label, arrow-and-target-card.
	hasCards := false
	for _, e := range g.edges {
		if e.cardFrom != "" || e.cardTo != "" {
			hasCards = true
			break
		}
	}
	rowGap := gapY
	if hasCards {
		rowGap = max(gapY, 3)
	}
	rankY := make([]int, maxRank+1)
	for r := 1; r <= maxRank; r++ {
		rankY[r] = rankY[r-1] + rankH[r-1] + max(rowGap, busTracks[r-1]+1)
	}
	canvasH := rankY[maxRank] + rankH[maxRank]
	bandEnd := make([]int, maxRank+1)
	for r := range bandEnd {
		bandEnd[r] = rankY[r] + rankH[r]
	}
	rankStart := rankY

	// Approach rows sit just above each band's arrival-head row.
	skipApproach := filled(maxRank+1, -1)
	for r := range maxRank {
		if entersInto[r+1] {
			skipApproach[r+1] = rankY[r+1] - 2
		}
	}

	diagramW := 1
	for r, row := range byRank {
		for _, idx := range row {
			w := sizes.boxW[idx]
			h := sizes.boxH[idx]
			cx := centers[idx]
			x := satSub(cx, half(w))
			y := rankY[r] + half(rankH[r]-h-sizes.extraH[idx])
			placedNodes[idx] = placed{x: x, y: y, w: w, h: h, cx: cx, cy: y + half(h), rank: r}
			diagramW = max(diagramW, x+w)
			if sizes.extraH[idx] > 0 && sizes.selfLabelW[idx] > 0 {
				diagramW = max(diagramW, x+w+2+sizes.selfLabelW[idx])
			}
		}
	}

	contentW := diagramW
	for _, e := range g.edges {
		if e.from == e.to {
			continue
		}
		if ranks[e.to] == ranks[e.from]+1 {
			if e.label != nil {
				contentW = max(contentW, placedNodes[e.to].cx+2+min(stringWidth(*e.label), maxLabel))
			}
			if e.cardTo != "" {
				contentW = max(contentW, placedNodes[e.to].cx+2+min(stringWidth(e.cardTo), maxLabel))
			}
			if e.cardFrom != "" {
				contentW = max(contentW, placedNodes[e.from].cx+2+stringWidth(e.cardFrom))
			}
		} else if isAdjacentBack(ranks, e) {
			if text, ok := edgeText(e); ok {
				contentW = max(contentW, placedNodes[e.to].cx+2+min(stringWidth(text), maxLabel))
			}
		} else if text, ok := edgeText(e); ok {
			contentW = max(contentW, diagramW+min(stringWidth(text), maxLabel)+1)
		}
	}

	edgeLane := make([]int, len(g.edges))
	var lanes []trackSpan
	for _, s := range laneSpans(g, ranks, placedNodes, true) {
		if !edgeStraight[s.edge] {
			lanes = append(lanes, s)
		}
	}
	canvasW := contentW
	laneBase := 0
	if len(lanes) > 0 {
		assigned, count := assignTracks(lanes, true)
		for _, a := range assigned {
			edgeLane[a[0]] = a[1]
		}
		canvasW = contentW + 1 + count
		laneBase = contentW + 1
	}

	return routePlan{
		canvasW: canvasW, canvasH: canvasH, bandEnd: bandEnd, rankStart: rankStart,
		edgeBus: edgeBus, laneBase: laneBase, edgeLane: edgeLane, skipApproach: skipApproach,
		edgeEntryX: edgeEntryX, edgeStraight: edgeStraight, fwdEntryX: fwdEntryX, edgeLabelLeft: edgeLabelLeft,
	}
}

func placeLr(ranks []int, maxRank int, byRank [][]int, sizes nodeSizes, g *graph, placedNodes []placed) routePlan {
	colW := make([]int, len(byRank))
	for r, row := range byRank {
		for _, i := range row {
			colW[r] = max(colW[r], sizes.boxW[i])
		}
	}

	centers := assignPositions(byRank, sizes.layH, 1, g.edges, ranks)

	// A skip whose target entry row crosses no box on any intermediate rank runs
	// straight through the diagram into the target's left side, exiting through
	// the source's right-side fan; the bottom lane is the fallback. (No entry
	// spreading or local returns here: LR boxes are three rows tall, so the
	// centre row is the only usable port on a side.)
	isSkip := func(e edge) bool { return e.from != e.to && ranks[e.to]-ranks[e.from] > 1 }
	// A back-edge target's bottom-entry `▲` stub sits one row below its box; a
	// straight run through that cell would appear to carry the arrival.
	stubRows := map[int]bool{}
	for _, e := range g.edges {
		if e.from == e.to || ranks[e.to] >= ranks[e.from] {
			continue
		}
		t := e.to
		stubRows[satSub(centers[t], half(sizes.boxH[t]+sizes.extraH[t]))+sizes.boxH[t]] = true
	}
	edgeStraight := filled(len(g.edges), false)
	for i, e := range g.edges {
		if !isSkip(e) {
			continue
		}
		row := centers[e.to]
		straight := !stubRows[row]
		if straight {
			for j := range g.nodes {
				if ranks[j] > ranks[e.from] && ranks[j] < ranks[e.to] &&
					absInt(centers[j]-row) <= half(sizes.boxH[j]+sizes.extraH[j]) {
					straight = false
					break
				}
			}
		}
		edgeStraight[i] = straight
	}

	// Left-to-right edge labels sit in the gap after their source's column, so
	// each gap sizes to the widest label leaving through it: one long label
	// widens its own band, not the whole diagram. Straight skips label there
	// too; a self-loop's label hangs beside its own box (selfLabelW).
	bandLabel := make([]int, maxRank+1)
	for i, e := range g.edges {
		if e.from == e.to {
			continue
		}
		if ranks[e.to] != ranks[e.from]+1 && !edgeStraight[i] {
			continue
		}
		verb := 0
		if e.label != nil {
			verb = min(stringWidth(*e.label), maxLabel)
		}
		cards := 0
		if e.cardFrom != "" {
			cards += stringWidth(e.cardFrom) + 1
		}
		if e.cardTo != "" {
			cards += stringWidth(e.cardTo) + 1
		}
		bandLabel[ranks[e.from]] = max(bandLabel[ranks[e.from]], verb+cards)
	}

	edgeBus := make([]int, len(g.edges))
	busTracks := make([]int, maxRank+1)
	for r := range maxRank {
		spans := busSpans(g, ranks, centers, r, true, false)
		// Straight-skip departures ride their own band's bus tracks: endpoint
		// sharing folds them onto their siblings' column, one fan origin.
		for i, e := range g.edges {
			if !isSkip(e) || !edgeStraight[i] || ranks[e.from] != r {
				continue
			}
			spans = append(spans, trackSpan{
				start: min(centers[e.from], centers[e.to]),
				end:   max(centers[e.from], centers[e.to]),
				from:  e.from, to: e.to, edge: i,
			})
		}
		if len(spans) == 0 {
			continue
		}
		assigned, count := assignTracks(spans, false)
		for _, a := range assigned {
			edgeBus[a[0]] = a[1]
		}
		busTracks[r] = count
	}

	rankX := make([]int, maxRank+1)
	for r := 1; r <= maxRank; r++ {
		gap := max(gapX+1, bandLabel[r-1]+3, busTracks[r-1]+1)
		rankX[r] = rankX[r-1] + colW[r-1] + gap
	}
	selfTail := 0
	for _, i := range byRank[maxRank] {
		if sizes.extraH[i] > 0 && sizes.selfLabelW[i] > 0 {
			selfTail = max(selfTail, 2+sizes.selfLabelW[i])
		}
	}
	canvasW := rankX[maxRank] + colW[maxRank] + selfTail
	bandEnd := make([]int, maxRank+1)
	for r := range bandEnd {
		bandEnd[r] = rankX[r] + colW[r]
	}
	rankStart := rankX

	diagramH := 1
	for r, row := range byRank {
		x := rankX[r]
		for _, idx := range row {
			w := sizes.boxW[idx]
			h := sizes.boxH[idx]
			cy := centers[idx]
			y := satSub(cy, half(h+sizes.extraH[idx]))
			placedNodes[idx] = placed{x: x, y: y, w: w, h: h, cx: x + half(w), cy: y + half(h), rank: r}
			diagramH = max(diagramH, y+h+sizes.extraH[idx])
		}
	}

	edgeLane := make([]int, len(g.edges))
	var lanes []trackSpan
	for _, s := range laneSpans(g, ranks, placedNodes, false) {
		if !edgeStraight[s.edge] {
			lanes = append(lanes, s)
		}
	}
	canvasH := diagramH
	laneBase := 0
	if len(lanes) > 0 {
		assigned, count := assignTracks(lanes, true)
		for _, a := range assigned {
			edgeLane[a[0]] = a[1]
		}
		canvasH = diagramH + 1 + count
		laneBase = diagramH + 1
	}

	return routePlan{
		canvasW: canvasW, canvasH: canvasH, bandEnd: bandEnd, rankStart: rankStart,
		edgeBus: edgeBus, laneBase: laneBase, edgeLane: edgeLane,
		skipApproach: filled(maxRank+1, -1), edgeEntryX: filled(len(g.edges), -1),
		edgeStraight: edgeStraight, fwdEntryX: filled(len(g.nodes), -1), edgeLabelLeft: filled(len(g.edges), false),
	}
}
