package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// Pi 1be312b4 (#10488) resource-loader.ts findShadowedContextFile: a linked worktree's context file shadows the main worktree's when the worktree root is inside the main
// repository root (isPathWithin) and the main repository's .git is the common git directory (pathsEqual). On Windows git's paths and the working directory may differ in casing.
func TestShadowedContextFileOfComparesCaseInsensitivelyOnWindows(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Repo")
	worktree := filepath.Join(root, "wt")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "AGENTS.md"), []byte("worktree"), 0o644); err != nil {
		t.Fatal(err)
	}
	commonGitDir := filepath.Join(strings.ToLower(root), ".git")
	identity := func(path string) string { return path }
	if got := shadowedContextFileOf(commonGitDir, worktree, identity); got != "" {
		t.Fatalf("shadowed = %q on a case-sensitive platform, want none", got)
	}
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	want := filepath.Join(strings.ToLower(root), "AGENTS.md")
	if got := shadowedContextFileOf(commonGitDir, worktree, identity); got != want {
		t.Fatalf("shadowed = %q, want %q", got, want)
	}
	// The worktree root equal to the main root is an ordinary repository, not a linked worktree.
	if got := shadowedContextFileOf(filepath.Join(strings.ToLower(root), ".git"), root, identity); got != "" {
		t.Fatalf("an ordinary repository shadowed %q", got)
	}
}
