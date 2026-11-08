// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// `erDiagram`: entities with attribute compartments and crow's-foot
// relationships. Ported from lovely-mermaid diagrams/er.ts. Lenient: an
// unreadable statement is dropped and recorded in warnings.
//
// Entities use node.sections as [title, attrs].

var erHeaders = []string{"erdiagram"}

func erDiagram() *diagram {
	return &diagram{
		kind:    "er",
		headers: erHeaders,
		render: func(src string) (*canvas, drawnMeta, bool) {
			g := parseEr(src)
			if g == nil {
				return nil, drawnMeta{}, false
			}
			c := layoutClass(g)
			if c == nil {
				return nil, drawnMeta{}, false
			}
			return c, drawnMeta{warnings: g.warnings, classDefs: g.classDefs}, true
		},
	}
}

func parseEr(src string) *graph {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind == "" || !hasHeader(erHeaders, kind) {
		return nil
	}

	g := newGraph(dirDown)
	// The open `{` body: an entity index, bodySkip for a dropped one, or bodyNone.
	curEntity := bodyNone

	for _, st := range statements[1:] {
		if curEntity != bodyNone {
			if st == "}" {
				curEntity = bodyNone
			} else if curEntity != bodySkip {
				pushErAttribute(&g.nodes[curEntity], st)
			}
			continue
		}

		if relText, relLabel, ok := splitErRelationship(st); ok {
			tokens := erTokens(relText)
			var op erOp
			opOK := false
			if len(tokens) == 3 {
				op, opOK = parseErOp(tokens[1])
			}
			var f, t int
			fOK, tOK := false, false
			if opOK {
				f, fOK = erEntity(g, tokens[0])
			}
			if fOK {
				t, tOK = erEntity(g, tokens[2])
			}
			if !opOK || !fOK || !tOK {
				g.drop(st)
			} else {
				lbl := ""
				if relLabel != nil {
					lbl = cleanLabel(*relLabel)
				}
				g.pushEdge(edge{
					from: f, to: t, label: nonEmpty(lbl),
					cardFrom: op.cardL, cardTo: op.cardR,
					headTo: headNone, headFrom: headNone, line: op.line,
				})
			}
		} else {
			open := strings.HasSuffix(st, "{")
			decl := st
			if open {
				decl = trimJS(st[:len(st)-1])
			}
			if decl == "" || len(erTokens(decl)) != 1 {
				// A bad declaration that opened a body swallows it whole; reading
				// the attributes as top-level statements would misparse everything inside.
				g.drop(st)
				if open {
					curEntity = bodySkip
				}
			} else {
				idx, ok := erEntity(g, decl)
				if open {
					if ok {
						curEntity = idx
					} else {
						curEntity = bodySkip
					}
				}
			}
		}
		if g.truncated != "" {
			g.warnings = append(g.warnings, "diagram truncated: "+g.truncated)
			break
		}
	}

	if len(g.nodes) == 0 {
		return nil
	}
	return g
}

// erEntity resolves an entity token (`NAME`, `"Quoted Name"` or `id[Label]`),
// keeping its title row fresh. A quoted name is its own identity; the quotes
// are not part of the title.
func erEntity(g *graph, token string) (int, bool) {
	open := -1
	if !strings.HasPrefix(token, "\"") {
		open = strings.Index(token, "[")
	}
	var idx int
	var ok bool
	switch {
	case open != -1:
		id := token[:open]
		if !strings.HasSuffix(token, "]") {
			g.warnings = append(g.warnings, "entity \""+id+"\": alias is missing its closing `]`")
		}
		label := cleanLabel(strings.TrimRight(token[open+1:], "]"))
		if id == "" || label == "" {
			return 0, false
		}
		idx, ok = g.nodeLabel(id, label)
	case strings.HasPrefix(token, "\""):
		label := cleanLabel(token)
		if label == "" {
			return 0, false
		}
		idx, ok = g.nodeIndex(token, &label, shapeRect)
	default:
		idx, ok = g.nodeIndex(token, nil, shapeRect)
	}
	if !ok {
		return 0, false
	}
	n := &g.nodes[idx]
	if n.sections == nil {
		n.sections = [][]string{{}, {}}
	}
	n.sections[0] = []string{displayGenerics(n.label)}
	return idx, true
}

// erTokens splits on whitespace, quoted spans kept whole so aliases may contain
// spaces.
func erTokens(s string) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	for _, c := range s {
		switch {
		case c == '"':
			inQuotes = !inQuotes
			cur.WriteRune(c)
		case !inQuotes && jsstring.IsSpace(c):
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func splitErRelationship(st string) (rel string, label *string, ok bool) {
	rel = st
	if before, after, found := splitOnce(st, ":"); found {
		rel = before
		l := trimJS(after)
		label = &l
	}
	for _, t := range erTokens(rel) {
		if _, isOp := parseErOp(t); isOp {
			return rel, label, true
		}
	}
	return "", nil, false
}

func isAscii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return false
		}
	}
	return true
}

type erOp struct {
	cardL, cardR string
	line         lineKind
}

// parseErOp reads a crow's-foot operator: two cardinality glyphs around `--` or `..`.
func parseErOp(tok string) (erOp, bool) {
	if len(tok) != 6 || !isAscii(tok) {
		return erOp{}, false
	}
	var line lineKind
	switch tok[2:4] {
	case "--":
		line = lineSolid
	case "..":
		line = lineDotted
	default:
		return erOp{}, false
	}
	cardL, okL := erCard(tok[0:2])
	cardR, okR := erCard(tok[4:6])
	if !okL || !okR {
		return erOp{}, false
	}
	return erOp{cardL: cardL, cardR: cardR, line: line}, true
}

func erCard(tok string) (string, bool) {
	switch tok {
	case "|o", "o|":
		return "0..1", true
	case "||":
		return "1", true
	case "}o", "o{":
		return "*", true
	case "}|", "|{":
		return "1..*", true
	}
	return "", false
}

// pushErAttribute adds an ER attribute, `type name`; a trailing quoted comment
// is dropped.
func pushErAttribute(n *node, raw string) {
	attrs := &n.sections[1]
	var parts []string
	for _, tok := range words(raw) {
		if strings.HasPrefix(tok, "\"") {
			break
		}
		parts = append(parts, tok)
	}
	if len(parts) == 0 {
		return
	}
	line := decodeHtmlEntities(strings.Join(parts, " "))
	if len(*attrs) < maxMembers {
		*attrs = append(*attrs, line)
	} else if len(*attrs) == maxMembers {
		*attrs = append(*attrs, "…")
	}
}
