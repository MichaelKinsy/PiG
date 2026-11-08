// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import "strings"

// `stateDiagram` / `stateDiagram-v2`: states, transitions, descriptions and
// composite states. Ported from lovely-mermaid diagrams/state.ts. Lenient: an
// unreadable statement is dropped and recorded in warnings.
//
// A composite (`state X { ... }`) becomes a group, drawn as a titled frame by
// the same machinery flowchart subgraphs use; `--` splits a composite into
// unlabelled sibling region groups. `[*]` is scoped per group, so an inner
// start dot is a different node from the outer one.

var stateHeaders = []string{"statediagram", "statediagram-v2"}

var stateDiagram = diagram{
	kind:    "state",
	headers: stateHeaders,
	render: func(src string) (*canvas, drawnMeta, bool) {
		g := parseState(src)
		if g == nil {
			return nil, drawnMeta{}, false
		}
		var c *canvas
		if len(g.groups) == 0 {
			c = layoutFlowchart(g)
		} else {
			c = layoutGrouped(g)
		}
		if c == nil {
			return nil, drawnMeta{}, false
		}
		return c, drawnMeta{warnings: g.warnings, classDefs: g.classDefs}, true
	},
}

// stateScope is an open composite: the group itself and its current `--`
// region, if any.
type stateScope struct {
	base   int
	region *int
}

func intPtr(n int) *int { return &n }

func parseState(src string) *graph {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind == "" || !hasHeader(stateHeaders, kind) {
		return nil
	}

	g := newGraph(dirDown)
	inNote := false
	// `class A,B name` assignments, applied after the walk.
	var classAssignments []classAssign
	var stack []stateScope

	// newGroup is a new group under the current scope, or -1 once a cap is hit.
	newGroup := func(id, label string) int {
		if len(g.groups) >= maxGroups || len(stack) >= maxGroupDepth {
			if g.truncated == "" {
				g.truncated = "subgraph cap (" + itoa(maxGroups) + " groups, depth " + itoa(maxGroupDepth) + ") reached"
			}
			return -1
		}
		var parent *int
		if g.curGroup != nil {
			parent = intPtr(*g.curGroup)
		}
		g.groups = append(g.groups, group{id: id, label: label, parent: parent})
		return len(g.groups) - 1
	}
	scopeOf := func() *int {
		if len(stack) == 0 {
			return nil
		}
		top := stack[len(stack)-1]
		if top.region != nil {
			return intPtr(*top.region)
		}
		return intPtr(top.base)
	}

	for _, st := range statements[1:] {
		if inNote {
			if asciiLower(st) == "end note" {
				inNote = false
			}
			continue
		}
		first := asciiLower(firstWord(st))
		switch {
		case first == "direction":
			arg := ""
			if w := words(st); len(w) > 1 {
				arg = w[1]
			}
			g.dir = parseDir(arg)
		case first == "note":
			// A single-line `note ... : text` needs no terminator.
			if !strings.Contains(st, ":") {
				inNote = true
			}
		case first == "state":
			rest := trimJS(st[len(firstWord(st)):])
			open := strings.HasSuffix(rest, "{")
			body := rest
			if open {
				body = trimJS(rest[:len(rest)-1])
			}
			if !open {
				if !parseStateDecl(body, g) {
					g.drop(st)
				}
			} else {
				// A composite. An unreadable declaration still opens an anonymous
				// frame: the `{` was consumed, so the `}` balance must hold.
				id, label, named := compositeName(body)
				if !named {
					g.drop(st)
					id, label = "anon "+itoa(len(g.groups)), ""
				}
				if gi := newGroup(id, label); gi >= 0 {
					stack = append(stack, stateScope{base: gi})
					g.curGroup = intPtr(gi)
				}
			}
		case first == "}":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			g.curGroup = scopeOf()
		case first == "--":
			// Region divider: members so far move into region 1 on the first
			// `--`; each divider opens the next unlabelled sibling region.
			if len(stack) > 0 {
				top := &stack[len(stack)-1]
				if top.region == nil {
					if r1 := newGroup("region "+itoa(len(g.groups)), ""); r1 >= 0 {
						for i, ng := range g.nodeGroup {
							if ng != nil && *ng == top.base {
								g.nodeGroup[i] = intPtr(r1)
							}
						}
						for i := range g.groups {
							if p := g.groups[i].parent; i != r1 && p != nil && *p == top.base {
								g.groups[i].parent = intPtr(r1)
							}
						}
					}
				}
				g.curGroup = intPtr(top.base)
				if next := newGroup("region "+itoa(len(g.groups)), ""); next >= 0 {
					top.region = intPtr(next)
					g.curGroup = intPtr(next)
				} else {
					top.region = nil
					g.curGroup = intPtr(top.base)
				}
			}
		case first == "classdef":
			if names, props, ok := parseClassDef(st[len(firstWord(st)):]); ok {
				for _, name := range names {
					g.classDefs[name] = props
				}
			}
		case first == "class":
			if a, ok := parseClassAssign(st[len(firstWord(st)):]); ok {
				classAssignments = append(classAssignments, a)
			}
		case first == "hide" || first == "scale":
			// Styling directives carry no layout meaning.
		case strings.Contains(st, "-->"):
			if !parseTransition(st, g) {
				g.drop(st)
			}
		default:
			if !parseStateDesc(st, g) {
				g.drop(st)
			}
		}
		if g.truncated != "" {
			g.warnings = append(g.warnings, "diagram truncated: "+g.truncated)
			break
		}
	}

	g.applyClasses(classAssignments)

	if len(g.nodes) == 0 {
		return nil
	}
	return g
}

