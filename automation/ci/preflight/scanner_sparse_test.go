// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case e.IsDir() && e.Name() == ".git":
			return filepath.SkipDir
		case !e.IsDir():
			rel, _ := filepath.Rel(root, p)
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	return names
}

// A PR names its files, and git reads a sparse-checkout argument as a pattern. Each name of a PR checks out that one
// file: never a sibling its characters would match as a pattern, and never the whole tree.
func TestAPRsFileNamesCheckOutOnlyThoseFiles(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	named := []string{"*", "!x", "#y", "a[b]", " trailing ", "dir/**/z", "q?.txt", `back\slash`, "plain.go"}
	siblings := []string{"innocent.go", "x", "y", "ab", "b", " trailing", "trailing", "dir/z", "dir/sub/z", "dir/sub/deep/z", "qx.txt", "backslash", "more/*.txt", "more/other.txt"}
	files := map[string]string{}
	for _, name := range slices.Concat(named, siblings) {
		files[name] = "content of " + name
	}
	remote := t.TempDir()
	head := gitRepo(t, remote, files)
	dir := t.TempDir()
	ws := workspace{dir: dir, added: filepath.Join(dir, "added"), head: filepath.Join(dir, "head"), tree: filepath.Join(dir, "tree"), main: t.TempDir(), remote: "file://" + remote}
	d := &data{Repo: "o/r", PR: 1}
	for _, name := range named {
		d.Files = append(d.Files, prFile{Filename: name, Status: "added", Patch: "@@ -0,0 +1 @@\n+content of " + name})
	}
	d.Pull.Head.SHA = head
	scans := []check{scanCheck(t, scanSpec{Input: inputChanged, Run: []string{"x"}, Exit: exitCodes{Clean: []int{0}}, Report: reportSpec{In: "stdout", Format: "lines", Pattern: `(?P<message>.+)`}})}
	if err := prepareWorkspace(context.Background(), d, scans, ws, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := slices.Sorted(slices.Values(named))
	if got := filesUnder(t, ws.head); !slices.Equal(got, want) {
		t.Errorf("checked out %q,\nwant only the PR's files %q", got, want)
	}

	// The content checks add paths of the head's own listing to the same checkout.
	if err := checkOut(ws.head, map[string][]string{"rule": {"more/*.txt"}}); err != nil {
		t.Fatal(err)
	}
	want = slices.Sorted(slices.Values(append(slices.Clone(named), "more/*.txt")))
	if got := filesUnder(t, ws.head); !slices.Equal(got, want) {
		t.Errorf("after adding more/*.txt the checkout has %q,\nwant %q", got, want)
	}
}

// A line break ends a pattern in git's sparse-checkout file, so a name with one cannot be asked for: the workspace
// fails instead of checking out something else.
func TestAFileNameWithALineBreakFailsTheWorkspace(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, name := range []string{"a\n/*", "a\rb"} {
		remote := t.TempDir()
		head := gitRepo(t, remote, map[string]string{name: "x", "innocent.go": "package innocent"})
		dir := t.TempDir()
		ws := workspace{dir: dir, added: filepath.Join(dir, "added"), head: filepath.Join(dir, "head"), tree: filepath.Join(dir, "tree"), main: t.TempDir(), remote: "file://" + remote}
		d := &data{Repo: "o/r", PR: 1, Files: []prFile{{Filename: name, Status: "added", Patch: "@@ -0,0 +1 @@\n+x"}}}
		d.Pull.Head.SHA = head
		scans := []check{scanCheck(t, scanSpec{Input: inputChanged, Run: []string{"x"}, Exit: exitCodes{Clean: []int{0}}, Report: reportSpec{In: "stdout", Format: "lines", Pattern: `(?P<message>.+)`}})}
		err := prepareWorkspace(context.Background(), d, scans, ws, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "line break") {
			t.Errorf("%q: err = %v, want a refusal naming the line break", name, err)
		}
		if _, statErr := os.Stat(filepath.Join(ws.head, "innocent.go")); statErr == nil {
			t.Errorf("%q checked out a file the PR did not name", name)
		}
	}
}
