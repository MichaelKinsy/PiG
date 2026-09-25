// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

// The shared diagram model, ported from grok-mermaid graph.ts. Flowchart, state,
// class and ER sources all parse into a Graph; only sequence diagrams differ.

// Caps that keep layout bounded; exceeding one drops the diagram to fallback.
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
}

type edge struct {
	from     int
	to       int
	label    *string // nil = no label
	headTo   head
	headFrom head
	line     lineKind
}

type group struct {
	id     string
	label  string
	parent *int // nil = top level
}

// classInfo is extra compartment content for class and ER boxes.
type classInfo struct {
	annotation *string
	attrs      []string
	methods    []string
}

func emptyClassInfo() classInfo { return classInfo{} }

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
	overCap   bool // set when a cap was hit; the caller abandons the parse
	warnings  []string
	dir       dir
}

func newGraph(d dir) *graph {
	return &graph{index: map[string]int{}, dir: d}
}

// nodeIndex returns the index of id, creating the node if new. A later
// declaration carrying a label overwrites the placeholder an edge created.
// Returns (-1, false) once maxNodes is reached, which aborts the parse.
func (g *graph) nodeIndex(id string, label *string, sh shape) (int, bool) {
	if existing, ok := g.index[id]; ok {
		if label != nil {
			g.nodes[existing].label = *label
			g.nodes[existing].shape = sh
		}
		return existing, true
	}
	if len(g.nodes) >= maxNodes {
		g.overCap = true
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

// pushEdge appends an edge, or flags overCap when maxEdges is reached.
func (g *graph) pushEdge(e edge) bool {
	if len(g.edges) >= maxEdges {
		g.overCap = true
		return false
	}
	g.edges = append(g.edges, e)
	return true
}