// compositeName reads the id and label of a composite declaration body.
func compositeName(body string) (id, label string, ok bool) {
	if strings.HasPrefix(body, "\"") {
		close := strings.Index(body[1:], "\"")
		if close == -1 {
			return "", "", false
		}
		label = decodeHtmlEntities(body[1 : 1+close])
		after := trimJS(body[close+2:])
		// A `:::` tag on a composite is dropped: groups paint no classed cells.
		src := label
		if strings.HasPrefix(after, "as") {
			src = trimJS(after[2:])
		}
		id, _ = takeTags(src)
		if id == "" {
			return "", "", false
		}
		return id, label, true
	}
	// A stereotype on a composite carries no drawing of its own; keep the name.
	name, _, _ := strings.Cut(body, "<<")
	id, _ = takeTags(trimJS(name))
	if id == "" || containsJSSpace(id) {
		return "", "", false
	}
	return id, id, true
}

// parseStateDecl reads `state "Label" as id` or `state id <<choice>>`, the
// non-composite forms.
func parseStateDecl(rest string, g *graph) bool {
	if rest == "" {
		return true
	}

	if strings.HasPrefix(rest, "\"") {
		close := strings.Index(rest[1:], "\"")
		if close == -1 {
			return false
		}
		label := rest[1 : 1+close]
		after := trimJS(rest[close+2:])
		src := label
		if strings.HasPrefix(after, "as") {
			src = trimJS(after[2:])
		}
		id, classes := takeTags(src)
		idx, ok := g.nodeLabel(id, decodeHtmlEntities(label))
		if !ok {
			return false
		}
		for _, cls := range classes {
			g.addClass(idx, cls)
		}
		return true
	}

	sh := shapeRound
	token := rest
	stereotyped := false
	if pos := strings.Index(rest, "<<"); pos != -1 {
		stereo := trimJS(strings.TrimSuffix(rest[pos+2:], ">>"))
		if stereo == "choice" {
			sh = shapeDiamond
		}
		token = trimJS(rest[:pos])
		stereotyped = true
	}
	id, classes := takeTags(token)
	if id == "" || containsJSSpace(id) {
		return false
	}
	var label *string
	if stereotyped {
		label = &id
	}
	idx, ok := g.nodeIndex(id, label, sh)
	if !ok {
		return false
	}
	for _, cls := range classes {
		g.addClass(idx, cls)
	}
	return true
}

