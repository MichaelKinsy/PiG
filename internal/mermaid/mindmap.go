// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// `mindmap`: an indentation tree, drawn the way every TUI draws trees
// (`├──`/`└──` guides). Ported from lovely-mermaid diagrams/mindmap.ts. It
// parses raw lines rather than statements: the indentation is the grammar, and
// statementsOf trims it away.

type mindNode struct {
	text     string
	children []*mindNode
}

var mindmapDiagram = diagram{
	kind:    "mindmap",
	headers: []string{"mindmap"},
	render: func(src string) (*canvas, drawnMeta, bool) {
		roots, warnings, ok := parseMindmap(src)
		if !ok {
			return nil, drawnMeta{}, false
		}

		// [prefix, text] per row; prefixes carry the `│ ├ └` guides.
		type mindRow struct{ prefix, text string }
		var rows []mindRow
		var walk func(n *mindNode, prefix, childPrefix string)
		walk = func(n *mindNode, prefix, childPrefix string) {
			rows = append(rows, mindRow{prefix, n.text})
			for i, child := range n.children {
				last := i == len(n.children)-1
				if last {
					walk(child, childPrefix+"└── ", childPrefix+"    ")
				} else {
					walk(child, childPrefix+"├── ", childPrefix+"│   ")
				}
			}
		}
		for _, root := range roots {
			walk(root, "", "")
		}

		width := 0
		for _, r := range rows {
			width = max(width, stringWidth(r.prefix)+stringWidth(r.text))
		}
		c := newCanvas(width, len(rows))
		for y, r := range rows {
			drawText(c, r.prefix, 0, y, RoleEdge)
			drawText(c, r.text, stringWidth(r.prefix), y, RoleText)
		}
		return c, drawnMeta{warnings: warnings, classDefs: map[string]map[string]string{}}, true
	},
}

func parseMindmap(src string) (roots []*mindNode, warnings []string, ok bool) {
	all := srcLines(src)
	lines := all[min(frontmatterEnd(all), len(all)):]
	headerAt := -1
	for i, l := range lines {
		if trimJS(l) != "" {
			headerAt = i
			break
		}
	}
	if headerAt == -1 || jsstring.ToLower(trimJS(lines[headerAt])) != "mindmap" {
		return nil, nil, false
	}

	warnings = []string{}
	// Ancestors of the next node: the node at each indent level seen so far.
	type frame struct {
		indent int
		node   *mindNode
	}
	var stack []frame
	count := 0
	truncated := false

	for _, raw := range lines[headerAt+1:] {
		noComment, _, _ := strings.Cut(raw, "%%")
		if trimJS(noComment) == "" {
			continue
		}
		indent := jsstring.Length(noComment) - jsstring.Length(trimStartJS(noComment))
		body := trimJS(noComment)
		// Decoration lines attach to the previous node and draw nothing.
		if strings.HasPrefix(body, "::icon") || strings.HasPrefix(body, ":::") {
			continue
		}
		text := nodeText(body)
		if text == "" {
			warnings = append(warnings, "dropped, unreadable statement: \""+body+"\"")
			continue
		}
		if count >= maxNodes {
			truncated = true
			break
		}
		count++
		node := &mindNode{text: fitLabel(text, wrapWidth)}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, node)
		} else {
			parent := stack[len(stack)-1].node
			parent.children = append(parent.children, node)
		}
		stack = append(stack, frame{indent, node})
	}
	if truncated {
		warnings = append(warnings, "diagram truncated: node cap ("+itoa(maxNodes)+") reached")
	}

	if len(roots) == 0 {
		return nil, nil, false
	}
	return roots, warnings, true
}

// mindShapes: shape brackets around a mindmap node all mean "text" in a terminal.
var mindShapes = [][2]string{
	{"((", "))"},
	{"))", "(("},
	{"(-", "-)"},
	{"{{", "}}"},
	{"[", "]"},
	{"(", ")"},
}

func nodeText(body string) string {
	// `id((text))`: an optional id may precede the bracket.
	for _, shape := range mindShapes {
		open, closer := shape[0], shape[1]
		at := strings.Index(body, open)
		if at != -1 && strings.HasSuffix(body, closer) && jsstring.Length(body[at:]) >= len(open)+len(closer) {
			return cleanLabel(body[at+len(open) : len(body)-len(closer)])
		}
	}
	return cleanLabel(body)
}
