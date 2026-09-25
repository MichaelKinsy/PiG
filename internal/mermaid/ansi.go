// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import "strings"

// ANSI rendering, ported from grok-mermaid ansi.ts. A convenience over mapping
// art.Styled yourself: pig's markdown integration maps Cls to its own theme
// instead, but this keeps the upstream helper faithful.

// AnsiTheme maps a semantic class to an SGR parameter, e.g. "2" (dim), "36"
// (cyan), "38;5;244" (256-colour). A class left out is printed unstyled.
type AnsiTheme map[Cls]string

// DefaultTheme: dim frame, plain labels, cyan connectors. Mirrors DEFAULT_THEME.
var DefaultTheme = AnsiTheme{
	ClsBorder:    "2",
	ClsEdge:      "36",
	ClsEdgeLabel: "2;36",
	ClsTitle:     "1",
}

const esc = "\x1b"

// ToAnsi renders art to ANSI-coloured lines using theme.
func ToAnsi(art Art, theme AnsiTheme) []string {
	out := make([]string, len(art.Styled))
	for i, row := range art.Styled {
		var b strings.Builder
		for _, span := range row {
			sgr, ok := theme[span.Cls]
			if !ok {
				b.WriteString(span.Text)
				continue
			}
			b.WriteString(esc + "[" + sgr + "m" + span.Text + esc + "[0m")
		}
		out[i] = b.String()
	}
	return out
}
