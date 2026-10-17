// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// laneCommit is a commit of the work that brought a fix into the integration history.
type laneCommit struct {
	Hash    string
	Subject string
	Message string
	Files   []string
	Shipped bool
	Tokens  map[string]bool
}

// laneOf returns the non-merge commits of the branch that carried commit hash into the first-parent history of HEAD: the commits reachable from hash and not from the first parent of the merge that took it. A commit made directly on the first-parent line has a lane of itself.
func (b *builder) laneOf(hash string) ([]laneCommit, error) {
	if lane, ok := b.lanes[hash]; ok {
		return lane, nil
	}
	lane, err := b.computeLane(hash)
	if err != nil {
		return nil, err
	}
	b.lanes[hash] = lane
	return lane, nil
}

func (b *builder) computeLane(hash string) ([]laneCommit, error) {
	merge, err := b.r.git("rev-list", "--first-parent", "--ancestry-path", "--reverse", hash+"..HEAD")
	if err != nil {
		return nil, err
	}
	rng := hash + "^!"
	if parents, err := b.r.git("rev-list", "--parents", "-n", "1", hash); err == nil && len(strings.Fields(parents)) >= 3 {
		f := strings.Fields(parents)
		return b.laneRange(f[1] + ".." + f[2])
	}
	if m, _, _ := strings.Cut(strings.TrimSpace(merge), "\n"); m != "" {
		// m is the merge that brought the commit into the first-parent line, unless the commit sits on that line itself and m is the merge right after it: then m's first parent is the commit and the range would be empty.
		if parents, err := b.r.git("rev-list", "--parents", "-n", "1", m); err == nil && len(strings.Fields(parents)) >= 3 && !sameCommit(strings.Fields(parents)[1], hash) {
			rng = strings.Fields(parents)[1] + ".." + hash
		}
	}
	if rng == hash+"^!" {
		// The commit sits on the first-parent line, where a lane's commits were applied one by one: its lane is the commits just before it.
		rng = "--first-parent -n " + fmt.Sprint(firstParentWindow) + " " + hash + " ^" + b.base
	}
	return b.laneRange(rng)
}

// sameCommit reports whether two hashes, either of which may be abbreviated, name the same commit.
func sameCommit(a, b string) bool { return strings.HasPrefix(a, b) || strings.HasPrefix(b, a) }

// firstParentWindow bounds how many commits before a first-parent-line commit count as its lane.
const firstParentWindow = 80

func (b *builder) laneRange(rng string) ([]laneCommit, error) {
	args := []string{"log", "--no-merges", "--name-only", "--format=%x02%H%x00%s%x00%b%x01"}
	out, err := b.r.git(append(args, strings.Fields(rng)...)...)
	if err != nil {
		return nil, err
	}
	var lane []laneCommit
	for rec := range strings.SplitSeq(out, "\x02") {
		head, files, ok := strings.Cut(rec, "\x01")
		if !ok {
			continue
		}
		parts := strings.SplitN(head, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		lc := laneCommit{Hash: parts[0], Subject: parts[1], Message: parts[1] + "\n\n" + strings.TrimSpace(parts[2])}
		for f := range strings.SplitSeq(files, "\n") {
			if f = strings.TrimSpace(f); f != "" {
				lc.Files = append(lc.Files, f)
				if shipped(f) {
					lc.Shipped = true
				}
			}
		}
		lc.Tokens = tokens(lc.Message + " " + strings.Join(lc.Files, " "))
		lane = append(lane, lc)
	}
	return lane, nil
}

var wordRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*`)

// tokens returns the lower-case words of s of five or more letters, with identifiers split at their capitals.
func tokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRE.FindAllString(s, -1) {
		for _, part := range splitCamel(w) {
			if len(part) >= 5 {
				out[strings.ToLower(part)] = true
			}
		}
		if len(w) >= 5 {
			lw := strings.ToLower(w)
			out[lw] = true
			// A plural names the same thing as its singular: "titles" in a subject is the note's `title`.
			if len(lw) >= 6 && strings.HasSuffix(lw, "s") && !strings.HasSuffix(lw, "ss") {
				out[lw[:len(lw)-1]] = true
			}
		}
	}
	return out
}

func splitCamel(w string) []string {
	var parts []string
	start := 0
	rs := []rune(w)
	for i := 1; i < len(rs); i++ {
		if unicode.IsUpper(rs[i]) && (unicode.IsLower(rs[i-1]) || (i+1 < len(rs) && unicode.IsLower(rs[i+1]))) {
			parts = append(parts, string(rs[start:i]))
			start = i
		}
	}
	return append(parts, string(rs[start:]))
}

var codeSpan = regexp.MustCompile("`([^`]+)`")

// noteTokens are the words of a release note, each tagged with its weight: a word in a code span names the thing that was fixed and counts double.
func noteTokens(note string) map[string]int {
	out := map[string]int{}
	for t := range tokens(note) {
		out[t] = 1
	}
	for _, m := range codeSpan.FindAllStringSubmatch(note, -1) {
		for t := range tokens(m[1]) {
			out[t] = 2
		}
	}
	return out
}

// relevance scores how much of a release note a lane commit talks about, ignoring words that most notes share.
func relevance(note map[string]int, generic map[string]bool, c laneCommit) int {
	score := 0
	for t, w := range note {
		if c.Tokens[t] && !generic[t] {
			score += w
		}
	}
	return score
}

// genericTokens returns the words that appear in more than a twelfth of the notes. They say nothing about which bug a commit fixes.
func genericTokens(notes []string) map[string]bool {
	df := map[string]int{}
	for _, n := range notes {
		for t := range tokens(n) {
			df[t]++
		}
	}
	out := map[string]bool{}
	for t, n := range df {
		if n*12 > len(notes) {
			out[t] = true
		}
	}
	return out
}