// parseTransition reads `A --> B: label`, including chains `A --> B --> C`.
func parseTransition(st string, g *graph) bool {
	rest := st
	prev := -1

	for {
		lhs, rhs, ok := splitOnce(rest, "-->")
		if !ok {
			break
		}

		fromID, fromClasses := takeTags(trimJS(strings.TrimRight(trimEndJS(lhs), "-")))
		var from int
		if prev >= 0 {
			// Mid-chain: the source is the previous target, so nothing may precede.
			if fromID != "" {
				return false
			}
			from = prev
		} else {
			if fromID == "" {
				return false
			}
			f, ok := stateEndpoint(g, fromID, true)
			if !ok {
				return false
			}
			for _, cls := range fromClasses {
				g.addClass(f, cls)
			}
			from = f
		}

		// After the label colon it is all label: mermaid never chains past a
		// label, so an arrow inside one (`: go "x --> y"`) is text, not a link.
		nextArrow := strings.Index(rhs, "-->")
		colonHead, colonRest, hasColon := splitColon(rhs)
		labelFirst := hasColon && (nextArrow == -1 || len(colonHead) < nextArrow)
		var toPart string
		switch {
		case labelFirst:
			toPart = colonHead
		case nextArrow == -1:
			toPart = rhs
		default:
			toPart = rhs[:nextArrow]
		}
		var label *string
		if labelFirst {
			label = nonEmpty(decodeHtmlEntities(trimJS(colonRest)))
		}
		tail := ""
		if !labelFirst && nextArrow != -1 {
			tail = rhs[nextArrow:]
		}

		toID, toClasses := takeTags(trimJS(strings.TrimRight(trimEndJS(strings.TrimLeft(trimStartJS(toPart), ">")), "-")))
		if toID == "" {
			return false
		}
		to, ok := stateEndpoint(g, toID, false)
		if !ok {
			return false
		}
		for _, cls := range toClasses {
			g.addClass(to, cls)
		}

		if !g.pushEdge(edge{from: from, to: to, label: label, headTo: headArrow, headFrom: headNone, line: lineSolid}) {
			return true
		}
		prev = to
		rest = tail
	}
	return true
}

// stateEndpoint resolves `[*]`, which is start or end depending on which side
// of the arrow it sits, and is scoped to the enclosing composite: each frame
// has its own start and end.
func stateEndpoint(g *graph, id string, isSource bool) (int, bool) {
	if id == "[*]" {
		scope := ""
		if g.curGroup != nil {
			scope = " g" + itoa(*g.curGroup)
		}
		side := "end"
		if isSource {
			side = "start"
		}
		bullet := "●"
		return g.nodeIndex("[*]"+side+scope, &bullet, shapeRound)
	}
	return g.nodeIndex(id, nil, shapeRound)
}

// parseStateDesc reads `id: description`, or a bare state name; either may
// carry `:::` tags.
func parseStateDesc(st string, g *graph) bool {
	head, desc, hasColon := splitColon(st)
	if !hasColon {
		head = st
	}
	id, classes := takeTags(trimJS(head))
	var idx int
	var ok bool
	if hasColon {
		desc = trimJS(desc)
		if id == "" || containsJSSpace(id) || desc == "" {
			return false
		}
		// Repeated descriptions accumulate (mermaid stacks them as lines; here
		// they join and wrap). A bare node's label is its id: that one replaces.
		text := decodeHtmlEntities(desc)
		if prev, seen := g.index[id]; seen && g.nodes[prev].label != id {
			text = g.nodes[prev].label + " " + text
		}
		idx, ok = g.nodeLabel(id, text)
	} else {
		if id == "" || containsJSSpace(id) {
			return false
		}
		idx, ok = g.nodeIndex(id, nil, shapeRound)
	}
	if !ok {
		return false
	}
	for _, cls := range classes {
		g.addClass(idx, cls)
	}
	return true
}
