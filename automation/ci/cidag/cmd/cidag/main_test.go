// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/automation/ci/cidag"
)

func TestAuditRunCLIExitCodeAndBlindSpots(t *testing.T) {
	tmp := t.TempDir()
	files := filepath.Join(tmp, "files.txt")
	if err := os.WriteFile(files, []byte("README.md\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Docs-only change: empty failed lanes -> audit passes.
	if err := run([]string{"-root", "../../../../..", "-files-from", files, "-audit-run", "-failed-lanes", ""}); err != nil {
		t.Fatalf("audit with zero failed lanes failed: %v", err)
	}

	// Docs-only change where a full run had a failing lane -> blind spot error.
	if err := run([]string{"-root", "../../../../..", "-files-from", files, "-audit-run", "-failed-lanes", "test-fast"}); err == nil {
		t.Fatalf("expected audit blind spot error when test-fast failed on docs-only plan")
	}

	// Force-full event (merge_group) covers every failing lane.
	if err := run([]string{"-root", "../../../../..", "-files-from", files, "-event", "merge_group", "-audit-run", "-failed-lanes", "test-fast,win-cli"}); err != nil {
		t.Fatalf("expected merge_group full plan to cover all failing lanes, got: %v", err)
	}
}

func TestGitNULPathsPreserveLeadingSpaceFilenames(t *testing.T) {
	// Unit check of splitGitNULPaths directly.
	got := splitGitNULPaths(" README.md\x00dir/file with space.txt\x00")
	want := []string{" README.md", "dir/file with space.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("splitGitNULPaths = %#v, want %#v", got, want)
	}

	// Integration check against real git diff -z and diff-tree -z.
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{
			"-c", "user.name=t",
			"-c", "user.email=t@example.invalid",
			"-c", "commit.gpgsign=false",
			"-c", "core.hooksPath=" + os.DevNull,
		}, args...)...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	baseSHA := git("rev-parse", "HEAD")
	baseSHA = baseSHA[:len(baseSHA)-1]

	// Create a file whose name starts with a literal space: " README.md".
	if err := os.WriteFile(filepath.Join(repo, " README.md"), []byte("not-docs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "leading-space")
	headSHA := git("rev-parse", "HEAD")
	headSHA = headSHA[:len(headSHA)-1]

	files, _, _, err := resolveChangedFiles(context.Background(), repo, "", "", baseSHA, headSHA)
	if err != nil {
		t.Fatalf("resolveChangedFiles: %v", err)
	}
	if !slices.Equal(files, []string{" README.md"}) {
		t.Fatalf("resolveChangedFiles = %#v, want [` README.md`]", files)
	}
	// Verify cidag.Select treats " README.md" as unowned -> full CI (not docs-only README.md).
	g := &cidag.Graph{Root: repo, Module: cidag.DefaultModule}
	plan := cidag.Select(files, g, cidag.DefaultRules, cidag.Options{})
	if !plan.Full || plan.DocsOnly || !slices.Contains(plan.Unowned, " README.md") {
		t.Fatalf("leading-space filename must fail closed as unowned -> full, got full=%v docsOnly=%v unowned=%v", plan.Full, plan.DocsOnly, plan.Unowned)
	}

	// Also verify diff-tree -z (used by replay) preserves the leading space.
	diffOut, err := gitOutput(context.Background(), repo, "diff-tree", "--no-commit-id", "--name-only", "--no-renames", "-z", "-r", "-m", "--first-parent", headSHA)
	if err != nil {
		t.Fatalf("git diff-tree -z: %v", err)
	}
	replayFiles := splitGitNULPaths(diffOut)
	if !slices.Equal(replayFiles, []string{" README.md"}) {
		t.Fatalf("replay diff-tree -z = %#v, want [` README.md`]", replayFiles)
	}
}
