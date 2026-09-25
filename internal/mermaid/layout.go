// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"math"
	"slices"
	"sort"
	"strconv"
)

// Graph layout, ported from grok-mermaid layout.ts: rank, order, place, route,
// draw (Sugiyama). BT/RL reuse TD/LR and flip the finished canvas so text is
// never mirrored.

const (
	pad            = 1       // cells of padding between a box border and its text
	gapX           = 3       // minimum horizontal space between boxes
	gapY           = 2       // minimum vertical space between boxes
	maxCanvasCells = 1 << 21 // refuse to allocate a canvas larger than this
)

func satSub(a, b int) int {
	if a > b {
		return a - b
	}
	return 0
}

func half(n int) int { return int(math.Floor(float64(n) / 2)) }

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func maxIntsOr(xs []int, def int) int {
	if len(xs) == 0 {
		return def
	}
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

type placed struct {
	x, y, w, h, cx, cy, rank int
}

// nodeSizes holds per-node dimensions; lay* include room for self-edge loops.
type nodeSizes struct {
	boxW, boxH, layW, layH, extraH, selfLabelW []int
}

type extraKind string

const (
	extraPlain        extraKind = "plain"
	extraFrame        extraKind = "frame"
	extraCompartments extraKind = "compartments"
)

type nodeExtra struct {
	kind     extraKind
	sub      *canvas
	sections [][]string
}

type routePlan struct {
	canvasW  int
	canvasH  int
	bandEnd  []int // coordinate just past each rank's boxes, where bus rows begin
	edgeBus  []int // bus track offset per edge
	laneBase int   // coordinate of the first lane track
	edgeLane []int // lane track offset per edge
}

// ------------------------------------------------------------------ ranking

// computeRanks does longest-path ranking over the graph's DAG; back edges are
// excluded by a DFS colouring pass.
func computeRanks(g *graph) []int {
	n := len(g.nodes)
	children := make([][]int, n)
	indeg := make([]int, n)
	for _, e := range g.edges {
		if e.from != e.to {
			children[e.from] = append(children[e.from], e.to)
			indeg[e.to]++
		}
	}

	color := make([]byte, n)
	dag := make([][]int, n)
	var order []int

	var starts []int
	for i := range n {
		if indeg[i] == 0 {
			starts = append(starts, i)
		}
	}
	for i := range n {
		starts = append(starts, i)
	}
	for _, start := range starts {
		if color[start] == 0 {
			dfsDag(start, children, color, dag, &order)
		}
	}

	rank := make([]int, n)
	for _, u := range slices.Backward(order) {

		for _, v := range dag[u] {
			rank[v] = max(rank[v], rank[u]+1)
		}
	}
	return rank
}

type dfsFrame struct{ u, i int }

// dfsDag is an iterative DFS recording postorder and skipping edges back into
// the stack.
func dfsDag(start int, children [][]int, color []byte, dag [][]int, order *[]int) {
	stack := []dfsFrame{{u: start, i: 0}}
	color[start] = 1
	for len(stack) > 0 {
		frame := &stack[len(stack)-1]
		u := frame.u
		if frame.i < len(children[u]) {
			v := children[u][frame.i]
			frame.i++
			if color[v] == 1 {
				continue // grey: back edge, ignore
			}
			dag[u] = append(dag[u], v)
			if color[v] == 0 {
				color[v] = 1
				stack = append(stack, dfsFrame{u: v, i: 0})
			}
		} else {
			color[u] = 2
			*order = append(*order, u)
			stack = stack[:len(stack)-1]
		}
	}
}

// orderRanks reorders nodes within each rank to minimise edge crossings via
// barycenter sweeps.
func orderRanks(byRank [][]int, edges []edge, ranks []int) {
	n := len(ranks)
	if len(byRank) < 2 || n < 3 {
		return
	}

	parents := make([][]int, n)
	children := make([][]int, n)
	for _, e := range edges {
		if e.from != e.to && ranks[e.to] > ranks[e.from] {
			parents[e.to] = append(parents[e.to], e.from)
			children[e.from] = append(children[e.from], e.to)
		}
	}

	pos := make([]int, n)
	reindex := func(row []int) {
		for i, v := range row {
			pos[v] = i
		}
	}
	for _, row := range byRank {
		reindex(row)
	}

	best := copyRows(byRank)
	bestCrossings := countCrossings(edges, ranks, pos)
	if bestCrossings == 0 {
		return
	}

	for it := range 8 {
		var rows [][]int
		var neigh [][]int
		if it%2 == 0 {
			rows = byRank[1:]
			neigh = parents
		} else {
			rows = reversedRows(byRank[:len(byRank)-1])
			neigh = children
		}
		for _, row := range rows {
			sortByBarycenter(row, neigh, pos)
			reindex(row)
		}
		crossings := countCrossings(edges, ranks, pos)
		if crossings < bestCrossings {
			bestCrossings = crossings
			best = copyRows(byRank)
		}
		if bestCrossings == 0 {
			break
		}
	}

	for i := range byRank {
		copy(byRank[i], best[i])
	}
}

func copyRows(rows [][]int) [][]int {
	out := make([][]int, len(rows))
	for i, r := range rows {
		out[i] = append([]int(nil), r...)
	}
	return out
}

// reversedRows returns the outer slice in reverse order, sharing inner slices.
func reversedRows(rows [][]int) [][]int {
	out := make([][]int, len(rows))
	for i, r := range rows {
		out[len(rows)-1-i] = r
	}
	return out
}

func sortByBarycenter(row []int, neigh [][]int, pos []int) {
	keys := make([]float64, len(row))
	for i, v := range row {
		if len(neigh[v]) == 0 {
			keys[i] = float64(pos[v])
		} else {
			s := 0
			for _, u := range neigh[v] {
				s += pos[u]
			}
			keys[i] = float64(s) / float64(len(neigh[v]))
		}
	}
	idx := make([]int, len(row))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return keys[idx[a]] < keys[idx[b]] })
	newRow := make([]int, len(row))
	for i, j := range idx {
		newRow[i] = row[j]
	}
	copy(row, newRow)
}

