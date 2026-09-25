// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
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
	return renderAt(src, wrapWidth)
}

// RenderWithin renders src as narrow as it needs to be to fit maxWidth columns.
//
// Diagram width is driven by how wide node labels are allowed to run before they
// wrap. The natural layout is tried first and returned untouched when it fits,
// so a diagram that already fits is byte-identical to a plain Render. When it
// does not fit, the label width that produces the widest art still inside
// maxWidth is found by measuring candidate layouts, not by guessing: each
// candidate is a real layout and its real width.
//
// The narrowest result is returned when nothing fits, since a diagram has a
// structural floor below which shrinking labels no longer helps: boxes, arrows
// and parallel branches occupy columns of their own. Callers compare Art.Width
// against their own space and decide what to do, exactly as with Render.
func RenderWithin(src string, maxWidth int) (Art, bool) {
	art, ok := renderAt(src, wrapWidth)
	if !ok || maxWidth <= 0 || art.Width <= maxWidth {
		return art, ok
	}

	// Binary search the largest label width whose layout fits. Width is
	// non-decreasing in label width, and the best fitting candidate seen is
	// kept rather than assumed, so a layout that breaks that ordering costs
	// accuracy of the search and never correctness of the result.
	best, found := Art{}, false
	lo, hi := minWrapWidth, wrapWidth-1
	narrowest := art
	for lo <= hi {
		mid := (lo + hi) / 2
		candidate, candidateOK := renderAt(src, mid)
		if !candidateOK {
			break
		}
		if candidate.splitWord {
			// This width is below the diagram's longest word, so the layout
			// slices words to fit its boxes and every narrower one slices more.
			// Search wider: a grid of fragments is less readable than the source
			// it would replace.
			lo = mid + 1
			continue
		}
		narrowest = candidate
		if candidate.Width <= maxWidth {
			best, found = candidate, true
			lo = mid + 1
			continue
		}
		hi = mid - 1
	}
	if found {
		return best, true
	}
	return narrowest, true
}

func renderAt(src string, wrap int) (Art, bool) {
	src = stripControls(src)
	if trimJS(src) == "" {
		return Art{}, false
	}
	c, warnings, ok := attempt(src, wrap)
	if !ok {
		return Art{}, false
	}
	plain, styled, width := c.toLines()
	if warnings == nil {
		warnings = []string{}
	}
	return Art{Plain: plain, Styled: styled, Width: width, Warnings: warnings, splitWord: c.splitWord}, true
}

// DiagramKind returns the kind src declares ("flowchart"/"state"/"class"/"er"/
// "sequence"), or "" if its header names no type this renderer draws.
func DiagramKind(src string) string { return diagramKind(src) }

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
