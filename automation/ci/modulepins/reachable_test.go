// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn runs git in dir with no user or system configuration (no signing, no hooks) and returns its trimmed output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_AUTHOR_DATE=2026-11-08T05:33:02Z", "GIT_COMMITTER_DATE=2026-11-08T05:33:02Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit adds an empty commit with the message and returns its sha.
func commit(t *testing.T, dir, message string) string {
	t.Helper()
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", message)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

func pseudo(sha string) string { return "v0.7.1-0.20261108053302-" + sha[:12] }

// pinnedRepo returns a repository whose root go.mod (committed last) requires the SDK at version.
func pinnedRepo(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	writeModule(t, dir, ".", "module github.com/MichaelKinsy/PiG\n\ngo 1.27.0\n\nrequire "+sdkPath+" "+version+"\n")
	return dir
}

func problemsOf(t *testing.T, dir string) string {
	t.Helper()
	problems, err := checkReachable(dir)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(problems, "\n")
}

func wantNoProblem(t *testing.T, dir string) {
	t.Helper()
	if got := problemsOf(t, dir); got != "" {
		t.Errorf("problems = %q, want none", got)
	}
}

func wantProblem(t *testing.T, dir string, fragments ...string) {
	t.Helper()
	got := problemsOf(t, dir)
	if got == "" {
		t.Fatalf("no problem reported, want one with %q", fragments)
	}
	for _, fragment := range fragments {
		if !strings.Contains(got, fragment) {
			t.Errorf("problem %q lacks %q", got, fragment)
		}
	}
}

// writePin writes the root go.mod at version and commits it.
func writePin(t *testing.T, dir, version string) {
	t.Helper()
	writeModule(t, dir, ".", "module github.com/MichaelKinsy/PiG\n\ngo 1.27.0\n\nrequire "+sdkPath+" "+version+"\n")
	gitIn(t, dir, "add", "go.mod")
	commit(t, dir, "pin "+version)
}

func TestReachablePseudoVersionOnTheBranchPasses(t *testing.T) {
	dir := pinnedRepo(t, "v0.7.0")
	gitIn(t, dir, "init", "-q", "-b", "line")
	below := commit(t, dir, "the commit the pin names")
	writePin(t, dir, pseudo(below))
	wantNoProblem(t, dir)
}

func TestReachablePseudoVersionOfAnotherLineFails(t *testing.T) {
	dir := pinnedRepo(t, "v0.7.0")
	gitIn(t, dir, "init", "-q", "-b", "line")
	commit(t, dir, "base")
	gitIn(t, dir, "checkout", "-q", "-b", "other")
	other := commit(t, dir, "a commit of another line")
	gitIn(t, dir, "checkout", "-q", "line")
	writePin(t, dir, pseudo(other))
	// The commit is in the repository (every branch is fetched) and public, yet it is not this line's history.
	wantProblem(t, dir, "is not an ancestor of HEAD", other[:12], "another line")
}

func TestReachablePseudoVersionAbsentFromTheRepositoryFails(t *testing.T) {
	dir := pinnedRepo(t, "v0.7.0")
	gitIn(t, dir, "init", "-q", "-b", "line")
	commit(t, dir, "base")
	writePin(t, dir, "v0.7.1-0.20261108053302-0123456789ab")
	// A squash merge or a force-push leaves exactly this: the pin names a commit the history no longer has.
	wantProblem(t, dir, "0123456789ab", "is not in this repository", "squash merge")
}

func TestReachableTaggedVersion(t *testing.T) {
	dir := pinnedRepo(t, "v0.7.0")
	gitIn(t, dir, "init", "-q", "-b", "line")
	tagged := commit(t, dir, "the released commit")
	gitIn(t, dir, "tag", "-a", "-m", "extensions/sdk v0.7.0", "extensions/sdk/v0.7.0", tagged)
	writePin(t, dir, "v0.7.0")
	wantNoProblem(t, dir)

	// A tag that the checkout does not have.
	writePin(t, dir, "v0.9.0")
	wantProblem(t, dir, "tag extensions/sdk/v0.9.0 is not in this checkout", "git fetch --tags")

	// A tag on a commit of another line.
	gitIn(t, dir, "checkout", "-q", "-b", "other", tagged)
	elsewhere := commit(t, dir, "released on another line")
	gitIn(t, dir, "tag", "-a", "-m", "extensions/sdk v0.8.0", "extensions/sdk/v0.8.0", elsewhere)
	gitIn(t, dir, "checkout", "-q", "line")
	writePin(t, dir, "v0.8.0")
	wantProblem(t, dir, "tag extensions/sdk/v0.8.0", elsewhere[:12], "is not an ancestor of HEAD")
}

func TestReachableShallowCheckoutNeverPasses(t *testing.T) {
	full := pinnedRepo(t, "v0.7.0")
	gitIn(t, full, "init", "-q", "-b", "line")
	below := commit(t, full, "the commit the pin names")
	commit(t, full, "a later commit")
	gitIn(t, full, "checkout", "-q", "-b", "other", below)
	other := commit(t, full, "a commit of another line")
	gitIn(t, full, "checkout", "-q", "line")
	writePin(t, full, pseudo(below))
	wantNoProblem(t, full)

	// Depth 1: the pin's commit is two commits down, so the checkout does not have it.
	shallow := filepath.Join(t.TempDir(), "shallow")
	gitIn(t, filepath.Dir(shallow), "clone", "-q", "--depth", "1", "--branch", "line", "file://"+full, shallow)
	if got := gitIn(t, shallow, "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Fatalf("clone is not shallow: %q", got)
	}
	wantProblem(t, shallow, below[:12], "is not in this checkout, which is shallow", "fetch-depth: 0")

	// Depth 1 with every branch tip: the pin's commit is present as the tip of another branch, and the shallow history cannot say whether HEAD has it.
	writePin(t, full, pseudo(other))
	tips := filepath.Join(t.TempDir(), "tips")
	gitIn(t, filepath.Dir(tips), "clone", "-q", "--depth", "1", "--no-single-branch", "--branch", "line", "file://"+full, tips)
	wantProblem(t, tips, other[:12], "shallow checkout, which cannot show whether it is an ancestor")

	// After the full history is fetched the same checkout gives the real answer.
	gitIn(t, tips, "fetch", "-q", "--unshallow")
	wantProblem(t, tips, "is not an ancestor of HEAD", "another line")
}

func TestReachableOutsideAGitCheckoutFails(t *testing.T) {
	dir := pinnedRepo(t, "v0.7.0")
	wantProblem(t, dir, "is not a git checkout")
}
