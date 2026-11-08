// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// `classDiagram`: classes with member compartments and UML relations. Ported
// from lovely-mermaid diagrams/class.ts. Lenient: an unreadable statement is
// dropped and recorded in warnings.
//
// Compartments live on node.sections: [title, attrs, methods], where the title
// is the optional `«annotation»` line followed by the class name.

var classHeaders = []string{"classdiagram", "classdiagram-v2"}

var classDiagram = diagram{
	kind:    "class",
	headers: classHeaders,
	render: func(src string) (*canvas, drawnMeta, bool) {
		g := parseClass(src)
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

type classOp struct {
	op       string
	headFrom head
	headTo   head
	line     lineKind
}

// classOps are the relation operators, longest-first so `--|>` wins over `--`.
var classOps = []classOp{
	{"<|--", headTriangle, headNone, lineSolid},
	{"--|>", headNone, headTriangle, lineSolid},
	{"<|..", headTriangle, headNone, lineDotted},
	{"..|>", headNone, headTriangle, lineDotted},
	{"*--", headDiamondFill, headNone, lineSolid},
	{"--*", headNone, headDiamondFill, lineSolid},
	{"o--", headDiamondOpen, headNone, lineSolid},
	{"--o", headNone, headDiamondOpen, lineSolid},
	{"<--", headArrow, headNone, lineSolid},
	{"-->", headNone, headArrow, lineSolid},
	{"<..", headArrow, headNone, lineDotted},
	{"..>", headNone, headArrow, lineDotted},
	{"--", headNone, headNone, lineSolid},
	{"..", headNone, headNone, lineDotted},
}

const maxClassOp = 4

const (
	bodyNone = -1 // no open `{` body
	bodySkip = -2 // a dropped declaration's body, swallowed whole
)

func parseClass(src string) *graph {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind == "" || !hasHeader(classHeaders, kind) {
		return nil
	}

	g := newGraph(dirDown)
	// declare declares a class from an id token, attaching any `:::` tags it carries.
	declare := func(token string) (int, bool) {
		id, classes := takeTags(token)
		idx, ok := g.nodeIndex(id, nil, shapeRect)
		if ok {
			if g.nodes[idx].sections == nil {
				g.nodes[idx].sections = [][]string{{displayGenerics(id)}, {}, {}}
			}
			for _, cls := range classes {
				g.addClass(idx, cls)
			}
		}
		return idx, ok
	}
	// The open `{` body: a class index, bodySkip for a dropped class, or bodyNone.
	curClass := bodyNone
	// `class A,B name` / `cssClass "A,B" name` assignments, applied post-walk.
	var classAssignments []classAssign
	// `link A "url"` / `click A href "url"` targets, applied post-walk.
	var hrefs []hrefAssign

	for _, st := range statements[1:] {
		if curClass != bodyNone {
			if st == "}" {
				curClass = bodyNone
			} else if curClass != bodySkip {
				pushMember(&g.nodes[curClass], st)
			}
			continue
		}

		first := asciiLower(firstWord(st))
		if first == "direction" {
			arg := ""
			if w := words(st); len(w) > 1 {
				arg = w[1]
			}
			g.dir = parseDir(arg)
			continue
		}
		if first == "classdef" {
			if names, props, ok := parseClassDef(st[len(firstWord(st)):]); ok {
				for _, name := range names {
					g.classDefs[name] = props
				}
			}
			continue
		}
		switch first {
		case "note", "callback", "style", "namespace", "}":
			continue
		}
		if first == "link" || first == "click" {
			if h, ok := parseHref(st[len(firstWord(st)):]); ok {
				hrefs = append(hrefs, h)
			}
			continue
		}
		if first == "cssclass" {
			if a, ok := parseClassAssign(strings.ReplaceAll(st[len(firstWord(st)):], "\"", "")); ok {
				classAssignments = append(classAssignments, a)
			} else {
				g.drop(st)
			}
			continue
		}
		switch {
		case first == "class":
			rest := trimJS(st[len("class"):])
			open := strings.HasSuffix(rest, "{")
			name := rest
			if open {
				name = trimJS(rest[:len(rest)-1])
			}
			// `class A["Label"]` (mermaid >=10.1): the label titles the box, the id
			// keys relations. Peel it before the space test: labels carry spaces.
			var label *string
			if id, lbl, suffix, ok := classLabeled(name); ok {
				name = id + suffix
				label = nonEmpty(cleanLabel(lbl))
			}
			switch {
			case !open && containsJSSpace(name):
				// Class names carry no spaces, so `class Agent focus` is the
				// assignment form, as in flowcharts.
				if a, ok := parseClassAssign(name); ok {
					classAssignments = append(classAssignments, a)
				} else {
					g.drop(st)
				}
			case name == "" || containsJSSpace(name):
				// A bad declaration that opened a body swallows it whole; reading
				// the members as top-level statements would misparse everything inside.
				g.drop(st)
				if open {
					curClass = bodySkip
				}
			default:
				idx, ok := declare(name)
				if ok && label != nil {
					n := &g.nodes[idx]
					n.label = *label
					// The name is the last title line (an annotation may precede it).
					if len(n.sections) > 0 && len(n.sections[0]) > 0 {
						n.sections[0][len(n.sections[0])-1] = *label
					}
				}
				if open {
					if ok {
						curClass = idx
					} else {
						curClass = bodySkip
					}
				}
			}
		case strings.HasPrefix(st, "<<"):
			ann, name, ok := splitOnce(st[2:], ">>")
			name = trimJS(name)
			if !ok || name == "" || containsJSSpace(name) {
				g.drop(st)
			} else if idx, ok := declare(name); ok {
				setAnnotation(&g.nodes[idx], trimJS(ann))
			}
		default:
			if rel := parseClassRelation(st); rel != nil {
				f, ok := declare(rel.from)
				var t int
				if ok {
					t, ok = declare(rel.to)
				}
				if ok {
					g.pushEdge(edge{
						from: f, to: t, label: rel.label,
						cardFrom: rel.cardFrom, cardTo: rel.cardTo,
						headTo: rel.headTo, headFrom: rel.headFrom, line: rel.line,
					})
				}
			} else {
				memberID, text, hasColon := splitColon(st)
				memberID, text = trimJS(memberID), trimJS(text)
				if !hasColon || memberID == "" || containsJSSpace(memberID) || text == "" {
					g.drop(st)
				} else if idx, ok := declare(memberID); ok {
					pushMember(&g.nodes[idx], text)
				}
			}
		}
		if g.truncated != "" {
			g.warnings = append(g.warnings, "diagram truncated: "+g.truncated)
			break
		}
	}

	g.applyClasses(classAssignments)
	g.applyHrefs(hrefs)

	if len(g.nodes) == 0 {
		return nil
	}
	return g
}

// classLabeled matches /^(\S+?)\[(.+)\](:::\S+)?$/: an id, a bracketed label
// and an optional `:::tag` suffix. \S+? is lazy and .+ greedy, so the id is
// the shortest prefix that lets the rest match and the label runs to the last
// `]` that leaves a valid suffix.
func classLabeled(name string) (id, label, suffix string, ok bool) {
	chars := []rune(name)
	for k := 1; k < len(chars); k++ {
		if jsstring.IsSpace(chars[k-1]) {
			return "", "", "", false
		}
		if chars[k] != '[' {
			continue
		}
		rest := chars[k+1:]
		for p := len(rest) - 1; p >= 1; p-- {
			if rest[p] != ']' {
				continue
			}
			if containsLineTerminator(rest[:p]) {
				continue
			}
			tail := string(rest[p+1:])
			if tail == "" || (strings.HasPrefix(tail, ":::") && len(tail) > 3 && !containsJSSpace(tail[3:])) {
				return string(chars[:k]), string(rest[:p]), tail, true
			}
		}
	}
	return "", "", "", false
}

func containsLineTerminator(r []rune) bool {
	for _, c := range r {
		if c == '\n' || c == '\r' || c == '\u2028' || c == '\u2029' {
			return true
		}
	}
	return false
}

// setAnnotation rewrites the title compartment as `«annotation»` over the class name.
func setAnnotation(n *node, annotation string) {
	n.sections[0] = []string{"«" + annotation + "»", displayGenerics(n.label)}
}

// pushMember adds a member to the attribute or method compartment, eliding
// past the cap.
func pushMember(n *node, raw string) {
	if strings.HasPrefix(raw, "<<") {
		if ann, _, ok := splitOnce(raw[2:], ">>"); ok {
			setAnnotation(n, trimJS(ann))
		}
		return
	}
	member := decodeHtmlEntities(displayGenerics(trimJS(raw)))
	list := &n.sections[1]
	if strings.Contains(member, "(") {
		list = &n.sections[2]
	}
	if len(*list) < maxMembers {
		*list = append(*list, member)
	} else if len(*list) == maxMembers {
		*list = append(*list, "…")
	}
}

type classRelation struct {
	from, to         string
	headFrom, headTo head
	line             lineKind
	label            *string
	cardFrom, cardTo string
}

func parseClassRelation(st string) *classRelation {
	chars := []rune(st)
	// Skip quoted spans, or the `..` inside a cardinality like `"0..*"` would
	// match the dotted-link operator.
	quoted := quoteMask(chars)
	var found *classOp
	foundPos := 0

outer:
	for pos := range chars {
		if quoted[pos] != 0 {
			continue
		}
		tail := string(chars[pos:min(pos+maxClassOp, len(chars))])
		for i := range classOps {
			op := &classOps[i]
			if !strings.HasPrefix(tail, op.op) {
				continue
			}
			// `o` is also an identifier character: skip a match glued to a name.
			if strings.HasPrefix(op.op, "o") && pos > 0 && isIdChar(chars[pos-1]) {
				continue
			}
			if strings.HasSuffix(op.op, "o") && pos+len(op.op) < len(chars) && isIdChar(chars[pos+len(op.op)]) {
				continue
			}
			found, foundPos = op, pos
			break outer
		}
	}
	if found == nil {
		return nil
	}

	lhsRaw := trimJS(string(chars[:foundPos]))
	rhsRaw := trimJS(string(chars[foundPos+len(found.op):]))

	lhs, cardFrom := stripCardinalitySuffix(lhsRaw)
	rhs, cardTo := stripCardinalityPrefix(rhsRaw)

	toID, relText, hasColon := splitColon(rhs)
	if !hasColon {
		toID = rhs
	}
	toID = trimJS(toID)
	var relLabel *string
	if hasColon {
		relLabel = nonEmpty(decodeHtmlEntities(trimJS(relText)))
	}

	if lhs == "" || toID == "" || containsJSSpace(lhs) || containsJSSpace(toID) {
		return nil
	}

	return &classRelation{
		from: lhs, to: toID,
		headFrom: found.headFrom, headTo: found.headTo, line: found.line,
		label: relLabel, cardFrom: cardFrom, cardTo: cardTo,
	}
}

// stripCardinalitySuffix reads `Class "1"`: a quoted cardinality trailing the
// left-hand name.
func stripCardinalitySuffix(s string) (string, string) {
	t := trimEndJS(s)
	if strings.HasSuffix(t, "\"") {
		rest := t[:len(t)-1]
		if q := strings.LastIndex(rest, "\""); q != -1 {
			return trimEndJS(rest[:q]), rest[q+1:]
		}
	}
	return t, ""
}

// stripCardinalityPrefix reads `"0..*" Class`: a quoted cardinality leading
// the right-hand name.
func stripCardinalityPrefix(s string) (string, string) {
	t := trimStartJS(s)
	if strings.HasPrefix(t, "\"") {
		rest := t[1:]
		if q := strings.Index(rest, "\""); q != -1 {
			return trimStartJS(rest[q+1:]), rest[:q]
		}
	}
	return t, ""
}