func countCrossings(edges []edge, ranks, pos []int) int {
	var adjacent [][3]int
	for _, e := range edges {
		if e.from != e.to && ranks[e.to] == ranks[e.from]+1 {
			adjacent = append(adjacent, [3]int{ranks[e.from], pos[e.from], pos[e.to]})
		}
	}
	crossings := 0
	for i := 0; i < len(adjacent); i++ {
		a := adjacent[i]
		for j := i + 1; j < len(adjacent); j++ {
			b := adjacent[j]
			if a[0] == b[0] && ((a[1] < b[1] && a[2] > b[2]) || (a[1] > b[1] && a[2] < b[2])) {
				crossings++
			}
		}
	}
	return crossings
}

// assignPositions assigns a cross-axis centre to every node.
func assignPositions(byRank [][]int, size []int, sep int, edges []edge, ranks []int) []int {
	n := len(size)
	parents := make([][]int, n)
	children := make([][]int, n)
	for _, e := range edges {
		if e.from != e.to && ranks[e.to] > ranks[e.from] {
			parents[e.to] = append(parents[e.to], e.from)
			children[e.from] = append(children[e.from], e.to)
		}
	}

	pos := make([]float64, n)
	for _, row := range byRank {
		x := 0.0
		for _, v := range row {
			h := float64(size[v]) / 2
			x += h
			pos[v] = x
			x += h + float64(sep)
		}
	}

	for it := range 10 {
		var rows [][]int
		var neigh [][]int
		if it%2 == 0 {
			rows = byRank
			neigh = parents
		} else {
			rows = reversedRows(byRank)
			neigh = children
		}
		for _, row := range rows {
			relaxRank(row, neigh, pos, size, sep)
		}
	}

	minLeft := math.Inf(1)
	for v := range n {
		minLeft = math.Min(minLeft, pos[v]-float64(size[v])/2)
	}
	if math.IsInf(minLeft, 0) {
		minLeft = 0
	}
	out := make([]int, n)
	for v := range n {
		out[v] = max(0, int(math.Round(pos[v]-minLeft)))
	}
	return out
}

func relaxRank(nodes []int, neigh [][]int, pos []float64, size []int, sep int) {
	n := len(nodes)
	if n == 0 {
		return
	}

	desired := make([]float64, n)
	for i, v := range nodes {
		if len(neigh[v]) == 0 {
			desired[i] = pos[v]
		} else {
			s := 0.0
			for _, u := range neigh[v] {
				s += pos[u]
			}
			desired[i] = s / float64(len(neigh[v]))
		}
	}
	halfOf := func(i int) float64 { return float64(size[nodes[i]]) / 2 }

	left := make([]float64, n)
	for i := range n {
		if i == 0 {
			left[i] = desired[i]
		} else {
			left[i] = math.Max(desired[i], left[i-1]+halfOf(i-1)+float64(sep)+halfOf(i))
		}
	}
	right := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		if i == n-1 {
			right[i] = desired[i]
		} else {
			right[i] = math.Min(desired[i], right[i+1]-halfOf(i+1)-float64(sep)-halfOf(i))
		}
	}
	for i := range n {
		pos[nodes[i]] = (left[i] + right[i]) / 2
	}
	for i := 1; i < n; i++ {
		minP := pos[nodes[i-1]] + halfOf(i-1) + float64(sep) + halfOf(i)
		if pos[nodes[i]] < minP {
			pos[nodes[i]] = minP
		}
	}
}

