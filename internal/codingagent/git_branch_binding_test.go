package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Resolve branch fixtures through the production footer binding, including its absent-repository state.
func resolveGitBranch(cwd string) string {
	footer := NewStatusLine(nil, "", nil)
	footer.SetCwd(cwd)
	return footer.GitBranch()
}

// FooterDataProvider captures gitPaths at construction (footer-data-provider.ts:120-123,239-241,308-309). Creating a repository in session_start does not attach a watcher to it.
func TestFooterDoesNotWatchRepositoryCreatedAfterBinding(t *testing.T) {
	cwd := t.TempDir()
	footer := NewStatusLine(nil, "", nil)
	footer.SetCwd(cwd)
	if err := os.Mkdir(filepath.Join(cwd, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".git", "HEAD"), []byte("ref: refs/heads/late\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if watcher := newGitBranchWatcher(cwd, footer); watcher != nil {
		watcher.run(ctx)
	}
	if ctx.Err() != nil {
		t.Fatal("footer attached a watcher to a repository absent when the footer was bound")
	}
	if got := footer.GitBranch(); got != "" {
		t.Fatalf("branch = %q, want no branch", got)
	}
}
