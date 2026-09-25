package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// stubGitBranchSpawn replaces the git fallback and counts its calls.
func stubGitBranchSpawn(t *testing.T, branch string) *[]string {
	t.Helper()
	var calls []string
	previous := resolveBranchWithGit
	resolveBranchWithGit = func(repoDir string) string {
		calls = append(calls, repoDir)
		return branch
	}
	t.Cleanup(func() { resolveBranchWithGit = previous })
	return &calls
}

func writeGitFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Ports footer-data-provider.test.ts "uses HEAD directly in a regular repo
// from a nested directory": the branch comes from HEAD with no git process.
func TestResolveGitBranchReadsHeadWithoutGitProcess(t *testing.T) {
	calls := stubGitBranchSpawn(t, "unused")
	repoDir := filepath.Join(t.TempDir(), "repo")
	writeGitFixtureFile(t, filepath.Join(repoDir, ".git", "HEAD"), "ref: refs/heads/main\n")
	nestedDir := filepath.Join(repoDir, "src", "nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveGitBranch(nestedDir); got != "main" {
		t.Fatalf("branch = %q, want main", got)
	}
	writeGitFixtureFile(t, filepath.Join(repoDir, ".git", "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")
	if got := resolveGitBranch(nestedDir); got != "detached" {
		t.Fatalf("branch at a commit HEAD = %q, want detached", got)
	}
	if got := resolveGitBranch(t.TempDir()); got != "" {
		t.Fatalf("branch outside a repository = %q, want empty", got)
	}
	if len(*calls) != 0 {
		t.Fatalf("git processes = %d, want 0 (upstream reads HEAD directly)", len(*calls))
	}
}

// Ports "resolves the branch via git when HEAD is .invalid in a reftable repo"
// and "treats an unresolved .invalid reftable HEAD as detached".
func TestResolveGitBranchAsksGitForReftableHead(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "repo")
	writeGitFixtureFile(t, filepath.Join(repoDir, ".git", "HEAD"), "ref: refs/heads/.invalid\n")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git", "reftable"), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := stubGitBranchSpawn(t, "main")
	if got := resolveGitBranch(repoDir); got != "main" {
		t.Fatalf("reftable branch = %q, want main", got)
	}
	if len(*calls) != 1 || (*calls)[0] != repoDir {
		t.Fatalf("git calls = %v, want one call in %s", *calls, repoDir)
	}
	stubGitBranchSpawn(t, "")
	if got := resolveGitBranch(repoDir); got != "detached" {
		t.Fatalf("unresolved reftable branch = %q, want detached", got)
	}
}

// Ports "resolves the branch via git in a reftable-backed worktree".
func TestResolveGitBranchInReftableWorktree(t *testing.T) {
	tempDir := t.TempDir()
	gitDir := filepath.Join(tempDir, "repo", ".git", "worktrees", "src")
	worktreeDir := filepath.Join(tempDir, "worktree")
	writeGitFixtureFile(t, filepath.Join(worktreeDir, ".git"), "gitdir: "+gitDir+"\n")
	writeGitFixtureFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/.invalid\n")
	writeGitFixtureFile(t, filepath.Join(gitDir, "commondir"), "../..\n")
	calls := stubGitBranchSpawn(t, "main")
	if got := resolveGitBranch(worktreeDir); got != "main" {
		t.Fatalf("worktree branch = %q, want main", got)
	}
	if len(*calls) != 1 || (*calls)[0] != worktreeDir {
		t.Fatalf("git calls = %v, want one call in %s", *calls, worktreeDir)
	}

	// A worktree whose .git file names a relative gitdir resolves it from the
	// worktree directory, as Node path.resolve does.
	relativeWorktree := filepath.Join(tempDir, "relative")
	writeGitFixtureFile(t, filepath.Join(relativeWorktree, ".git"), "gitdir: ../repo/.git/worktrees/src\n")
	writeGitFixtureFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feature/x\n")
	if got := resolveGitBranch(relativeWorktree); got != "feature/x" {
		t.Fatalf("relative worktree branch = %q, want feature/x", got)
	}
}

// Upstream findGitPaths falls through to the parent directory when a .git
// file does not start with "gitdir: ", so a malformed child .git file does not
// hide the enclosing repository.
func TestResolveGitBranchWalksPastMalformedGitFile(t *testing.T) {
	calls := stubGitBranchSpawn(t, "unused")
	outer := t.TempDir()
	writeGitFixtureFile(t, filepath.Join(outer, ".git", "HEAD"), "ref: refs/heads/main\n")
	inner := filepath.Join(outer, "child")
	writeGitFixtureFile(t, filepath.Join(inner, ".git"), "not a gitdir\n")
	if got := resolveGitBranch(inner); got != "main" {
		t.Fatalf("branch = %q, want the parent repository's main", got)
	}
	if len(*calls) != 0 {
		t.Fatalf("git processes = %d, want 0", len(*calls))
	}
}
