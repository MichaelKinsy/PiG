// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"math"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// `pie`: proportions as a labelled bar list. Ported from lovely-mermaid
// diagrams/pie.ts. A terminal has no circle worth drawing; bars carry the same
// information in less space and align with how every TUI shows usage. Lenient:
// an unreadable statement is dropped and recorded in warnings.

// pieBarW is the columns of the full-scale bar; eighth blocks refine below one cell.
const pieBarW = 20

var pieEighths = [8]string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

func pieDiagram() *diagram {
	return &diagram{
		kind:    "pie",
		headers: []string{"pie"},
		render: func(src string) (*canvas, drawnMeta, bool) {
			parsed := parsePie(src)
			if parsed == nil {
				return nil, drawnMeta{}, false
			}

			labelW := 0
			total := 0.0
			for i, s := range parsed.slices {
				if w := stringWidth(s.label); i == 0 || w > labelW {
					labelW = w
				}
				total += s.value
			}
			type pieRow struct {
				pieSlice
				share  float64
				suffix string
			}
			rows := make([]pieRow, len(parsed.slices))
			suffixW := 0
			for i, s := range parsed.slices {
				share := 0.0
				if total != 0 {
					share = s.value / total
				}
				pct := jsnumber.String(jsnumber.Round(share*100)) + "%"
				for len(pct) < 4 {
					pct = " " + pct
				}
				data := ""
				if parsed.showData {
					data = "  (" + jsnumber.String(s.value) + ")"
				}
				rows[i] = pieRow{pieSlice: s, share: share, suffix: pct + data}
				if w := stringWidth(rows[i].suffix); i == 0 || w > suffixW {
					suffixW = w
				}
			}

			barX := labelW + 2
			suffixX := barX + pieBarW + 1
			width := suffixX + suffixW
			top := 0
			if parsed.title != nil {
				top = 1
			}
			c := newCanvas(width, top+len(rows))

			if parsed.title != nil {
				drawText(c, *parsed.title, max(0, (width-stringWidth(*parsed.title))/2), 0, RoleTitle)
			}
			for i, r := range rows {
				y := top + i
				drawText(c, r.label, 0, y, RoleText)
				eighths := int(jsnumber.Round(r.share * pieBarW * 8))
				bar := strings.Repeat("█", eighths/8) + pieEighths[eighths%8]
				// A nonzero slice always shows at least a sliver.
				if bar == "" && r.value > 0 {
					bar = "▏"
				}
				drawText(c, bar, barX, y, RoleEdge)
				// The unfilled remainder is a track, so every bar shows its full scale.
				drawText(c, strings.Repeat("░", pieBarW-stringWidth(bar)), barX+stringWidth(bar), y, RoleBorder)
				drawText(c, r.suffix, suffixX, y, RoleEdgeLabel)
			}
			return c, drawnMeta{warnings: parsed.warnings, classDefs: map[string]map[string]string{}}, true
		},
	}
}

type pieSlice struct {
	label string
	value float64
}

type pieModel struct {
	title    *string
	slices   []pieSlice
	showData bool
	warnings []string
}

func parsePie(src string) *pieModel {
	statements := statementsOf(src)
	if headerKind(statements) != "pie" {
		return nil
	}

	// The header line may carry `showData` and an inline `title ...`.
	head := words(statements[0])
	showData := false
	inlineTitle := -1
	for i, w := range head {
		lw := jsstring.ToLower(w)
		if lw == "showdata" {
			showData = true
		}
		if lw == "title" && inlineTitle == -1 {
			inlineTitle = i
		}
	}
	var title *string
	if inlineTitle != -1 {
		title = nonEmpty(strings.Join(head[inlineTitle+1:], " "))
	}

	var slices []pieSlice
	warnings := []string{}
	truncated := false
	for _, st := range statements[1:] {
		first := ""
		if w := words(st); len(w) > 0 {
			first = jsstring.ToLower(w[0])
		}
		if first == "title" {
			title = nonEmpty(trimJS(st[len("title"):]))
			continue
		}
		slice, ok := parseSlice(st)
		if !ok {
			warnings = append(warnings, "dropped, unreadable statement: \""+st+"\"")
			continue
		}
		if len(slices) >= maxNodes {
			truncated = true
			break
		}
		slices = append(slices, slice)
	}
	if truncated {
		warnings = append(warnings, "diagram truncated: slice cap ("+itoa(maxNodes)+") reached")
	}

	if len(slices) == 0 {
		return nil
	}
	return &pieModel{title: title, slices: slices, showData: showData, warnings: warnings}
}

// parseSlice reads `"Label" : 42.5`; the label may be unquoted as long as it
// has no colon.
func parseSlice(st string) (pieSlice, bool) {
	chars := []rune(st)
	quoted := quoteMask(chars)
	colon := -1
	for i, c := range chars {
		if c == ':' && quoted[i] == 0 {
			colon = i
			break
		}
	}
	if colon == -1 {
		return pieSlice{}, false
	}
	label := fitLabel(cleanLabel(string(chars[:colon])), 24)
	value := jsnumber.Parse(trimJS(string(chars[colon+1:])))
	if label == "" || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return pieSlice{}, false
	}
	return pieSlice{label: label, value: value}, true
}
