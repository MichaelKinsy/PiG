// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"sort"
	"strings"
)

// Canvas assembly, direction flips and subgraph scopes, ported from
// lovely-mermaid layout.ts (layoutCanvas .. buildScope).

// layoutCanvas ranks, places, draws and routes a graph onto a fresh canvas.
func layoutCanvas(g *graph, extras []nodeExtra) *canvas {
	n := len(g.nodes)
	if n == 0 {
		return nil
	}

	// Parallel edges ride the same cells, so all labels after the first were
	// silently lost: join them onto the first instead. Done before sizing so the
	// joined label gets its room.
	firstOf := map[[2]int]int{}
	for i := range g.edges {
		e := &g.edges[i]
		if e.from == e.to {
			continue
		}
		key := [2]int{e.from, e.to}
		first, seen := firstOf[key]
		if !seen {
			firstOf[key] = i
			continue
		}
		if e.label != nil {
			joined := *e.label
			if head := g.edges[first].label; head != nil {
				joined = *head + " / " + *e.label
			}
			g.edges[first].label = &joined
			e.label = nil
		}
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

	// Lane edges exit through the rank's trailing side toward the lane strip;
	// their endpoints go last within the rank, or whatever the ordering put
	// beyond them would sit in that corridor and be cut through.
	vertical := g.dir == dirDown || g.dir == dirUp
	inLane := make([]bool, n)
	for _, e := range g.edges {
		if vertical && isAdjacentBack(ranks, e) {
			continue
		}
		if e.from != e.to && ranks[e.to] != ranks[e.from]+1 {
			inLane[e.from] = true
			inLane[e.to] = true
		}
	}
	for _, row := range byRank {
		sort.SliceStable(row, func(a, b int) bool { return !inLane[row[a]] && inLane[row[b]] })
	}

	wrapped := make([][]string, n)
	for i, nd := range g.nodes {
		wrapped[i] = wrapLabel(nd.label, wrapWidth, maxLines)
	}
	widest := func(lines []string) int {
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
			boxW[i] = max(extra.sub.w+2, stringWidth(fitLabel(g.nodes[i].label, wrapWidth))+4)
			boxH[i] = extra.sub.h + 2
		case extraCompartments:
			var flat []string
			filledSections, total := 0, 0
			for _, sec := range extra.sections {
				flat = append(flat, sec...)
				if len(sec) > 0 {
					filledSections++
				}
				total += len(sec)
			}
			boxW[i] = widest(flat) + 2*pad + 2
			boxH[i] = total + satSub(filledSections, 1) + 2
		default:
			boxW[i] = widest(wrapped[i]) + 2*pad + 2
			boxH[i] = len(wrapped[i]) + 2
		}
	}

	// A self-edge needs two rows below its box, and room beside it for a label.
	extraH := make([]int, n)
	selfLabelW := make([]int, n)
	for _, e := range g.edges {
		if e.from != e.to {
			continue
		}
		extraH[e.from] = 2
		if text, ok := edgeText(e); ok {
			selfLabelW[e.from] = max(selfLabelW[e.from], min(stringWidth(text), maxLabel))
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
		c.curTag = strings.Join(g.nodes[idx].classes, " ")
		c.curHref = g.nodes[idx].href
		// `BT` mirrors the finished canvas; multi-row content draws upside down so
		// the flip restores reading order (flipHorizontal's text runs, vertically).
		mirrored := g.dir == dirUp
		switch extra.kind {
		case extraFrame:
			drawFrame(c, placedNodes[idx], g.nodes[idx].label, extra.sub, mirrored)
		case extraCompartments:
			drawClassBox(c, placedNodes[idx], extra.sections, mirrored)
		default:
			drawBox(c, placedNodes[idx], wrapped[idx], g.nodes[idx].shape, mirrored)
		}
	}
	c.curTag = ""
	c.curHref = ""

	var laneLabels []laneLabel
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
		adjacentBack := from.rank == to.rank+1
		// A back edge crosses the band below its *target's* rank.
		busRank := from.rank
		if adjacentBack {
			busRank = to.rank
		}
		bus := plan.bandEnd[busRank] + plan.edgeBus[i]
		lane := plan.laneBase + plan.edgeLane[i]
		switch {
		case vertical && adjacent:
			routeForward(c, from, to, e, bus, plan.fwdEntryX[e.to], plan.edgeLabelLeft[i])
		case vertical && adjacentBack:
			routeBackAdjacent(c, from, to, e, bus)
		case vertical && to.rank > from.rank:
			routeSkip(c, from, to, e, skipAt{
				laneX: lane, entryX: plan.edgeEntryX[i], busY: bus,
				approachY: plan.skipApproach[to.rank], straight: plan.edgeStraight[i], labelLeft: plan.edgeLabelLeft[i],
			})
		case vertical:
			routeBack(c, from, to, e, lane)
		case adjacent:
			routeForwardLr(c, from, to, e, bus)
		case to.rank > from.rank && plan.edgeStraight[i]:
			routeSkipLr(c, from, to, e, bus)
		default:
			routeBackLr(c, from, to, e, lane, &laneLabels)
		}
	}
	placeLaneLabels(c, laneLabels)

	c.finalizeMask()
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

// layoutFlowchart lays out flowchart and state diagrams: plain boxes, no extra content.
func layoutFlowchart(g *graph) *canvas {
	extras := make([]nodeExtra, len(g.nodes))
	for i := range extras {
		extras[i] = nodeExtra{kind: extraPlain}
	}
	c := layoutCanvas(g, extras)
	if c == nil {
		return nil
	}
	return orient(c, g)
}

// layoutClass lays out class and ER diagrams: boxes divided into title /
// attribute / method rows.
func layoutClass(g *graph) *canvas {
	extras := make([]nodeExtra, len(g.nodes))
	for i, nd := range g.nodes {
		sections := nd.sections
		if sections == nil {
			sections = [][]string{{nd.label}}
		}
		extras[i] = nodeExtra{kind: extraCompartments, sections: sections}
	}
	c := layoutCanvas(g, extras)
	if c == nil {
		return nil
	}
	return orient(c, g)
}

// -------------------------------------------------------------------- groups

// scopeItem is an endpoint inside a scope: a plain node or a (proxied) subgraph.
type scopeItem struct {
	group bool
	i     int
}

type scopeEdge struct {
	from, to scopeItem
	edge     int
}

const topScope = -1 // JS null: the top-level scope

// layoutGrouped lays out a flowchart that uses `subgraph`. Each subgraph
// becomes a framed box holding its own independently laid-out canvas. An edge
// is drawn in the innermost scope containing both endpoints; one crossing a
// subgraph boundary attaches to the frame instead of the node.
func layoutGrouped(g *graph) *canvas {
	// A node whose id matches a subgraph id stands in for that subgraph.
	proxy := map[int]int{}
	for gi, grp := range g.groups {
		if ni, ok := g.index[grp.id]; ok {
			proxy[ni] = gi
		}
	}

	groupChain := func(cur *int) []int {
		var chain []int
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
		item  scopeItem
		chain []int
	}
	endpoint := func(n int) endpointInfo {
		if gi, ok := proxy[n]; ok {
			return endpointInfo{scopeItem{true, gi}, groupChain(g.groups[gi].parent)}
		}
		return endpointInfo{scopeItem{false, n}, groupChain(g.nodeGroup[n])}
	}

	// Edges bucketed by the scope that draws them; topScope is the top level.
	scopeEdges := map[int][]scopeEdge{}
	referenced := make([]bool, len(g.groups))
	for ei, e := range g.edges {
		f := endpoint(e.from)
		t := endpoint(e.to)
		k := 0
		for k < len(f.chain) && k < len(t.chain) && f.chain[k] == t.chain[k] {
			k++
		}
		scope := topScope
		if k > 0 {
			scope = f.chain[k-1]
		}
		fItem, tItem := f.item, t.item
		if len(f.chain) > k {
			fItem = scopeItem{true, f.chain[k]}
		}
		if len(t.chain) > k {
			tItem = scopeItem{true, t.chain[k]}
		}
		for _, item := range [2]scopeItem{fItem, tItem} {
			if item.group {
				referenced[item.i] = true
			}
		}
		scopeEdges[scope] = append(scopeEdges[scope], scopeEdge{fItem, tItem, ei})
	}

	directNodes := map[int][]int{}
	for ni, grp := range g.nodeGroup {
		if _, ok := proxy[ni]; ok {
			continue
		}
		key := topScope
		if grp != nil {
			key = *grp
		}
		directNodes[key] = append(directNodes[key], ni)
	}

	// Drop empty subgraphs, but keep any that an edge attaches to. Walked by the
	// actual child relation: state `--` regions reparent earlier groups under
	// later ones, so index order says nothing about depth.
	childGroups := make([][]int, len(g.groups))
	for gi, grp := range g.groups {
		if grp.parent != nil {
			childGroups[*grp.parent] = append(childGroups[*grp.parent], gi)
		}
	}
	keep := make([]bool, len(g.groups))
	var visit func(gi int) bool
	visit = func(gi int) bool {
		kept := referenced[gi] || len(directNodes[gi]) > 0
		for _, c := range childGroups[gi] {
			if visit(c) {
				kept = true
			}
		}
		keep[gi] = kept
		return kept
	}
	for gi, grp := range g.groups {
		if grp.parent == nil {
			visit(gi)
		}
	}

	c := buildScope(g, topScope, scopeEdges, directNodes, keep)
	if c == nil {
		return nil
	}
	return orient(c, g)
}

func buildScope(g *graph, scope int, scopeEdges map[int][]scopeEdge, directNodes map[int][]int, keep []bool) *canvas {
	var items []scopeItem
	for _, i := range directNodes[scope] {
		items = append(items, scopeItem{false, i})
	}
	for gi, grp := range g.groups {
		parent := topScope
		if grp.parent != nil {
			parent = *grp.parent
		}
		if parent == scope && keep[gi] {
			items = append(items, scopeItem{true, gi})
		}
	}

	if len(items) == 0 {
		return newCanvas(1, 1)
	}

	nodeAt := map[int]int{}
	groupAt := map[int]int{}
	var nodes []node
	var extras []nodeExtra
	for _, item := range items {
		if item.group {
			groupAt[item.i] = len(nodes)
		} else {
			nodeAt[item.i] = len(nodes)
		}
		if !item.group {
			src := g.nodes[item.i]
			nodes = append(nodes, node{label: src.label, shape: src.shape, classes: src.classes, href: src.href})
			extras = append(extras, nodeExtra{kind: extraPlain})
		} else {
			sub := buildScope(g, item.i, scopeEdges, directNodes, keep)
			if sub == nil {
				return nil
			}
			nodes = append(nodes, node{label: g.groups[item.i].label, shape: shapeRect})
			extras = append(extras, nodeExtra{kind: extraFrame, sub: sub})
		}
	}

	var edges []edge
	for _, se := range scopeEdges[scope] {
		fm, tm := nodeAt, nodeAt
		if se.from.group {
			fm = groupAt
		}
		if se.to.group {
			tm = groupAt
		}
		fi, fok := fm[se.from.i]
		ti, tok := tm[se.to.i]
		if !fok || !tok {
			continue
		}
		e := g.edges[se.edge]
		edges = append(edges, edge{from: fi, to: ti, label: e.label, headTo: e.headTo, headFrom: e.headFrom, line: e.line})
	}

	// Layout only reads nodes/edges/dir, so a bare graph carrying those is enough.
	synth := newGraph(g.dir)
	synth.nodes = nodes
	synth.edges = edges
	return layoutCanvas(synth, extras)
}
