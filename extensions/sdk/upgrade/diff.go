// SPDX-License-Identifier: MIT

package upgrade

import (
	"fmt"
	"strings"
)

const diffContext = 3

// Diff returns the unified diff of a file's rewrite, or the empty string when
// the file is unchanged. name labels both sides.
func Diff(name string, before, after []byte) string {
	if string(before) == string(after) {
		return ""
	}
	old, updated := splitLines(string(before)), splitLines(string(after))
	ops := diffLines(old, updated)
	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", name, name)
	for _, hunk := range hunks(ops) {
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", hunk.oldStart, hunk.oldLen, hunk.newStart, hunk.newLen)
		for _, op := range hunk.ops {
			out.WriteString(op.kind)
			out.WriteString(op.text)
			out.WriteString("\n")
		}
	}
	return out.String()
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

type diffOp struct {
	kind string // " ", "-" or "+"
	text string
}

// diffLines is Myers' O(ND) shortest edit script.
func diffLines(a, b []string) []diffOp {
	n, m := len(a), len(b)
	limit := n + m
	offset := limit
	trace := make([][]int, 0, limit+1)
	frontier := make([]int, 2*limit+2)
	for d := 0; d <= limit; d++ {
		snapshot := make([]int, len(frontier))
		copy(snapshot, frontier)
		trace = append(trace, snapshot)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && frontier[offset+k-1] < frontier[offset+k+1]) {
				x = frontier[offset+k+1]
			} else {
				x = frontier[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			frontier[offset+k] = x
			if x >= n && y >= m {
				return backtrack(a, b, trace, d, offset)
			}
		}
	}
	return nil
}

func backtrack(a, b []string, trace [][]int, last, offset int) []diffOp {
	var reversed []diffOp
	x, y := len(a), len(b)
	for d := last; d > 0; d-- {
		frontier := trace[d]
		k := x - y
		var previous int
		if k == -d || (k != d && frontier[offset+k-1] < frontier[offset+k+1]) {
			previous = k + 1
		} else {
			previous = k - 1
		}
		prevX := frontier[offset+previous]
		prevY := prevX - previous
		for x > prevX && y > prevY {
			reversed = append(reversed, diffOp{" ", a[x-1]})
			x--
			y--
		}
		if x == prevX {
			reversed = append(reversed, diffOp{"+", b[y-1]})
			y--
		} else {
			reversed = append(reversed, diffOp{"-", a[x-1]})
			x--
		}
	}
	for x > 0 && y > 0 {
		reversed = append(reversed, diffOp{" ", a[x-1]})
		x--
		y--
	}
	ops := make([]diffOp, len(reversed))
	for i, op := range reversed {
		ops[len(reversed)-1-i] = op
	}
	return ops
}

type hunk struct {
	oldStart, oldLen, newStart, newLen int
	ops                                []diffOp
}

// hunks groups the edit script into hunks with diffContext lines around each change.
func hunks(ops []diffOp) []hunk {
	var result []hunk
	i := 0
	oldLine, newLine := 1, 1
	for i < len(ops) {
		if ops[i].kind == " " {
			i++
			oldLine++
			newLine++
			continue
		}
		start := i
		for back := 0; back < diffContext && start > 0 && ops[start-1].kind == " "; back++ {
			start--
		}
		leading := i - start
		current := hunk{oldStart: oldLine - leading, newStart: newLine - leading}
		end := i
		quiet := 0
		for end < len(ops) {
			if ops[end].kind == " " {
				quiet++
				if quiet > 2*diffContext {
					break
				}
			} else {
				quiet = 0
			}
			end++
		}
		stop := end
		trailing := 0
		for stop > i && ops[stop-1].kind == " " {
			stop--
			trailing++
		}
		stop += min(trailing, diffContext)
		current.ops = ops[start:stop]
		for _, op := range current.ops {
			if op.kind != "+" {
				current.oldLen++
			}
			if op.kind != "-" {
				current.newLen++
			}
		}
		result = append(result, current)
		for _, op := range ops[i:stop] {
			if op.kind != "+" {
				oldLine++
			}
			if op.kind != "-" {
				newLine++
			}
		}
		i = stop
	}
	return result
}
