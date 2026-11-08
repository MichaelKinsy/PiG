// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"strconv"
	"strings"
)

// ANSI rendering, ported from lovely-mermaid ansi.ts.

// AnsiTheme maps a role to an SGR parameter, e.g. "2" (dim), "36" (cyan),
// "38;5;244" (256-colour). A role left out is printed unstyled.
type AnsiTheme map[Role]string

// DefaultTheme: dim frame, plain labels, cyan connectors. Mirrors DEFAULT_THEME.
var DefaultTheme = AnsiTheme{
	RoleBorder:    "2",
	RoleEdge:      "36",
	RoleEdgeLabel: "2;36",
	RoleTitle:     "1",
}

const (
	esc  = "\x1b"
	osc8 = esc + "]8;;"
	st   = esc + "\\"
)

func rgbSgr(hex string, sgr int) string {
	parts := make([]string, 0, 5)
	parts = append(parts, strconv.Itoa(sgr), "2")
	for _, i := range [3]int{1, 3, 5} {
		n, _ := strconv.ParseInt(hex[i:i+2], 16, 0)
		parts = append(parts, strconv.FormatInt(n, 10))
	}
	return strings.Join(parts, ";")
}

// ClassSgr is the truecolor SGR a class style gives a span of the given role,
// or ok=false when the style says nothing about it (fall back to the theme).
// stroke colors borders, color text; fill backs every painted cell, with a
// black/white foreground picked for contrast when none was declared. A style
// that colors nothing for this role keeps fallback (the theme's SGR, when
// hasFallback), so a bold-only class bolds the themed look.
func ClassSgr(style ClassStyle, role Role, fallback string, hasFallback bool) (string, bool) {
	var p []string
	var fg *string
	switch role {
	case RoleBorder:
		if style.Stroke != nil {
			fg = style.Stroke
		} else {
			fg = style.Color
		}
	case RoleEdge:
	default:
		fg = style.Color
	}
	backed := fg
	if backed == nil && style.Fill != nil {
		c := ContrastOn(*style.Fill)
		backed = &c
	}
	if backed != nil {
		p = append(p, rgbSgr(*backed, 38))
	}
	if style.Fill != nil {
		p = append(p, rgbSgr(*style.Fill, 48))
	}
	if len(p) == 0 && hasFallback {
		p = append(p, fallback)
	}
	if style.Bold != nil && *style.Bold {
		p = append([]string{"1"}, p...)
	}
	if len(p) == 0 {
		return "", false
	}
	return strings.Join(p, ";"), true
}

// ToAnsi renders art to ANSI-coloured lines. Spans that carry author classes
// are styled from art.ClassDefs, overriding the role theme; everything else
// follows theme. A span's link target becomes an OSC 8 hyperlink.
func ToAnsi(art Art, theme AnsiTheme) []string {
	out := make([]string, len(art.Styled))
	for i, row := range art.Styled {
		var b strings.Builder
		for _, span := range row {
			themed, hasThemed := theme[span.Role]
			sgr, has := themed, hasThemed
			if cls := ResolveClassStyle(span.Classes, art.ClassDefs); cls != nil {
				sgr, has = ClassSgr(*cls, span.Role, themed, hasThemed)
			}
			if span.Href != nil {
				b.WriteString(osc8 + *span.Href + st)
			}
			if has {
				b.WriteString(esc + "[" + sgr + "m" + span.Text + esc + "[0m")
			} else {
				b.WriteString(span.Text)
			}
			if span.Href != nil {
				b.WriteString(osc8 + st)
			}
		}
		out[i] = b.String()
	}
	return out
}
