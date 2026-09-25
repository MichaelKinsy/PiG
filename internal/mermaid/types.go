// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

// Package mermaid renders Mermaid diagrams as Unicode box-drawing art for
// terminals. It is a faithful port of pi's bundled grok-mermaid@0.2.2
// (TypeScript, Apache-2.0, © 2023-2026 SpaceXAI, © 2026 Alexey Zaytsev), so its
// output matches what pi renders. Render returns the art, or ok=false (upstream
// `null`) for blank input, a syntax error, an unsupported diagram type, or a
// layout refused as too large.
package mermaid

// Cls is the semantic class of a run of cells; consumers map these to a theme.
// Mirrors grok-mermaid types.ts Cls.
type Cls string

const (
	ClsBorder    Cls = "border"    // box outlines, subgraph frames, compartment rules
	ClsText      Cls = "text"      // node / participant / compartment labels
	ClsEdge      Cls = "edge"      // connector lines and arrowheads
	ClsEdgeLabel Cls = "edgeLabel" // text sitting on an edge
	ClsTitle     Cls = "title"     // the `mermaid: <kind>` header of a source box
	ClsNone      Cls = "none"      // blank filler
)

// Span is a run of adjacent cells sharing one semantic class.
type Span struct {
	Text string
	Cls  Cls
}

// Art is a rendered diagram. plain[i] and styled[i] describe the same row:
// plain is right-trimmed for display width and copy/paste, styled keeps the run
// structure needed to colour it. Width is the widest row's display columns.
// Warnings lists source the flowchart grammar dropped (advisory).
type Art struct {
	Plain    []string
	Styled   [][]Span
	Width    int
	Warnings []string
	// splitWord records that a label was broken inside a word to fit its box.
	// RenderWithin refuses such a layout: a grid of sliced fragments is less
	// readable than the source it would replace.
	splitWord bool
}
