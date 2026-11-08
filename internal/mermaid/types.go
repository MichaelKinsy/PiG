// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

// Package mermaid renders Mermaid diagrams as Unicode box-drawing art for
// terminals. It is a faithful port of lovely-mermaid@0.3.3 (TypeScript,
// Apache-2.0, © 2023-2026 SpaceXAI, © 2026 Alexey Zaytsev), the renderer pi
// bundles, so its output matches what pi renders. Render returns the art, or
// ok=false (upstream `null`) for blank input, an unsupported diagram type, a
// source in which not one statement parsed, or a layout refused as too large.
package mermaid

// Role is the semantic role of a run of cells: what a cell is, decided by the
// renderer. Consumers map roles to a theme. Mirrors lovely-mermaid types.ts
// Role; it is distinct from Span.Classes, which is what the author assigned.
type Role string

const (
	RoleBorder    Role = "border"    // box outlines, subgraph frames, compartment rules
	RoleText      Role = "text"      // node / participant / compartment labels
	RoleEdge      Role = "edge"      // connector lines and arrowheads
	RoleEdgeLabel Role = "edgeLabel" // text sitting on an edge
	RoleTitle     Role = "title"     // the `mermaid: <kind>` header of a source box, a frontmatter title
	RoleNone      Role = "none"      // blank filler
)

// Span is a run of adjacent cells sharing one role and one set of author
// classes.
type Span struct {
	Text string
	Role Role
	// Classes are the author-assigned class names of the node these cells
	// belong to (`:::name` or `class A,B name`); nil on cells of no classed node.
	Classes []string
	// Href is the link target of the node these cells belong to (`click` /
	// `link`); nil when there is none.
	Href *string
}

// Art is a rendered diagram. Plain[i] and Styled[i] describe the same row:
// Plain is right-trimmed for display width and copy/paste, Styled keeps the run
// structure needed to colour it. Width is the widest row's display columns.
// ClassDefs are the diagram's parsed `classDef` declarations. Warnings lists
// source the grammar dropped and any size-cap truncation (advisory).
type Art struct {
	Plain     []string
	Styled    [][]Span
	Width     int
	ClassDefs map[string]map[string]string
	Warnings  []string
}
