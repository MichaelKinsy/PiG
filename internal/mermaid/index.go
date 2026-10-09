// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import "strings"

// Render pipeline, ported from grok-mermaid index.ts. render → attempt
// (one-line-retry for the stricter grammars) → draw (dispatch on kind).

// Render renders a Mermaid source block as Unicode box-drawing art, or ok=false
// for blank input, a syntax error, an unsupported diagram type, or a layout
// refused as too large. The diagram is laid out at whatever size it needs;
// Art.Width reports the columns it turned out to be; deciding what to do when
// that exceeds the space is the caller's (the Mermaid transformer falls back to
// the raw source, matching upstream mermaid.ts).
func Render(src string) (Art, bool) {
	src = stripControls(src)
	if trimJS(src) == "" {
		return Art{}, false
	}
	c, warnings, ok := attempt(src, wrapWidth)
	if !ok {
		return Art{}, false
	}
	plain, styled, width := c.toLines()
	if warnings == nil {
		warnings = []string{}
	}
	return Art{Plain: plain, Styled: styled, Width: width, Warnings: warnings}, true
}

// attempt draws src, retrying once without its last line if the grammar rejects
// it (keeps a streaming diagram on screen while its final line is half-typed).
func attempt(src string, wrap int) (*canvas, []string, bool) {
	if c, warnings, ok := draw(src, wrap); ok {
		return c, warnings, true
	}

	body := trimEndJS(src)
	cut := strings.LastIndex(body, "\n")
	if cut == -1 {
		return nil, nil, false
	}
	c, warnings, ok := draw(body[:cut], wrap)
	if !ok {
		return nil, nil, false
	}
	dropped := trimJS(body[cut+1:])
	warnings = append(append([]string(nil), warnings...), "dropped, unreadable final line: \""+dropped+"\"")
	return c, warnings, true
}

// draw dispatches on the declared diagram type; ok=false means nothing drawn.
func draw(src string, wrap int) (*canvas, []string, bool) {
	switch diagramKind(src) {
	case "flowchart":
		g := parseGraph(src)
		if g == nil {
			return nil, nil, false
		}
		var c *canvas
		if len(g.groups) == 0 {
			c = layoutFlowchart(g, wrap)
		} else {
			c = layoutGrouped(g, wrap)
		}
		if c == nil {
			return nil, nil, false
		}
		return c, g.warnings, true
	case "state":
		g := parseState(src)
		if g == nil {
			return nil, nil, false
		}
		return plainDraw(layoutFlowchart(g, wrap))
	case "class":
		g, infos, ok := parseClass(src)
		if !ok {
			return nil, nil, false
		}
		return plainDraw(layoutClass(g, infos, wrap))
	case "er":
		g, infos, ok := parseEr(src)
		if !ok {
			return nil, nil, false
		}
		return plainDraw(layoutClass(g, infos, wrap))
	case "sequence":
		s := parseSequence(src)
		if s == nil {
			return nil, nil, false
		}
		return plainDraw(layoutSequence(s, wrap))
	}
	return nil, nil, false
}

func plainDraw(c *canvas) (*canvas, []string, bool) {
	if c == nil {
		return nil, nil, false
	}
	return c, nil, true
}
