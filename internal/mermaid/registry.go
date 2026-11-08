// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"slices"
	"sync"
)

// The diagram registry: one entry per supported diagram type, ported from
// lovely-mermaid registry.ts.
//
// diagramKind and Render both resolve through this table, so the header test
// each parser gates on and the one diagramKind reports are the same function
// by construction.

// drawnMeta is what a diagram reports besides its canvas.
type drawnMeta struct {
	warnings  []string
	classDefs map[string]map[string]string
}

// diagram is a diagram type this renderer draws.
type diagram struct {
	kind string
	// headers are the header keywords declaring this diagram type, lowercased.
	// Matched exactly: upstream's prefix tests accepted junk like
	// `stateDiagramFoo` that mermaid proper rejects at the grammar stage.
	headers []string
	// render parses and lays out; ok=false means nothing was drawn.
	render func(src string) (*canvas, drawnMeta, bool)
}

func hasHeader(headers []string, kind string) bool { return slices.Contains(headers, kind) }

// The table is built on first use, not by the package's init: a package-level slice of
// closures keeps every parser and layout linked into programs that import the package
// and never draw a diagram. Plain variables, so nothing here runs at init.
var (
	diagramsOnce  sync.Once
	diagramsTable []*diagram
)

func diagrams() []*diagram {
	diagramsOnce.Do(func() {
		diagramsTable = []*diagram{
			flowchartDiagram(),
			stateDiagram(),
			classDiagram(),
			erDiagram(),
			sequenceDiagram(),
			pieDiagram(),
			mindmapDiagram(),
			timelineDiagram(),
			gitgraphDiagram(),
		}
	})
	return diagramsTable
}

// diagramFor is the registry entry src's header declares, or nil.
func diagramFor(src string) *diagram {
	header := headerKind(statementsOf(src))
	if header == "" {
		return nil
	}
	for _, d := range diagrams() {
		if hasHeader(d.headers, header) {
			return d
		}
	}
	return nil
}

// diagramKind is the kind of diagram src declares, or "" if its header names no
// type this renderer draws. It reads the header only: it says nothing about
// whether the body parses.
func diagramKind(src string) string {
	// The same strip Render applies, so the two entry points agree on any src.
	if d := diagramFor(stripControls(src)); d != nil {
		return d.kind
	}
	return ""
}
