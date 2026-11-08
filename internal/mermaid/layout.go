// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"math"
	"slices"
	"sort"
)

// Graph layout, ported from lovely-mermaid layout.ts: rank, order, place, route,
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
	canvasW int
	canvasH int
	bandEnd []int // coordinate just past each rank's boxes, where bus rows begin
	// rankStart is the coordinate where each rank's boxes begin: no box sits before it.
	rankStart []int
	edgeBus   []int // bus track offset per edge
	laneBase  int   // coordinate of the first lane track
	edgeLane  []int // lane track offset per edge
	// skipApproach is the reserved corridor row above rank r for laned skip approaches, or -1.
	skipApproach []int
	// edgeEntryX is, for skip edges, the column entering the target's top, or -1.
	edgeEntryX []int
	// edgeStraight marks skip edges whose entry column is box-free: drop straight and skip the lane.
	edgeStraight []bool
	// fwdEntryX is, per node, the forward cluster's entry column, or -1 for the centre.
	fwdEntryX []int
	// edgeLabelLeft renders the label left of the arrowhead instead of right, per edge.
	edgeLabelLeft []bool
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
