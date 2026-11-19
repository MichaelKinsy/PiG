// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// GitHub's "Update branch" merges the base branch into a PR's branch with a commit of GitHub's own: two parents, committed
// by GitHub <noreply@github.com>, signed by GitHub, and without a Signed-off-by line, which nobody can add to it. Such a
// commit needs no sign-off when it is exactly that merge and holds nothing of its own. The committer line and, on a fork,
// the signature are the PR author's to write, so the exemption does not rest on them: the second parent must be on the
// base branch (the merge brought the base in, and every commit the PR itself adds is still checked), and the commit's tree
// must be the tree git's own merge of the two parents writes (no conflict resolution, no edit).
const (
	webFlowName  = "GitHub"
	webFlowEmail = "noreply@github.com"

	treeClean    = "clean"
	treeConflict = "conflict"
	treeEdited   = "edited"

	exemptUpdateBranch = "exempt-update-branch-merges"
)

// mergeFacts is what git says of a merge that has the shape of an update-branch merge. The API does not send it.
type mergeFacts struct {
	// OnBase: the second parent is reachable from the tip of the PR's base branch.
	OnBase bool `json:"on_base"`
	// Tree is treeClean when the commit's tree is the one git's merge of its two parents writes, treeConflict when that
	// merge conflicts, treeEdited when it writes another tree. Empty when the second parent is not on the base.
	Tree string `json:"tree,omitempty"`
	// Err is why git could not tell.
	Err string `json:"err,omitempty"`
}

func (c prCommit) byWebFlow() bool {
	return c.Commit.Committer.Name == webFlowName && c.Commit.Committer.Email == webFlowEmail
}

// updateBranchShaped is what the API alone says of an update-branch merge; git decides the rest (mergeFactsOf).
func (c prCommit) updateBranchShaped() bool {
	return len(c.Parents) == 2 && c.byWebFlow() && c.Commit.Verification.Verified
}

// updateBranchMerge reports whether c is GitHub's update-branch merge of the base branch with nothing of its own. For
// another merge, why names the condition it misses; why is empty for a commit with one parent. err is git's failure to
// decide.
func updateBranchMerge(c prCommit, base string) (exempt bool, why string, err error) {
	switch {
	case len(c.Parents) < 2:
		return false, "", nil
	case len(c.Parents) > 2:
		return false, fmt.Sprintf("a merge of %d parents, not of two", len(c.Parents)), nil
	case !c.byWebFlow():
		return false, fmt.Sprintf("a merge committed by %q <%s>, not by GitHub's update-branch (%s <%s>)", c.Commit.Committer.Name, c.Commit.Committer.Email, webFlowName, webFlowEmail), nil
	case !c.Commit.Verification.Verified:
		return false, fmt.Sprintf("a merge in GitHub's name whose signature GitHub does not verify (%s)", c.Commit.Verification.Reason), nil
	case c.Merge == nil:
		return false, "", errors.New("git was not asked about it")
	case c.Merge.Err != "":
		return false, "", errors.New(c.Merge.Err)
	case !c.Merge.OnBase:
		return false, fmt.Sprintf("a merge whose second parent %s is not on %s", short(c.Parents[1].SHA), base), nil
	case c.Merge.Tree == treeConflict:
		return false, "a merge with a conflict resolution: git's merge of its two parents conflicts", nil
	case c.Merge.Tree != treeClean:
		return false, "a merge with an edit of its own: its tree is not the tree of git's merge of its two parents", nil
	}
	return true, "", nil
}

// oneLine is git's error as a check's reason takes it: one line, clipped.
func oneLine(s string) string {
	return clip(strings.Join(strings.Fields(s), " "), 300)
}

func mergeFactsOf(ctx context.Context, ws workspace, dir string, c prCommit) *mergeFacts {
	first, second := c.Parents[0].SHA, c.Parents[1].SHA
	out, err := git(ctx, "-C", dir, "merge-base", "--is-ancestor", second, "refs/preflight/base")
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 1:
		return &mergeFacts{}
	default:
		return &mergeFacts{Err: oneLine(fmt.Sprintf("git merge-base: %v: %s", err, out))}
	}
	facts := &mergeFacts{OnBase: true}
	want, clean, err := mergeTreeOf(ctx, ws, dir, first, second)
	switch {
	case err != nil:
		facts.Err = oneLine(err.Error())
		return facts
	case !clean:
		facts.Tree = treeConflict
		return facts
	}
	got, err := git(ctx, "-C", dir, "rev-parse", "--verify", c.SHA+"^{tree}")
	switch {
	case err != nil:
		facts.Err = oneLine(fmt.Sprintf("git rev-parse: %v: %s", err, got))
	case strings.TrimSpace(got) == want:
		facts.Tree = treeClean
	default:
		facts.Tree = treeEdited
	}
	return facts
}

// askGit answers from the PR's commits what the API did not: whether the head merges into the base branch, when GitHub
// has not computed that yet, and what each merge shaped like an update-branch merge holds.
func askGit(ctx context.Context, ws workspace, d *data, log io.Writer) {
	var merges []int
	for i, c := range d.Commits {
		if c.updateBranchShaped() {
			merges = append(merges, i)
		}
	}
	if d.Pull.Mergeable != nil && len(merges) == 0 {
		return
	}
	dir, repoErr := mergeRepo(ctx, ws, d)
	if d.Pull.Mergeable == nil {
		m, err := false, repoErr
		if err == nil {
			m, err = mergeable(ctx, ws, dir)
		}
		if err != nil {
			logf(log, "mergeable: git merge-tree could not tell: %v", err)
		} else {
			d.Pull.Mergeable = &m
			logf(log, "mergeable from git merge-tree: %v", m)
		}
	}
	for _, i := range merges {
		c := &d.Commits[i]
		if repoErr != nil {
			c.Merge = &mergeFacts{Err: oneLine(repoErr.Error())}
		} else {
			c.Merge = mergeFactsOf(ctx, ws, dir, *c)
		}
		logf(log, "merge %s by GitHub: second parent on %s %v, tree %q, error %q", short(c.SHA), d.Pull.Base.Ref, c.Merge.OnBase, c.Merge.Tree, c.Merge.Err)
	}
}
