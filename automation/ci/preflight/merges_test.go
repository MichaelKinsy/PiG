// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type identity struct{ name, email string }

var (
	ann     = identity{"Ann", "ann@example.com"}
	webFlow = identity{"GitHub", "noreply@github.com"}
)

// mergeHistory is a repository the pre-flight fetches from: main (root, then tip, which changes line 3 of f and adds g) and
// the commits a test puts on a PR's branch.
type mergeHistory struct {
	t         *testing.T
	remote    string
	repo      string
	index     string
	root, tip string
}

func newMergeHistory(t *testing.T) *mergeHistory {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	h := &mergeHistory{t: t, remote: t.TempDir(), index: filepath.Join(t.TempDir(), "index")}
	h.repo = filepath.Join(h.remote, "o", "r")
	if err := os.MkdirAll(h.repo, 0o755); err != nil {
		t.Fatal(err)
	}
	h.git(ann, "", "init", "-q", "-b", "main")
	h.git(ann, "", "config", "uploadpack.allowFilter", "true")
	h.root = h.commit(ann, "", "root: f\n\nSigned-off-by: Ann <ann@example.com>", "f", "1\n2\n3\n")
	h.tip = h.commit(ann, h.root, "main: three\n\nSigned-off-by: Ann <ann@example.com>", "f", "1\n2\nthree\n", "g", "main\n")
	h.git(ann, "", "update-ref", "refs/heads/main", h.tip)
	return h
}

