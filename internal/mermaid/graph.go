// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import "slices"

// The shared diagram model, ported from lovely-mermaid graph.ts. Flowchart, state,
// class and ER sources all parse into a Graph; the other diagram types differ.

// Caps that keep layout bounded; exceeding one truncates the diagram to its prefix.
const (
	maxNodes      = 128
	maxEdges      = 512
	maxGroups     = 24
	maxGroupDepth = 6
	maxMembers    = 8 // class members / ER attributes per box before eliding with …
)

type shape string

const (
	shapeRect    shape = "rect"
	shapeRound   shape = "round"
	shapeDiamond shape = "diamond"
)

// head is the decoration at one end of an edge.
type head string

const (
	headNone        head = "none"
	headArrow       head = "arrow"
	headCircle      head = "circle"
	headCross       head = "cross"
	headTriangle    head = "triangle"
	headDiamondFill head = "diamondFill"
	headDiamondOpen head = "diamondOpen"
)

type lineKind string

const (
	lineSolid  lineKind = "solid"
	lineDotted lineKind = "dotted"
	lineThick  lineKind = "thick"
)

type dir string

const (
	dirDown  dir = "down"
	dirUp    dir = "up"
	dirRight dir = "right"
	dirLeft  dir = "left"
)

type node struct {
	label string
	shape shape
	// sections is compartment content for class and ER boxes, pre-formatted by
	// the parser: one string per row, one slice per compartment (title,
	// attributes, methods). nil on plain nodes; layout draws it verbatim,
	// separated by horizontal rules.
	sections [][]string
	// classes are author-assigned class names (`:::name`, `class A,B name`),
	// carried out through Span.Classes.
	classes []string
	// href is a `click A "url"` / `link A "url"` target, carried out through
	// Span.Href; "" when there is none.
	href string
}

type edge struct {
	from  int
	to    int
	label *string // nil = no label
	// cardFrom and cardTo are cardinalities (ER crow's-foot, class
	// multiplicities), painted at their own end of the edge; "" when absent.
	cardFrom string
	cardTo   string
	headTo   head
	headFrom head
	line     lineKind
}

type group struct {
	id     string
	label  string
	parent *int // nil = top level
}

// parseDir maps LR/RL/BT (as written) to a direction; else down.
func parseDir(token string) dir {
	switch asciiUpper(token) {
	case "LR":
		return dirRight
	case "RL":
		return dirLeft
	case "BT":
		return dirUp
	default:
		return dirDown
	}
}

type graph struct {
	nodes     []node
	edges     []edge
	index     map[string]int
	groups    []group
	nodeGroup []*int // innermost subgraph each node was declared in, parallel to nodes
	curGroup  *int
	// truncated names the size cap hit ("" when none). The parser stops
	// consuming statements and renders the prefix, warning about the truncation.
	truncated string
	// warnings lists source the grammar could not read and dropped.
	warnings []string
	// classDefs are the parsed `classDef` declarations: name -> property map.
	classDefs map[string]map[string]string
	dir       dir
}

func newGraph(d dir) *graph {
	return &graph{index: map[string]int{}, classDefs: map[string]map[string]string{}, dir: d}
}

// nodeIndex returns the index of id, creating the node if new. A later
// declaration carrying a label overwrites the placeholder an edge created.
// Returns (-1, false) once maxNodes is reached, which truncates the parse.
func (g *graph) nodeIndex(id string, label *string, sh shape) (int, bool) {
	if existing, ok := g.index[id]; ok {
		if label != nil {
			g.nodes[existing].label = *label
			g.nodes[existing].shape = sh
		}
		return existing, true
	}
	if len(g.nodes) >= maxNodes {
		if g.truncated == "" {
			g.truncated = "node cap (" + itoa(maxNodes) + ") reached"
		}
		return -1, false
	}
	g.index[id] = len(g.nodes)
	lbl := id
	if label != nil {
		lbl = *label
	}
	g.nodes = append(g.nodes, node{label: lbl, shape: sh})
	g.nodeGroup = append(g.nodeGroup, g.curGroup)
	return len(g.nodes) - 1, true
}

// nodeLabel sets a node's label without disturbing its shape, creating it if new.
func (g *graph) nodeLabel(id, label string) (int, bool) {
	if existing, ok := g.index[id]; ok {
		g.nodes[existing].label = label
		return existing, true
	}
	return g.nodeIndex(id, &label, shapeRound)
}

// addClass attaches an author class name to a node, ignoring a repeat.
func (g *graph) addClass(idx int, name string) {
	n := &g.nodes[idx]
	if !slices.Contains(n.classes, name) {
		n.classes = append(n.classes, name)
	}
}

// classAssign is one `[ids, names]` class assignment.
type classAssign struct{ ids, names []string }

// applyClasses applies collected class assignments. Run after the statement
// walk so a `class A,B name` (or `:::` tag) may precede the nodes it names;
// unknown ids are ignored.
func (g *graph) applyClasses(assignments []classAssign) {
	for _, a := range assignments {
		for _, id := range a.ids {
			idx, ok := g.index[trimJS(id)]
			if !ok {
				continue
			}
			for _, name := range a.names {
				if t := trimJS(name); t != "" {
					g.addClass(idx, t)
				}
			}
		}
	}
}

// hrefAssign is one `[id, url]` link target.
type hrefAssign struct{ id, url string }

// applyHrefs applies link targets; the last one per id wins, unknown ids are
// ignored. Deferred like applyClasses, for the same ordering reason.
func (g *graph) applyHrefs(hrefs []hrefAssign) {
	for _, h := range hrefs {
		if idx, ok := g.index[trimJS(h.id)]; ok {
			g.nodes[idx].href = h.url
		}
	}
}

// drop records an unreadable statement; the diagram renders without it.
// Skipped once truncated: a cap-caused parse failure is not the statement's
// fault.
func (g *graph) drop(st string) {
	if g.truncated == "" {
		g.warnings = append(g.warnings, "dropped, unreadable statement: \""+st+"\"")
	}
}

// pushEdge appends an edge, or flags truncated when maxEdges is reached.
func (g *graph) pushEdge(e edge) bool {
	if len(g.edges) >= maxEdges {
		if g.truncated == "" {
			g.truncated = "edge cap (" + itoa(maxEdges) + ") reached"
		}
		return false
	}
	g.edges = append(g.edges, e)
	return true
}