// ------------------------------------------------------------------- tracks

// span5 is [start, end, from, to, edgeIndex].
type span5 = [5]int

// assignTracks packs spans into as few parallel tracks as possible.
func assignTracks(spans []span5) (assigned [][2]int, count int) {
	sorted := append([]span5(nil), spans...)
	sort.SliceStable(sorted, func(a, b int) bool {
		for i := range 5 {
			if sorted[a][i] != sorted[b][i] {
				return sorted[a][i] < sorted[b][i]
			}
		}
		return false
	})
	var tracks [][][4]int
	for _, sp := range sorted {
		s, e, f, t, idx := sp[0], sp[1], sp[2], sp[3], sp[4]
		slot := -1
		for ti, members := range tracks {
			fits := true
			for _, m := range members {
				s2, e2, f2, t2 := m[0], m[1], m[2], m[3]
				if e2+2 > s && e+2 > s2 && f2 != f && t2 != t {
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
		tracks[slot] = append(tracks[slot], [4]int{s, e, f, t})
		assigned = append(assigned, [2]int{idx, slot})
	}
	return assigned, len(tracks)
}

func busSpans(g *graph, ranks, centers []int, r int, exact bool) []span5 {
	var out []span5
	for i, e := range g.edges {
		var jogs bool
		if exact {
			jogs = centers[e.from] != centers[e.to]
		} else {
			jogs = absInt(centers[e.from]-centers[e.to]) > 1
		}
		if e.from != e.to && ranks[e.from] == r && ranks[e.to] == r+1 && jogs {
			out = append(out, span5{
				min(centers[e.from], centers[e.to]),
				max(centers[e.from], centers[e.to]),
				e.from, e.to, i,
			})
		}
	}
	return out
}

func laneSpans(g *graph, ranks []int, placedNodes []placed, vertical bool) []span5 {
	var out []span5
	for i, e := range g.edges {
		if e.from == e.to || ranks[e.to] == ranks[e.from]+1 {
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
		out = append(out, span5{a, b, e.from, e.to, i})
	}
	return out
}

// ----------------------------------------------------------------- placement

func placeTd(ranks []int, maxRank int, byRank [][]int, sizes nodeSizes, g *graph, placedNodes []placed) routePlan {
	centers := assignPositions(byRank, sizes.layW, gapX, g.edges, ranks)

	edgeBus := make([]int, len(g.edges))
	busTracks := make([]int, maxRank+1)
	for r := range maxRank {
		spans := busSpans(g, ranks, centers, r, false)
		if len(spans) == 0 {
			continue
		}
		assigned, count := assignTracks(spans)
		for _, a := range assigned {
			edgeBus[a[0]] = a[1]
		}
		busTracks[r] = count
	}

	rankH := make([]int, len(byRank))
	for r, row := range byRank {
		if len(row) == 0 {
			rankH[r] = 3
		} else {
			vals := make([]int, len(row))
			for k, i := range row {
				vals[k] = sizes.boxH[i] + sizes.extraH[i]
			}
			rankH[r] = maxIntsOr(vals, 0)
		}
	}
	rankY := make([]int, maxRank+1)
	for r := 1; r <= maxRank; r++ {
		rankY[r] = rankY[r-1] + rankH[r-1] + max(gapY, busTracks[r-1]+1)
	}
	canvasH := rankY[maxRank] + rankH[maxRank]
	bandEnd := make([]int, maxRank+1)
	for r := 0; r <= maxRank; r++ {
		bandEnd[r] = rankY[r] + rankH[r]
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
		if e.from == e.to || e.label == nil {
			continue
		}
		lw := min(stringWidth(*e.label), maxLabel)
		if ranks[e.to] == ranks[e.from]+1 {
			contentW = max(contentW, placedNodes[e.to].cx+2+lw)
		} else {
			contentW = max(contentW, diagramW+lw+1)
		}
	}

	edgeLane := make([]int, len(g.edges))
	lanes := laneSpans(g, ranks, placedNodes, true)
	canvasW := contentW
	laneBase := 0
	if len(lanes) > 0 {
		assigned, count := assignTracks(lanes)
		for _, a := range assigned {
			edgeLane[a[0]] = a[1]
		}
		canvasW = contentW + 1 + count
		laneBase = contentW + 1
	}

	return routePlan{canvasW: canvasW, canvasH: canvasH, bandEnd: bandEnd, edgeBus: edgeBus, laneBase: laneBase, edgeLane: edgeLane}
}

func placeLr(ranks []int, maxRank int, byRank [][]int, sizes nodeSizes, g *graph, placedNodes []placed) routePlan {
	colW := make([]int, len(byRank))
	for r, row := range byRank {
		if len(row) == 0 {
			colW[r] = 0
		} else {
			vals := make([]int, len(row))
			for k, i := range row {
				vals[k] = sizes.boxW[i]
			}
			colW[r] = maxIntsOr(vals, 0)
		}
	}

	var labelWidths []int
	for _, e := range g.edges {
		if e.from != e.to && ranks[e.to] != ranks[e.from]+1 {
			continue
		}
		if e.label == nil {
			continue
		}
		labelWidths = append(labelWidths, min(stringWidth(*e.label), maxLabel))
	}
	maxLabelW := maxIntsOr(labelWidths, 0)
	baseGap := max(gapX+1, maxLabelW+3)

	centers := assignPositions(byRank, sizes.layH, 1, g.edges, ranks)

	edgeBus := make([]int, len(g.edges))
	busTracks := make([]int, maxRank+1)
	for r := range maxRank {
		spans := busSpans(g, ranks, centers, r, true)
		if len(spans) == 0 {
			continue
		}
		assigned, count := assignTracks(spans)
		for _, a := range assigned {
			edgeBus[a[0]] = a[1]
		}
		busTracks[r] = count
	}

	rankX := make([]int, maxRank+1)
	for r := 1; r <= maxRank; r++ {
		rankX[r] = rankX[r-1] + colW[r-1] + max(baseGap, busTracks[r-1]+1)
	}
	var selfTails []int
	for _, i := range byRank[maxRank] {
		if sizes.extraH[i] > 0 && sizes.selfLabelW[i] > 0 {
			selfTails = append(selfTails, 2+sizes.selfLabelW[i])
		}
	}
	canvasW := rankX[maxRank] + colW[maxRank] + maxIntsOr(selfTails, 0)
	bandEnd := make([]int, maxRank+1)
	for r := 0; r <= maxRank; r++ {
		bandEnd[r] = rankX[r] + colW[r]
	}

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
	lanes := laneSpans(g, ranks, placedNodes, false)
	canvasH := diagramH
	laneBase := 0
	if len(lanes) > 0 {
		assigned, count := assignTracks(lanes)
		for _, a := range assigned {
			edgeLane[a[0]] = a[1]
		}
		canvasH = diagramH + 1 + count
		laneBase = diagramH + 1
	}

	return routePlan{canvasW: canvasW, canvasH: canvasH, bandEnd: bandEnd, edgeBus: edgeBus, laneBase: laneBase, edgeLane: edgeLane}
}

// -------------------------------------------------------------------- canvas

// layoutCanvas ranks, places, draws and routes a graph onto a fresh canvas.
func layoutCanvas(g *graph, extras []nodeExtra, wrap int) *canvas {
	n := len(g.nodes)
	if n == 0 {
		return nil
	}

	ranks := computeRanks(g)
	maxRank := 0
	for _, r := range ranks {
		maxRank = max(maxRank, r)
	}

	byRank := make([][]int, maxRank+1)
	for idx := range ranks {
		byRank[ranks[idx]] = append(byRank[ranks[idx]], idx)
	}
	orderRanks(byRank, g.edges, ranks)

	wrapped := make([][]string, n)
	var splitAnyWord bool
	for i, nd := range g.nodes {
		var splitWord bool
		wrapped[i], splitWord = wrapLabel(nd.label, wrap, maxLinesFor(wrap))
		splitAnyWord = splitAnyWord || splitWord
	}
	widest := func(lines []string) int {
		if len(lines) == 0 {
			return 1
		}
		w := 1
		for _, l := range lines {
			w = max(w, stringWidth(l))
		}
		return w
	}

	boxW := make([]int, n)
	boxH := make([]int, n)
	for i, extra := range extras {
		switch extra.kind {
		case extraFrame:
			boxW[i] = max(extra.sub.w+2, frameTitleWidth(g.nodes[i].label, wrap)+4)
			boxH[i] = extra.sub.h + 2
		case extraCompartments:
			var flat []string
			for _, sec := range extra.sections {
				flat = append(flat, sec...)
			}
			boxW[i] = widest(flat) + 2*pad + 2
			filled := 0
			total := 0
			for _, sec := range extra.sections {
				if len(sec) > 0 {
					filled++
				}
				total += len(sec)
			}
			boxH[i] = total + satSub(filled, 1) + 2
		default:
			boxW[i] = widest(wrapped[i]) + 2*pad + 2
			boxH[i] = len(wrapped[i]) + 2
		}
	}

	extraH := make([]int, n)
	selfLabelW := make([]int, n)
	for _, e := range g.edges {
		if e.from != e.to {
			continue
		}
		extraH[e.from] = 2
		if e.label != nil {
			selfLabelW[e.from] = max(selfLabelW[e.from], min(stringWidth(*e.label), maxLabel))
		}
	}
	for i := range n {
		if extraH[i] > 0 {
			boxW[i] = max(boxW[i], 7)
		}
	}

	layW := make([]int, n)
	layH := make([]int, n)
	for i := range n {
		layW[i] = boxW[i]
		if selfLabelW[i] > 0 {
			layW[i] += 2 * (selfLabelW[i] + 3)
		}
		layH[i] = boxH[i] + extraH[i]
	}
	sizes := nodeSizes{boxW: boxW, boxH: boxH, layW: layW, layH: layH, extraH: extraH, selfLabelW: selfLabelW}

	placedNodes := make([]placed, n)

	vertical := g.dir == dirDown || g.dir == dirUp
	var plan routePlan
	if vertical {
		plan = placeTd(ranks, maxRank, byRank, sizes, g, placedNodes)
	} else {
		plan = placeLr(ranks, maxRank, byRank, sizes, g, placedNodes)
	}

	if plan.canvasW*plan.canvasH > maxCanvasCells {
		return nil
	}

	c := newCanvas(plan.canvasW, plan.canvasH)
	for idx := range n {
		extra := extras[idx]
		switch extra.kind {
		case extraFrame:
			drawFrame(c, placedNodes[idx], g.nodes[idx].label, extra.sub)
		case extraCompartments:
			drawClassBox(c, placedNodes[idx], extra.sections)
		default:
			drawBox(c, placedNodes[idx], wrapped[idx], g.nodes[idx].shape)
		}
	}

	for i, e := range g.edges {
		switch e.line {
		case lineDotted:
			c.curStyle = styDot
		case lineThick:
			c.curStyle = styThick
		default:
			c.curStyle = stySolid
		}
		if e.from == e.to {
			routeSelf(c, placedNodes[e.from], e)
			continue
		}
		from := placedNodes[e.from]
		to := placedNodes[e.to]
		adjacent := to.rank == from.rank+1
		bus := plan.bandEnd[from.rank] + plan.edgeBus[i]
		lane := plan.laneBase + plan.edgeLane[i]
		switch {
		case vertical && adjacent:
			routeForward(c, from, to, e, bus)
		case vertical:
			routeBack(c, from, to, e, lane)
		case adjacent:
			routeForwardLr(c, from, to, e, bus)
		default:
			routeBackLr(c, from, to, e, lane)
		}
	}

	c.finalizeMask()
	c.splitWord = c.splitWord || splitAnyWord
	return c
}

// orient applies the direction flip a finished canvas needs for BT / RL.
func orient(c *canvas, g *graph) *canvas {
	switch g.dir {
	case dirUp:
		c.flipVertical()
	case dirLeft:
		c.flipHorizontal()
	}
	return c
}

// layoutFlowchart lays out flowchart and state diagrams: plain boxes.
func layoutFlowchart(g *graph, wrap int) *canvas {
	extras := make([]nodeExtra, len(g.nodes))
	for i := range extras {
		extras[i] = nodeExtra{kind: extraPlain}
	}
	c := layoutCanvas(g, extras, wrap)
	if c == nil {
		return nil
	}
	return orient(c, g)
}

// layoutClass lays out class and ER diagrams: boxes divided into compartments.
func layoutClass(g *graph, infos []classInfo, wrap int) *canvas {
	extras := make([]nodeExtra, len(g.nodes))
	for i, nd := range g.nodes {
		var title []string
		if infos[i].annotation != nil {
			title = append(title, "«"+*infos[i].annotation+"»")
		}
		title = append(title, layoutDisplayGenerics(nd.label))
		extras[i] = nodeExtra{kind: extraCompartments, sections: [][]string{title, infos[i].attrs, infos[i].methods}}
	}
	c := layoutCanvas(g, extras, wrap)
	if c == nil {
		return nil
	}
	return orient(c, g)
}

func layoutDisplayGenerics(s string) string {
	var out []rune
	open := false
	for _, c := range s {
		if c == '~' {
			if open {
				out = append(out, '>')
			} else {
				out = append(out, '<')
			}
			open = !open
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}

// -------------------------------------------------------------------- groups

func nodeKey(i int) string  { return "n" + strconv.Itoa(i) }
func groupKey(i int) string { return "g" + strconv.Itoa(i) }

type scopeEdge struct {
	f, t string
	ei   int
}

// layoutGrouped lays out a flowchart that uses subgraph.
func layoutGrouped(g *graph, wrap int) *canvas {
	proxy := map[int]int{} // node index -> group index it stands in for
	for gi, gr := range g.groups {
		if ni, ok := g.index[gr.id]; ok {
			proxy[ni] = gi
		}
	}

	groupChain := func(start *int) []int {
		var chain []int
		cur := start
		for cur != nil {
			chain = append(chain, *cur)
			cur = g.groups[*cur].parent
		}
		for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
			chain[i], chain[j] = chain[j], chain[i]
		}
		return chain
	}
	type endpointInfo struct {
		key   string
		chain []int
	}
	endpoint := func(n int) endpointInfo {
		if gi, ok := proxy[n]; ok {
			return endpointInfo{key: groupKey(gi), chain: groupChain(g.groups[gi].parent)}
		}
		return endpointInfo{key: nodeKey(n), chain: groupChain(g.nodeGroup[n])}
	}

	scopeEdges := map[int][]scopeEdge{} // -1 = top level
	referenced := make([]bool, len(g.groups))
	for ei, e := range g.edges {
		f := endpoint(e.from)
		t := endpoint(e.to)
		k := 0
		for k < len(f.chain) && k < len(t.chain) && f.chain[k] == t.chain[k] {
			k++
		}
		scope := -1
		if k != 0 {
			scope = f.chain[k-1]
		}
		fKey := f.key
		if len(f.chain) > k {
			fKey = groupKey(f.chain[k])
		}
		tKey := t.key
		if len(t.chain) > k {
			tKey = groupKey(t.chain[k])
		}
		for _, key := range []string{fKey, tKey} {
			if key[0] == 'g' {
				gi, _ := strconv.Atoi(key[1:])
				referenced[gi] = true
			}
		}
		scopeEdges[scope] = append(scopeEdges[scope], scopeEdge{f: fKey, t: tKey, ei: ei})
	}

	directNodes := map[int][]int{}
	for ni, grp := range g.nodeGroup {
		if _, ok := proxy[ni]; ok {
			continue
		}
		key := -1
		if grp != nil {
			key = *grp
		}
		directNodes[key] = append(directNodes[key], ni)
	}

	keep := make([]bool, len(g.groups))
	for gi := len(g.groups) - 1; gi >= 0; gi-- {
		hasNodes := len(directNodes[gi]) > 0
		hasChildren := false
		for c, gr := range g.groups {
			if parentEq(gr.parent, gi) && keep[c] {
				hasChildren = true
				break
			}
		}
		keep[gi] = hasNodes || hasChildren || referenced[gi]
	}

	c := buildScope(g, -1, scopeEdges, directNodes, keep, wrap)
	if c == nil {
		return nil
	}
	return orient(c, g)
}

func parentEq(p *int, scope int) bool {
	if p == nil {
		return scope == -1
	}
	return *p == scope
}

func buildScope(g *graph, scope int, scopeEdges map[int][]scopeEdge, directNodes map[int][]int, keep []bool, wrap int) *canvas {
	var items []string
	for _, ni := range directNodes[scope] {
		items = append(items, nodeKey(ni))
	}
	for gi := range g.groups {
		if parentEq(g.groups[gi].parent, scope) && keep[gi] {
			items = append(items, groupKey(gi))
		}
	}

	if len(items) == 0 {
		return newCanvas(1, 1)
	}

	indexOf := map[string]int{}
	var nodes []node
	var extras []nodeExtra
	for _, item := range items {
		indexOf[item] = len(nodes)
		i, _ := strconv.Atoi(item[1:])
		if item[0] == 'n' {
			nodes = append(nodes, node{label: g.nodes[i].label, shape: g.nodes[i].shape})
			extras = append(extras, nodeExtra{kind: extraPlain})
		} else {
			sub := buildScope(g, i, scopeEdges, directNodes, keep, wrap)
			if sub == nil {
				return nil
			}
			nodes = append(nodes, node{label: g.groups[i].label, shape: shapeRect})
			extras = append(extras, nodeExtra{kind: extraFrame, sub: sub})
		}
	}

	var edges []edge
	for _, se := range scopeEdges[scope] {
		fi, okF := indexOf[se.f]
		ti, okT := indexOf[se.t]
		if !okF || !okT {
			continue
		}
		e := g.edges[se.ei]
		edges = append(edges, edge{from: fi, to: ti, label: e.label, headTo: e.headTo, headFrom: e.headFrom, line: e.line})
	}

	synth := &graph{nodes: nodes, edges: edges, dir: g.dir, index: map[string]int{}}
	return layoutCanvas(synth, extras, wrap)
}

// ------------------------------------------------------------------- drawing

func drawBox(c *canvas, p placed, lines []string, sh shape) {
	x, y, w, h := p.x, p.y, p.w, p.h
	right := x + w - 1
	bottom := y + h - 1

	rounded := sh == shapeRound || sh == shapeDiamond
	tl, tr, bl, br := "┌", "┐", "└", "┘"
	if rounded {
		tl, tr, bl, br = "╭", "╮", "╰", "╯"
	}
	c.set(x, y, tl, ClsBorder)
	c.set(right, y, tr, ClsBorder)
	c.set(x, bottom, bl, ClsBorder)
	c.set(right, bottom, br, ClsBorder)

	for cx := x + 1; cx < right; cx++ {
		c.addBits(cx, y, bitL|bitR, ClsBorder)
		c.addBits(cx, bottom, bitL|bitR, ClsBorder)
	}
	for cy := y + 1; cy < bottom; cy++ {
		c.addBits(x, cy, bitU|bitD, ClsBorder)
		c.addBits(right, cy, bitU|bitD, ClsBorder)
	}

	for cy := y; cy <= bottom; cy++ {
		for cx := x; cx <= right; cx++ {
			c.occupied[c.idx(cx, cy)] = 1
		}
	}

	inner := max(1, satSub(w, 2*pad+2))
	for li, line := range lines {
		text := fitLabel(line, inner)
		textX := x + 1 + pad + half(satSub(inner, stringWidth(text)))
		drawText(c, text, textX, y+1+li, ClsText)
	}
}

// drawClassBox draws a class or ER box: sections separated by rules, title centred.
func drawClassBox(c *canvas, p placed, sections [][]string) {
	drawBox(c, p, nil, shapeRect)
	inner := max(1, satSub(p.w, 2*pad+2))
	row := p.y + 1
	first := true
	for si, section := range sections {
		if len(section) == 0 {
			continue
		}
		if !first {
			c.set(p.x, row, "├", ClsBorder)
			for x := p.x + 1; x < p.x+p.w-1; x++ {
				c.set(x, row, "─", ClsBorder)
			}
			c.set(p.x+p.w-1, row, "┤", ClsBorder)
			row++
		}
		first = false
		for _, line := range section {
			text := fitLabel(line, inner)
			tx := p.x + 1 + pad
			if si == 0 {
				tx = p.x + 1 + pad + half(satSub(inner, stringWidth(text)))
			}
			drawTextOverEdges(c, text, tx, row, ClsText)
			row++
		}
	}
}

// drawFrame draws a subgraph frame: a titled box with a sub-canvas centred inside.
func drawFrame(c *canvas, p placed, title string, sub *canvas) {
	drawBox(c, p, nil, shapeRect)
	t := fitLabel(title, satSub(p.w, 4))
	drawTextOverEdges(c, " "+t+" ", p.x+1, p.y, ClsText)
	c.splitWord = c.splitWord || sub.splitWord
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
	}
	return arrow
}

func routeForward(c *canvas, from, to placed, e edge, bus int) {
	tx := to.cx
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
		c.addBits(tx, headRow, bitU, ClsEdge)
	} else {
		c.set(tx, headRow, headGlyph(e.headTo, "▼"), ClsEdge)
	}
	if e.headFrom != headNone {
		c.set(bx, by, headGlyph(e.headFrom, "▲"), ClsEdge)
	}

	if e.label != nil {
		placeLabel(c, *e.label, headRow, tx+1)
	}
}

func routeSelf(c *canvas, p placed, e edge) {
	bottom := p.y + p.h - 1
	exitX := p.cx + 1
	retX := p.x + p.w - 2
	if retX <= exitX || bottom+2 >= c.h {
		return
	}

	v, hh, bl, br := "│", "─", "╰", "╯"
	switch e.line {
	case lineDotted:
		v, hh, bl, br = "╎", "╌", "╰", "╯"
	case lineThick:
		v, hh, bl, br = "┃", "━", "┗", "┛"
	}

	c.junction(exitX, bottom, bitD)
	c.set(exitX, bottom+1, v, ClsEdge)
	c.set(exitX, bottom+2, bl, ClsEdge)
	for x := exitX + 1; x < retX; x++ {
		c.set(x, bottom+2, hh, ClsEdge)
	}
	c.set(retX, bottom+2, br, ClsEdge)
	c.set(retX, bottom+1, headGlyph(e.headTo, "▲"), ClsEdge)
	if e.label != nil {
		placeLabel(c, *e.label, bottom+1, p.x+p.w+1)
	}
}

func routeBack(c *canvas, from, to placed, e edge, laneX int) {
	sx := from.x + from.w - 1
	sy := from.cy
	tx := to.x + to.w - 1
	tyc := to.cy

	c.junction(sx, sy, bitR)
	c.segH(sy, sx, laneX)
	c.segV(laneX, sy, tyc)
	c.segH(tyc, tx+1, laneX)

	if e.headTo == headNone {
		c.addBits(tx+1, tyc, bitR, ClsEdge)
	} else {
		c.set(tx+1, tyc, headGlyph(e.headTo, "◄"), ClsEdge)
	}
	if e.headFrom != headNone {
		c.set(sx, sy, headGlyph(e.headFrom, "◄"), ClsEdge)
	}

	if e.label != nil {
		placeLabel(c, *e.label, satSub(tyc, 1), satSub(laneX, stringWidth(*e.label)+1))
	}
}

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
		c.addBits(headCol, ly, bitR, ClsEdge)
	} else {
		c.set(headCol, ly, headGlyph(e.headTo, "▶"), ClsEdge)
	}
	if e.headFrom != headNone {
		c.set(rx, ry, headGlyph(e.headFrom, "◄"), ClsEdge)
	}

	if e.label != nil {
		placeLabel(c, *e.label, satSub(ly, 1), bus+1)
	}
}

func routeBackLr(c *canvas, from, to placed, e edge, laneY int) {
	sx := from.cx
	sy := from.y + from.h - 1
	tx := to.cx
	ty := to.y + to.h - 1

	c.junction(sx, sy, bitD)
	c.segV(sx, sy, laneY)
	c.segH(laneY, sx, tx)
	c.segV(tx, laneY, ty+1)

	if e.headTo == headNone {
		c.addBits(tx, ty+1, bitD, ClsEdge)
	} else {
		c.set(tx, ty+1, headGlyph(e.headTo, "▲"), ClsEdge)
	}
	if e.headFrom != headNone {
		c.set(sx, sy, headGlyph(e.headFrom, "▲"), ClsEdge)
	}

	if e.label != nil {
		placeLabel(c, *e.label, satSub(laneY, 1), half(sx+tx))
	}
}

// placeLabel writes an edge label, stopping at the first occupied cell.
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
		c.set(x, row, mc.cluster, ClsEdgeLabel)
		for k := 1; k < mc.width; k++ {
			c.set(x+k, row, cont, ClsEdgeLabel)
		}
		x += mc.width
	}
}

// frameTitleWidth returns the columns a subgraph frame must give its title.
//
// At the natural width the title is clipped to the label wrap width, matching
// grok-mermaid. While narrowing to fit an area the full title is measured
// instead, so the frame grows rather than losing the title's words; a frame that
// grows past the target is rejected by the search, which then tries another
// width. drawFrame clips to the frame it is given, so sizing the frame for the
// whole title is what keeps it whole on screen.
func frameTitleWidth(title string, wrap int) int {
	if wrap >= wrapWidth {
		return stringWidth(fitLabel(title, wrap))
	}
	return stringWidth(title)
}
