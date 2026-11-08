// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// `gitGraph`: commit lanes, drawn the way `git log --graph` draws them, newest
// commit on top, one column per branch, connector rows where history splits.
// Ported from lovely-mermaid diagrams/gitgraph.ts. Lenient: an unreadable
// statement is dropped and recorded in warnings.
//
// Connector rows go through the canvas direction bits, so a merge reaching
// across an active lane crosses it with a `┼` instead of erasing it.

type gitCommit struct {
	lane int
	id   string
	tag  *string
	// mergeFrom is the lane this commit merged in, or -1 if it is not a merge commit.
	mergeFrom int
}

type gitRow struct {
	commit bool
	at     int // commit rows: index into commits
	open   bool
	parent int
	lane   int
}

type gitLabelPart struct {
	text string
	role Role
}

var gitgraphHeaders = []string{"gitgraph", "gitgraph:"}

var gitgraphDiagram = diagram{
	kind:    "gitgraph",
	headers: gitgraphHeaders,
	render: func(src string) (*canvas, drawnMeta, bool) {
		branches, commits, forkAt, warnings, ok := parseGitGraph(src)
		if !ok {
			return nil, drawnMeta{}, false
		}
		laneCount := len(branches)

		// The newest commit of each lane wears the branch name.
		headOf := map[int]int{}
		for i, c := range commits {
			headOf[c.lane] = i
		}

		// Rows, top-down: commit rows interleaved with connector rows. `open`
		// hangs a merged lane off its merge commit, `close` returns a forked lane
		// to its parent at its fork point. Lanes never used (a branch with no
		// commits that nothing merged) simply never open.
		var rows []gitRow
		used := make([]bool, len(branches))
		for lane := range branches {
			used[lane] = lane == 0
			for _, c := range commits {
				if c.lane == lane || c.mergeFrom == lane {
					used[lane] = true
					break
				}
			}
		}
		for i, c := range slices.Backward(commits) {

			rows = append(rows, gitRow{commit: true, at: i})
			if c.mergeFrom != -1 {
				rows = append(rows, gitRow{open: true, parent: c.lane, lane: c.mergeFrom})
			}
			// Close outer lanes first so an inner close still sees them as columns.
			for lane := len(branches) - 1; lane > 0; lane-- {
				if forkAt[lane] == i-1 && used[lane] {
					rows = append(rows, gitRow{parent: commits[i-1].lane, lane: lane})
				}
			}
		}

		laneX := func(lane int) int { return lane * 2 }
		graphW := laneCount * 2
		labels := make([][]gitLabelPart, len(rows))
		labelW := 1
		for y, row := range rows {
			if !row.commit {
				labelW = max(labelW, 0)
				continue
			}
			c := commits[row.at]
			var parts []gitLabelPart
			if c.id != "" {
				parts = append(parts, gitLabelPart{c.id, RoleText})
			}
			if headOf[c.lane] == row.at {
				parts = append(parts, gitLabelPart{"(" + branches[c.lane] + ")", RoleEdgeLabel})
			}
			if c.tag != nil {
				parts = append(parts, gitLabelPart{"[" + *c.tag + "]", RoleEdgeLabel})
			}
			if c.mergeFrom != -1 {
				parts = append(parts, gitLabelPart{"⇐ " + branches[c.mergeFrom], RoleEdgeLabel})
			}
			labels[y] = parts
			w := -1
			for _, p := range parts {
				w += stringWidth(p.text) + 1
			}
			labelW = max(labelW, w)
		}
		width := graphW + labelW
		c := newCanvas(width, len(rows))

		// Walk top-down with the set of lanes currently drawn as columns: a lane
		// joins at its newest own commit or its `open` connector, and leaves at
		// its `close`. Everything in the set draws `│` through every other row.
		live := map[int]bool{}
		for y, row := range rows {
			if row.commit {
				cm := commits[row.at]
				live[cm.lane] = true
				for lane := range live {
					if lane != cm.lane {
						c.addBits(laneX(lane), y, bitU|bitD, RoleEdge)
					}
				}
				c.set(laneX(cm.lane), y, "●", RoleEdge)
				x := graphW
				for _, p := range labels[y] {
					drawText(c, p.text, x, y, p.role)
					x += stringWidth(p.text) + 1
				}
				continue
			}
			// The parent keeps its column, the child lane hooks on toward it, and
			// unrelated live lanes cross the horizontal run as `┼` via the bit merge.
			parent, lane := row.parent, row.lane
			rejoins := row.open && live[lane]
			if row.open {
				live[lane] = true
			}
			for l := range live {
				if l != parent && l != lane {
					c.addBits(laneX(l), y, bitU|bitD, RoleEdge)
				}
			}
			// An open hangs off the merge commit directly above; a close bends down
			// into the fork commit below, continuing up only if the parent already
			// had a column here.
			if row.open {
				c.addBits(laneX(parent), y, bitU|bitD, RoleEdge)
			} else {
				bits := bitD
				if live[parent] {
					bits |= bitU
				}
				c.addBits(laneX(parent), y, bits, RoleEdge)
			}
			live[parent] = true
			c.segH(y, laneX(parent), laneX(lane))
			bits := bitU
			if row.open {
				bits = bitD
			}
			if rejoins {
				bits |= bitU
			}
			c.addBits(laneX(lane), y, bits, RoleEdge)
			if !row.open {
				delete(live, lane)
			}
		}

		c.finalizeMask()
		return c, drawnMeta{warnings: warnings, classDefs: map[string]map[string]string{}}, true
	},
}

