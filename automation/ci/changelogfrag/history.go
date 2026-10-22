// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ledgerSections are the fragment sections the bug ledger (automation/ci/bugledger) reads: it turns each bullet of one
// into a ledger entry and attributes the tests of the commits it finds for it. Keep it equal to the sections the
// ledger's scanner reads by default.
var ledgerSections = []string{"Fixed"}

// fixMarker is the marker the bug ledger reads at the end of a bullet: "<!-- fix: <commit>[, <commit>...] -->". It is
// the pattern of automation/ci/bugledger/changelog.go.
var fixMarker = regexp.MustCompile(`\s*<!--\s*fix:\s*([0-9a-f][0-9a-f ,]*?)\s*-->`)

// nameSHA is the commit a fragment named <lane>-<sha>.md claims to describe.
var nameSHA = regexp.MustCompile(`-([0-9a-f]{7,40})\.md$`)

// minSHA is the fewest hex digits of a commit a marker may name; fewer are ambiguous in a repository of this size.
const minSHA = 7

const gitTimeout = 60 * time.Second

// fixTrailer is the trailer a squash commit carries for each commit it replaced: "Fix-Commit: <full sha>", the 40 hex
// digits of a commit a fragment's name or fix marker may still name after the squash removed it from the history. An
// abbreviated sha does not count: only the full name identifies one commit.
var fixTrailer = regexp.MustCompile(`(?m)^Fix-Commit: ([0-9a-f]{40})[ \t]*$`)

// history answers which commits are reachable from HEAD in the repository that holds the fragments.
type history struct {
	dir       string
	reachable map[string]bool
	// replaced maps the full sha a Fix-Commit trailer names to the commit of HEAD's history that carries the trailer.
	replaced map[string]string
}

func (h *history) git(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", h.dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// openHistory reads the commits reachable from HEAD. A shallow clone lacks the commits a fragment names, so it fails
// instead of reporting them unreachable.
func openHistory(dir string) (*history, error) {
	h := &history{dir: dir, reachable: map[string]bool{}, replaced: map[string]string{}}
	shallow, err := h.git("rev-parse", "--is-shallow-repository")
	if err != nil {
		return nil, fmt.Errorf("the fragment checks resolve the commits fragments name against the history of HEAD, so %s must be in a git checkout: %w", dir, err)
	}
	if strings.TrimSpace(shallow) == "true" {
		return nil, fmt.Errorf("the fragment checks resolve the commits fragments name against the history of HEAD, and this clone is shallow; fetch the full history (git fetch --unshallow, or fetch-depth: 0 in CI)")
	}
	all, err := h.git("rev-list", "HEAD")
	if err != nil {
		return nil, err
	}
	for c := range strings.SplitSeq(all, "\n") {
		if c = strings.TrimSpace(c); c != "" {
			h.reachable[c] = true
		}
	}
	trailed, err := h.git("log", "HEAD", "--grep=^Fix-Commit: ", "--format=%H%x00%B%x01")
	if err != nil {
		return nil, err
	}
	for entry := range strings.SplitSeq(trailed, "\x01") {
		commit, body, ok := strings.Cut(strings.TrimSpace(entry), "\x00")
		if !ok {
			continue
		}
		for _, m := range fixTrailer.FindAllStringSubmatch(body, -1) {
			if _, taken := h.replaced[m[1]]; !taken {
				h.replaced[m[1]] = commit
			}
		}
	}
	return h, nil
}

// reaches reports whether rev names a commit that HEAD reaches: a commit reachable from HEAD, or one a squash commit of
// HEAD's history replaced and names in a Fix-Commit trailer.
func (h *history) reaches(rev string) bool {
	out, err := h.git("rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err == nil && h.reachable[strings.TrimSpace(out)] {
		return true
	}
	return h.replacedBy(rev) != ""
}

// replacedBy returns the commit of HEAD's history whose Fix-Commit trailer names the commit that the hex prefix rev
// abbreviates, or "" when no trailer names it, or when the prefix fits the full sha of more than one replaced commit.
func (h *history) replacedBy(rev string) string {
	if len(rev) < minSHA {
		return ""
	}
	found := ""
	for full := range h.replaced {
		if strings.HasPrefix(full, rev) {
			if found != "" && found != full {
				return ""
			}
			found = full
		}
	}
	return h.replaced[found]
}

// checkHistory reports the fragments whose name or fix markers name a commit that HEAD does not reach, and the bullets
// of a section the bug ledger reads that carry no marker. A legacy fragment is exempt: its commits were squashed away
// before the rule, so no commit reachable from HEAD can name them.
func checkHistory(dir string, fragments []fragment) []error {
	var checked []fragment
	for _, f := range fragments {
		if !f.legacy {
			checked = append(checked, f)
		}
	}
	if len(checked) == 0 {
		return nil
	}
	h, err := openHistory(dir)
	if err != nil {
		return []error{err}
	}
	sort.Slice(checked, func(i, j int) bool { return checked[i].name < checked[j].name })
	var problems []error
	for _, f := range checked {
		if m := nameSHA.FindStringSubmatch(f.name); m != nil && !h.reaches(m[1]) {
			lane := strings.TrimSuffix(f.name, m[0])
			problems = append(problems, fmt.Errorf("%s: the name <lane>-<sha>.md names %s, which is not a commit reachable from HEAD (a rebase, squash or amend replaced it, so its SHA is gone from this branch, and no commit of its history carries a \"Fix-Commit: <full sha>\" trailer for it); rename the fragment after the commit it describes on this branch: git mv %s/%s %s/%s-<sha>.md, with <sha> from git rev-parse --short=10 <commit>", f.name, m[1], dir, f.name, dir, lane))
		}
		for _, section := range ledgerSections {
			for _, item := range f.items[section] {
				problems = append(problems, checkMarker(h, f.name, section, item)...)
			}
		}
	}
	return problems
}

// checkMarker checks one bullet of a section the ledger reads.
func checkMarker(h *history, name, section, item string) []error {
	text := abbreviate(strings.Join(strings.Fields(fixMarker.ReplaceAllString(item, "")), " "))
	matches := fixMarker.FindAllStringSubmatch(item, -1)
	if len(matches) == 0 {
		return []error{fmt.Errorf("%s: the \"### %s\" bullet %q has no fix marker; the bug ledger reads it and, without a marker, guesses its commits from the history, which can credit a squash commit and thousands of its tests to this bullet. End the bullet with <!-- fix: <sha> --> naming the commit that fixes it (git log --oneline -- <the code it changes>; git rev-parse --short=10 <commit>)", name, section, text)}
	}
	var problems []error
	for _, m := range matches {
		for sha := range strings.FieldsFuncSeq(m[1], func(r rune) bool { return r == ',' || r == ' ' }) {
			switch {
			case len(sha) < minSHA:
				problems = append(problems, fmt.Errorf("%s: the fix marker %q on the \"### %s\" bullet %q names fewer than %d hex digits of a commit; name the commit with git rev-parse --short=10 <commit>", name, sha, section, text, minSHA))
			case !h.reaches(sha):
				problems = append(problems, fmt.Errorf("%s: the fix marker %s on the \"### %s\" bullet %q names no commit reachable from HEAD (a rebase, squash or amend replaced it, so its SHA is gone from this branch, and no commit of its history carries a \"Fix-Commit: <full sha>\" trailer for it); name the commit that replaced it: git log --oneline -- <the code the bullet describes>", name, sha, section, text))
			}
		}
	}
	return problems
}
