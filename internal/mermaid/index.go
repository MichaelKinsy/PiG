// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import "strings"

// Render renders a Mermaid source block as Unicode box-drawing art, ported from
// lovely-mermaid index.ts render(). Supported: graph/flowchart (including
// subgraph), stateDiagram, classDiagram, erDiagram, sequenceDiagram, pie,
// mindmap, timeline and gitGraph.
//
// The diagram is laid out at whatever size it needs; Art.Width reports the
// columns it turned out to be. Deciding what to do when that exceeds the space
// is the caller's (the Mermaid transformer falls back to the raw source,
// matching upstream mermaid.ts).
//
// ok=false (upstream `null`) means there is no art to show: blank input, a
// diagram type this renderer does not draw, a source in which not one statement
// parsed, or a diagram large enough that laying it out is refused. Rendering is
// best-effort in every grammar: a statement either contributes what parsed or is
// dropped, and a diagram over a size cap renders its prefix. Everything given up
// on is listed in Art.Warnings, advisory only.
func Render(src string) (Art, bool) {
	src = stripControls(src)
	if trimJS(src) == "" {
		return Art{}, false
	}
	d := diagramFor(src)
	if d == nil {
		return Art{}, false
	}
	c, meta, ok := d.render(src)
	if !ok {
		return Art{}, false
	}
	plain, styled, width := c.toLines()

	// A frontmatter `title:` is centred above the art, in the `title` role.
	if title, ok := frontmatterTitle(src); ok {
		tw := stringWidth(title)
		width = max(width, tw)
		pad := strings.Repeat(" ", (width-tw)/2)
		plain = append([]string{pad + title, ""}, plain...)
		titleRow := []Span{{Text: title, Role: RoleTitle}}
		if pad != "" {
			titleRow = []Span{{Text: pad, Role: RoleNone}, {Text: title, Role: RoleTitle}}
		}
		styled = append([][]Span{titleRow, nil}, styled...)
	}
	warnings := meta.warnings
	if warnings == nil {
		warnings = []string{}
	}
	classDefs := meta.classDefs
	if classDefs == nil {
		classDefs = map[string]map[string]string{}
	}
	return Art{Plain: plain, Styled: styled, Width: width, ClassDefs: classDefs, Warnings: warnings}, true
}