func parseGitGraph(src string) (branches []string, commits []gitCommit, forkAt []int, warnings []string, ok bool) {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind == "" || !hasHeader(gitgraphHeaders, kind) {
		return nil, nil, nil, nil, false
	}

	const none = -1 // JS null for fork points and heads
	branches = []string{"main"}
	forkAt = []int{none}
	warnings = []string{}
	// Newest commit index per lane: the fork point for branches cut from it.
	heads := []int{none}
	cur := 0
	auto := 0
	truncated := false
	nextAuto := func() string {
		id := "c" + itoa(auto)
		auto++
		return id
	}

	for _, st := range statements[1:] {
		if len(commits) >= maxEdges {
			truncated = true
			break
		}
		w := words(st)
		first, rest := "", trimJS(st)
		if len(w) > 0 {
			first = jsstring.ToLower(w[0])
			rest = trimJS(st[len(w[0]):])
		}
		switch first {
		case "commit":
			attrs := commitAttrs(rest)
			heads[cur] = len(commits)
			id := ""
			if attrs.id != nil {
				id = *attrs.id
			} else {
				id = nextAuto()
			}
			commits = append(commits, gitCommit{lane: cur, id: id, tag: attrs.tag, mergeFrom: none})
		case "branch":
			name, _, hasName := nameToken(rest)
			// The fork point is the current branch's head, not the newest commit.
			fork := heads[cur]
			if fork == none {
				fork = forkAt[cur]
			}
			if !hasName || slices.Contains(branches, name) || fork == none {
				warnings = append(warnings, "dropped, unreadable statement: \""+st+"\"")
				continue
			}
			branches = append(branches, name)
			forkAt = append(forkAt, fork)
			heads = append(heads, none)
			cur = len(branches) - 1
		case "checkout", "switch":
			name, _, _ := nameToken(rest)
			lane := slices.Index(branches, name)
			if lane == -1 {
				warnings = append(warnings, "dropped, unreadable statement: \""+st+"\"")
				continue
			}
			cur = lane
		case "merge":
			name, after, _ := nameToken(rest)
			lane := slices.Index(branches, name)
			if lane == -1 || lane == cur {
				warnings = append(warnings, "dropped, unreadable statement: \""+st+"\"")
				continue
			}
			// An unnamed merge shows no id: the `⇐ branch` marker already says what
			// it is, and an invented id would collide with authored ones.
			attrs := commitAttrs(after)
			heads[cur] = len(commits)
			id := ""
			if attrs.id != nil {
				id = *attrs.id
			}
			commits = append(commits, gitCommit{lane: cur, id: id, tag: attrs.tag, mergeFrom: lane})
		case "cherry-pick":
			attrs := commitAttrs(rest)
			heads[cur] = len(commits)
			var id string
			if attrs.id == nil {
				id = nextAuto()
			} else {
				id = "⟲ " + *attrs.id
			}
			commits = append(commits, gitCommit{lane: cur, id: id, tag: attrs.tag, mergeFrom: none})
		default:
			warnings = append(warnings, "dropped, unreadable statement: \""+st+"\"")
		}
	}
	if truncated {
		warnings = append(warnings, "diagram truncated: commit cap ("+itoa(maxEdges)+") reached")
	}

	if len(commits) == 0 {
		return nil, nil, nil, nil, false
	}
	return branches, commits, forkAt, warnings, true
}

// nameToken reads the first branch-name token; quotes let a name carry spaces
// or keywords. ok=false is JS `undefined` (no token).
func nameToken(rest string) (name, after string, ok bool) {
	if strings.HasPrefix(rest, "\"") {
		if closeAt := strings.Index(rest[1:], "\""); closeAt != -1 {
			return rest[1 : 1+closeAt], rest[closeAt+2:], true
		}
	}
	w := words(rest)
	if len(w) == 0 {
		return "", rest, false
	}
	return w[0], rest[len(w[0]):], true
}

type gitAttrs struct{ id, tag *string }

var gitAttrRe = regexp.MustCompile(`(?i)(id|tag)[` + jsstring.SpaceClass + `]*:[` + jsstring.SpaceClass + `]*"([^"]*)"`)

// commitAttrs reads `id: "x" tag: "v1" ...` key/value pairs trailing a commit
// or merge.
func commitAttrs(rest string) gitAttrs {
	var out gitAttrs
	for _, m := range gitAttrRe.FindAllStringSubmatch(rest, -1) {
		v := fitLabel(cleanLabel(m[2]), maxLabel)
		if strings.EqualFold(m[1], "id") {
			out.id = &v
		} else {
			out.tag = &v
		}
	}
	return out
}
