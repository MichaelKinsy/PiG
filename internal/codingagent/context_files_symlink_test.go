package codingagent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports packages/coding-agent/test/resource-loader.test.ts "should load the worktree's context once when it symlinks to the main repo's file" and "should skip the main repo's duplicate when the main repo's file is a symlink" (pi issue 10681, 42a3497d): the shadowed main-repo file is compared by the directory's real path plus the file name, so a symlinked AGENTS.md is not mistaken for the shadowed file.
func TestLoadProjectContextFilesNestedWorktreeSymlinkedContext(t *testing.T) {
	setup := func(t *testing.T) (outer, mainDir, worktree, leaf string) {
		t.Helper()
		outer = t.TempDir()
		mainDir = filepath.Join(outer, "main")
		worktree = filepath.Join(mainDir, "worktrees", "feat")
		leaf = filepath.Join(worktree, "src")
		gitDir := filepath.Join(mainDir, ".git", "worktrees", "feat")
		for _, dir := range []string{leaf, gitDir} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		writeContextFile(t, filepath.Join(mainDir, ".git", "HEAD"), "ref: refs/heads/main\n")
		writeContextFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feat\n")
		writeContextFile(t, filepath.Join(gitDir, "commondir"), "../..")
		writeContextFile(t, filepath.Join(worktree, ".git"), "gitdir: "+gitDir+"\n")
		return outer, mainDir, worktree, leaf
	}
	paths := func(files []ContextFile) []string {
		out := []string{}
		for _, file := range files {
			out = append(out, file.Path)
		}
		return out
	}
	t.Run("worktree file symlinks to the main repo's file", func(t *testing.T) {
		_, mainDir, worktree, leaf := setup(t)
		writeContextFile(t, filepath.Join(mainDir, "AGENTS.md"), "main repo instructions")
		testenv.Symlink(t, filepath.Join(mainDir, "AGENTS.md"), filepath.Join(worktree, "AGENTS.md"))
		got := paths(LoadProjectContextFiles(leaf, ""))
		if want := []string{filepath.Join(worktree, "AGENTS.md")}; !reflect.DeepEqual(got, want) {
			t.Fatalf("paths = %v, want %v", got, want)
		}
	})
	t.Run("main repo's file is a symlink", func(t *testing.T) {
		outer, mainDir, worktree, leaf := setup(t)
		shared := filepath.Join(outer, "shared-agents.md")
		writeContextFile(t, shared, "shared instructions")
		for _, dir := range []string{mainDir, worktree} {
			testenv.Symlink(t, shared, filepath.Join(dir, "AGENTS.md"))
		}
		got := paths(LoadProjectContextFiles(leaf, ""))
		if want := []string{filepath.Join(worktree, "AGENTS.md")}; !reflect.DeepEqual(got, want) {
			t.Fatalf("paths = %v, want %v", got, want)
		}
	})
}