// git runs with Ann as the author and who as the committer, as update-branch keeps the PR's author and commits as GitHub.
// It works on objects and an index of its own: the repository has no checked-out files.
func (h *mergeHistory) git(who identity, stdin string, args ...string) string {
	h.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", h.repo, "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(childEnv(), "GIT_INDEX_FILE="+h.index, "GIT_AUTHOR_NAME="+ann.name, "GIT_AUTHOR_EMAIL="+ann.email, "GIT_COMMITTER_NAME="+who.name, "GIT_COMMITTER_EMAIL="+who.email)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit puts the files (name, text, ...) on top of parent (the first commit when parent is empty) and returns the commit.
func (h *mergeHistory) commit(who identity, parent, message string, files ...string) string {
	h.t.Helper()
	if parent == "" {
		return h.commitTree(who, message, h.tree("", files...))
	}
	return h.commitTree(who, message, h.tree(parent, files...), parent)
}

// tree is the tree of the commit (the empty tree when it is empty) with the files written over it.
func (h *mergeHistory) tree(commit string, files ...string) string {
	h.t.Helper()
	if commit == "" {
		h.git(ann, "", "read-tree", "--empty")
	} else {
		h.git(ann, "", "read-tree", commit)
	}
	for i := 0; i < len(files); i += 2 {
		blob := h.git(ann, files[i+1], "hash-object", "-w", "--stdin")
		h.git(ann, "", "update-index", "--add", "--cacheinfo", "100644,"+blob+","+files[i])
	}
	return h.git(ann, "", "write-tree")
}

func (h *mergeHistory) commitTree(who identity, message, tree string, parents ...string) string {
	h.t.Helper()
	args := []string{"commit-tree", tree, "-m", message}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	return h.git(who, "", args...)
}

// cleanMerge is the tree git's merge of the two commits writes.
func (h *mergeHistory) cleanMerge(ours, theirs string) string {
	h.t.Helper()
	return h.git(ann, "", "merge-tree", "--write-tree", ours, theirs)
}

// pull is the fake's PR for the head: the commits between main and the head as the API lists them, each verified but the
// ones named unverified.
func (h *mergeHistory) pull(pr int, head string, unverified ...string) fakePull {
	h.t.Helper()
	h.git(ann, "", "update-ref", "refs/pull/"+strconv.Itoa(pr)+"/head", head)
	var list []map[string]any
	for sha := range strings.FieldsSeq(h.git(ann, "", "rev-list", "--reverse", "--topo-order", h.tip+".."+head)) {
		fields := strings.SplitN(h.git(ann, "", "show", "-s", "--format=%P%x00%cn%x00%ce%x00%B", sha), "\x00", 4)
		var ps []map[string]string
		for p := range strings.FieldsSeq(fields[0]) {
			ps = append(ps, map[string]string{"sha": p})
		}
		verification := map[string]any{"verified": true, "reason": "valid"}
		if slices.Contains(unverified, sha) {
			verification = map[string]any{"verified": false, "reason": "unsigned"}
		}
		list = append(list, map[string]any{"sha": sha, "parents": ps, "commit": map[string]any{
			"message": fields[3], "committer": map[string]string{"name": fields[1], "email": fields[2]}, "verification": verification,
		}})
	}
	raw, err := json.Marshal(list)
	if err != nil {
		h.t.Fatal(err)
	}
	return fakePull{head: head, count: len(list), commits: string(raw)}
}

// GitHub's "Update branch" merges main into a PR's branch with a commit nobody can sign off. The shipped signoff check
// accepts that commit, and only that one: two parents, committed and verified by GitHub, the second parent on main, and the
// tree git's own merge of the two parents writes. Every case is a PR of real commits that the pre-flight fetches.
func TestSignoffExemptsOnlyGitHubsUpdateBranchMerge(t *testing.T) {
	h := newMergeHistory(t)
	const signedOff = "\n\nSigned-off-by: Ann <ann@example.com>"
	const merge = "Merge branch 'main' into topic"
	work := h.commit(ann, h.root, "topic: h"+signedOff, "h", "topic\n")
	plain := h.commit(ann, work, "topic: i, without the line", "i", "topic\n")
	lone := h.commit(ann, h.root, "topic: j, without the line", "j", "topic\n")
	side := h.commit(ann, h.root, "side: k"+signedOff, "k", "side\n")
	clash := h.commit(ann, h.root, "topic: drei"+signedOff, "f", "1\n2\ndrei\n")
	clean := h.cleanMerge(work, h.tip)
	edited := h.tree(h.commitTree(ann, "the clean merge", clean, work, h.tip), "extra", "added in the merge\n")
	resolved := h.tree(clash, "g", "main\n")

	updateBranch := h.commitTree(webFlow, merge, clean, work, h.tip)
	byAnn := h.commitTree(ann, merge, clean, work, h.tip)
	ofSide := h.commitTree(webFlow, "Merge branch 'side' into topic", h.cleanMerge(work, side), work, side)
	intoMain := h.commitTree(webFlow, "Merge branch 'topic' into main", clean, h.tip, work)
	withEdit := h.commitTree(webFlow, merge, edited, work, h.tip)
	withResolution := h.commitTree(webFlow, merge, resolved, clash, h.tip)
	afterPlain := h.commitTree(webFlow, merge, h.cleanMerge(plain, h.tip), plain, h.tip)
	octopus := h.commitTree(webFlow, merge, clean, work, h.tip, side)
	afterLone := h.commitTree(webFlow, merge, h.cleanMerge(lone, h.tip), lone, h.tip)

	cases := []struct {
		name   string
		head   string
		result string
		// names are texts the signoff line of the report holds, not texts it must not hold.
		names, not []string
		unverified []string
	}{
		{name: "a clean update-branch merge of main", head: updateBranch, result: resultPass, names: []string{"GitHub's update-branch merge of main"}},
		{name: "b the same merge committed by someone else", head: byAnn, result: resultFail, names: []string{short(byAnn), `committed by "Ann" <ann@example.com>, not by GitHub's update-branch`}, not: []string{short(work)}},
		{name: "c a second parent that is not on main", head: ofSide, result: resultFail, names: []string{short(ofSide), "second parent " + short(side) + " is not on main"}, not: []string{short(work)}},
		{name: "c the branch as second parent of a merge into main", head: intoMain, result: resultFail, names: []string{short(intoMain), "second parent " + short(work) + " is not on main"}},
		{name: "d a merge of main with an edit of its own", head: withEdit, result: resultFail, names: []string{short(withEdit), "its tree is not the tree of git's merge of its two parents"}},
		{name: "e a merge of main that resolves a conflict", head: withResolution, result: resultFail, names: []string{short(withResolution), "git's merge of its two parents conflicts"}},
		{name: "f a commit without the line beside an exempt merge", head: afterPlain, result: resultFail, names: []string{"commit(s) " + short(plain) + ": add"}, not: []string{short(afterPlain), short(work)}},
		{name: "g an octopus merge", head: octopus, result: resultFail, names: []string{short(octopus), "a merge of 3 parents"}},
		{name: "h a commit without the line before the exempt merge", head: afterLone, result: resultFail, names: []string{"commit(s) " + short(lone) + ": add"}, not: []string{short(afterLone)}},
		{name: "a merge in GitHub's name that GitHub does not verify", head: updateBranch, result: resultFail, names: []string{short(updateBranch), "signature GitHub does not verify"}, unverified: []string{updateBranch}},
	}

	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Checks = slices.DeleteFunc(cfg.Checks, func(c check) bool { return c.Name != "signoff" })
	if len(cfg.Checks) != 1 {
		t.Fatalf("config.yaml has %d signoff checks", len(cfg.Checks))
	}
	bodies := map[int]string{}
	pulls := map[int]fakePull{}
	for i, tc := range cases {
		bodies[i+1] = "x"
		pulls[i+1] = h.pull(i+1, tc.head, tc.unverified...)
	}
	g := newFakeGitHub(bodies)
	g.pulls = pulls
	server := httptest.NewServer(g)
	defer server.Close()
	dir := t.TempDir()
	o := runOptions{apiBase: server.URL, gitBase: "file://" + h.remote, token: "t", repo: "o/r", root: dir, bin: dir, work: filepath.Join(dir, "work"), dryRun: true}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var log strings.Builder
			result, _, err := runPR(context.Background(), cfg, &ruleEnv{root: dir}, o, "v", pending{pr: i + 1}, nil, nil, &log)
			if err != nil {
				t.Fatalf("%v\n%s", err, log.String())
			}
			var line string
			for l := range strings.Lines(log.String()) {
				if strings.Contains(l, " signoff ") && !strings.HasPrefix(l, "::") {
					line = l
				}
			}
			if result != tc.result {
				t.Errorf("result %s, want %s: %s", result, tc.result, line)
			}
			for _, want := range tc.names {
				if !strings.Contains(line, want) {
					t.Errorf("the signoff line does not name %q: %s", want, line)
				}
			}
			for _, not := range tc.not {
				if strings.Contains(line, not) {
					t.Errorf("the signoff line names %q: %s", not, line)
				}
			}
		})
	}
}

