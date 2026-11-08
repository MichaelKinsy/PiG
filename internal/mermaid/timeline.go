// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// `timeline`: periods and their events as a vertical list, one row per event,
// the period named on its first row. Ported from lovely-mermaid
// diagrams/timeline.ts. Lenient: an unreadable statement is dropped and
// recorded in warnings.

// timelineRow is one output row: a period cell (blank on continuations) and an event.
type timelineRow struct {
	period string
	event  string
	// section headers occupy a row of their own.
	section bool
}

func timelineDiagram() *diagram {
	return &diagram{
		kind:    "timeline",
		headers: []string{"timeline"},
		render: func(src string) (*canvas, drawnMeta, bool) {
			title, rows, warnings, ok := parseTimeline(src)
			if !ok {
				return nil, drawnMeta{}, false
			}

			periodW := 0
			for _, r := range rows {
				if !r.section {
					periodW = max(periodW, stringWidth(r.period))
				}
			}
			top := 0
			width := 0
			if title != nil {
				top = 1
				width = stringWidth(*title) + 6
			}
			for _, r := range rows {
				if r.section {
					width = max(width, stringWidth(r.event))
				} else {
					width = max(width, periodW+3+stringWidth(r.event))
				}
			}
			c := newCanvas(width, top+len(rows))

			if title != nil {
				t := " " + *title + " "
				x := max(0, floorDiv(width-stringWidth(t)-4, 2))
				drawText(c, "──", x, 0, RoleEdge)
				drawText(c, t, x+2, 0, RoleTitle)
				drawText(c, "──", x+2+stringWidth(t), 0, RoleEdge)
			}
			for i, row := range rows {
				y := top + i
				if row.section {
					drawText(c, row.event, 0, y, RoleTitle)
					continue
				}
				drawText(c, row.period, 0, y, RoleText)
				if row.event == "" {
					continue
				}
				drawText(c, "─", periodW+1, y, RoleEdge)
				drawText(c, row.event, periodW+3, y, RoleEdgeLabel)
			}
			return c, drawnMeta{warnings: warnings, classDefs: map[string]map[string]string{}}, true
		},
	}
}

// floorDiv is Math.floor(a / b) for b > 0.
func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

func parseTimeline(src string) (title *string, rows []timelineRow, warnings []string, ok bool) {
	statements := statementsOf(src)
	if headerKind(statements) != "timeline" {
		return nil, nil, nil, false
	}

	warnings = []string{}
	truncated := false
	lastPeriod := false

	for _, st := range statements[1:] {
		if len(rows) >= maxEdges {
			truncated = true
			break
		}
		first := ""
		if w := words(st); len(w) > 0 {
			first = jsstring.ToLower(w[0])
		}
		if first == "title" {
			title = nonEmpty(trimJS(st[len("title"):]))
			continue
		}
		if first == "section" {
			name := trimJS(st[len("section"):])
			rows = append(rows, timelineRow{period: "", event: timelineClean(name), section: true})
			lastPeriod = false
			continue
		}
		// `period : event : event`; a statement of only `: event`s continues the
		// previous period (the `;`-split form of mermaid's multi-line events).
		parts := strings.Split(st, ":")
		for i := range parts {
			parts[i] = timelineClean(parts[i])
		}
		period := parts[0]
		parts = parts[1:]
		if period == "" && len(parts) > 0 && lastPeriod {
			for _, event := range parts {
				rows = append(rows, timelineRow{event: event})
			}
			continue
		}
		// A bare period renders event-less, as mermaid draws it.
		if period != "" && len(parts) == 0 {
			rows = append(rows, timelineRow{period: period})
			lastPeriod = true
			continue
		}
		if period == "" || hasEmpty(parts) {
			warnings = append(warnings, "dropped, unreadable statement: \""+st+"\"")
			continue
		}
		for i, event := range parts {
			p := ""
			if i == 0 {
				p = period
			}
			rows = append(rows, timelineRow{period: p, event: event})
		}
		lastPeriod = true
	}
	if truncated {
		warnings = append(warnings, "diagram truncated: event cap ("+itoa(maxEdges)+") reached")
	}

	if len(rows) == 0 {
		return nil, nil, nil, false
	}
	return title, rows, warnings, true
}

func hasEmpty(parts []string) bool {
	return slices.Contains(parts, "")
}

func timelineClean(s string) string {
	return fitLabel(decodeHtmlEntities(trimJS(s)), maxLabel)
}
