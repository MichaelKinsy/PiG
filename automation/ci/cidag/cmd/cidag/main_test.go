// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"io"
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

	// Also verify runReplay itself preserves the leading space via diff-tree -z
	// (not trimming " README.md" to docs-only "README.md").
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module "+cidag.DefaultModule+"\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "doc.go"), []byte("package root\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wPipe
	replayErr := runReplay(context.Background(), repo, 1, false, 0, true)
	_ = wPipe.Close()
	os.Stdout = origStdout
	replayJSON, readErr := io.ReadAll(rPipe)
	_ = rPipe.Close()
	if replayErr != nil {
		t.Fatalf("runReplay: %v", replayErr)
	}
	if readErr != nil {
		t.Fatalf("read runReplay stdout: %v", readErr)
	}
	var summary replaySummary
	if err := json.Unmarshal(replayJSON, &summary); err != nil {
		t.Fatalf("decode runReplay JSON: %v\n%s", err, replayJSON)
	}
	if len(summary.Rows) != 1 {
		t.Fatalf("runReplay rows = %d, want 1", len(summary.Rows))
	}
	row := summary.Rows[0]
	if !row.Full || row.DocsOnly || row.Unowned != 1 {
		t.Fatalf("runReplay must treat leading-space ` README.md` as unowned -> full, got full=%v docsOnly=%v unowned=%d reasons=%v",
			row.Full, row.DocsOnly, row.Unowned, row.FullReason)
	}
}

func TestResolveChangedFilesFromPreservesFilenameWhitespace(t *testing.T) {
	tmp := t.TempDir()
	listPath := filepath.Join(tmp, "files.txt")
	content := " README.md\nREADME.md \r\n\tdocs/note.md\n\n"
	if err := os.WriteFile(listPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	files, _, _, err := resolveChangedFiles(context.Background(), tmp, listPath, "", "", "")
	if err != nil {
		t.Fatalf("resolveChangedFiles(-files-from): %v", err)
	}
	want := []string{" README.md", "README.md ", "\tdocs/note.md"}
	if !slices.Equal(files, want) {
		t.Fatalf("files = %#v, want %#v", files, want)
	}
	g := &cidag.Graph{Root: tmp, Module: cidag.DefaultModule}
	plan := cidag.Select([]string{files[0]}, g, cidag.DefaultRules, cidag.Options{})
	if !plan.Full || plan.DocsOnly || !slices.Contains(plan.Unowned, " README.md") {
		t.Fatalf("leading-space filename from -files-from must fail closed as unowned -> full, got full=%v docsOnly=%v unowned=%v",
			plan.Full, plan.DocsOnly, plan.Unowned)
	}
}