// When git cannot be asked (the remote is gone), a merge shaped like an update-branch merge is neither exempt nor a
// failure: the check is an error, which the next run decides again.
func TestSignoffIsUndecidedWhenGitCannotTellAboutAMerge(t *testing.T) {
	h := newMergeHistory(t)
	work := h.commit(ann, h.root, "topic: h\n\nSigned-off-by: Ann <ann@example.com>", "h", "topic\n")
	head := h.commitTree(webFlow, "Merge branch 'main' into topic", h.cleanMerge(work, h.tip), work, h.tip)
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Checks = slices.DeleteFunc(cfg.Checks, func(c check) bool { return c.Name != "signoff" })
	g := newFakeGitHub(map[int]string{1: "x"})
	g.pulls = map[int]fakePull{1: h.pull(1, head)}
	server := httptest.NewServer(g)
	defer server.Close()
	dir := t.TempDir()
	o := runOptions{apiBase: server.URL, gitBase: "file://" + filepath.Join(dir, "no-such-remote"), token: "t", repo: "o/r", root: dir, bin: dir, work: filepath.Join(dir, "work"), dryRun: true}
	var log strings.Builder
	result, _, err := runPR(context.Background(), cfg, &ruleEnv{root: dir}, o, "v", pending{pr: 1}, nil, nil, &log)
	if err != nil || result == resultPass || !strings.Contains(log.String(), "ERROR signoff ") || !strings.Contains(log.String(), "git could not tell whether commit(s) "+short(head)) {
		t.Fatalf("result %s, err %v, log:\n%s", result, err, log.String())
	}
}

func TestAllCommitsParametersAreValidated(t *testing.T) {
	for _, tc := range []struct {
		with params
		ok   bool
	}{
		{params{"message": "x", "help": "h", exemptUpdateBranch: true}, true},
		{params{"message": "x", "help": "h", exemptUpdateBranch: "yes"}, false},
		{params{"verified": true, "help": "h", exemptUpdateBranch: true}, false},
	} {
		if err := validateAllCommits(tc.with); (err == nil) != tc.ok {
			t.Errorf("%v: %v", tc.with, err)
		}
	}
}
