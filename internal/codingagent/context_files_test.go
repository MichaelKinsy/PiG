package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestLoadProjectContextFiles_PrefersOverridePerDirectory(t *testing.T) {
	root := t.TempDir()
	service := filepath.Join(root, "service")
	if err := os.MkdirAll(service, 0o755); err != nil {
		t.Fatal(err)
	}
	writeContextFile(t, filepath.Join(root, "AGENTS.md"), "root regular")
	writeContextFile(t, filepath.Join(root, "AGENTS.override.md"), "root override")
	writeContextFile(t, filepath.Join(service, "AGENTS.md"), "service regular")
	writeContextFile(t, filepath.Join(service, "AGENTS.override.md"), "service override")

	files := LoadProjectContextFiles(service, "")
	if len(files) != 2 || files[0].Content != "root override" || files[1].Content != "service override" {
		t.Fatalf("context files = %+v", files)
	}
}

func TestLoadProjectContextFiles_SkipsOverrideDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "AGENTS.override.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeContextFile(t, filepath.Join(dir, "AGENTS.md"), "regular")
	files := LoadProjectContextFiles(dir, "")
	if len(files) != 1 || files[0].Content != "regular" {
		t.Fatalf("context files = %+v", files)
	}
}

func writeContextFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProjectContextFiles_CwdOnly(t *testing.T) {
	dir := t.TempDir()
	content := "# Test\nSome rules."
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	files := LoadProjectContextFiles(dir, "")
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	if files[0].Content != content {
		t.Errorf("content = %q, want %q", files[0].Content, content)
	}
}

func TestLoadProjectContextFiles_PrefersAGENTSOverCLAUDE(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("claude"), 0644); err != nil {
		t.Fatal(err)
	}
	files := LoadProjectContextFiles(dir, "")
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	if files[0].Content != "agents" {
		t.Errorf("content = %q, want %q (should prefer AGENTS.md)", files[0].Content, "agents")
	}
}

func TestLoadProjectContextFiles_AcceptsUppercaseVariants(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.MD"), []byte("agents-upper"), 0644); err != nil {
		t.Fatal(err)
	}
	files := LoadProjectContextFiles(dir, "")
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	if files[0].Content != "agents-upper" {
		t.Fatalf("content = %q, want agents-upper", files[0].Content)
	}
}

func TestLoadProjectContextFiles_AncestorWalk(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	// AGENTS.md in root
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root"), 0644); err != nil {
		t.Fatal(err)
	}
	// CLAUDE.md in sub
	if err := os.WriteFile(filepath.Join(sub, "CLAUDE.md"), []byte("sub"), 0644); err != nil {
		t.Fatal(err)
	}
	files := LoadProjectContextFiles(sub, "")
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2 (root + sub)", len(files))
	}
	// Root-first ordering (upstream: ancestorContextFiles.unshift)
	if files[0].Content != "root" {
		t.Errorf("files[0] = %q, want root (root-first order)", files[0].Content)
	}
	if files[1].Content != "sub" {
		t.Errorf("files[1] = %q, want sub", files[1].Content)
	}
}

func TestLoadProjectContextFiles_AgentDirFirst(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte("global"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("project"), 0644); err != nil {
		t.Fatal(err)
	}
	files := LoadProjectContextFiles(cwd, agentDir)
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	if files[0].Content != "global" {
		t.Errorf("files[0] = %q, want global (agent dir first)", files[0].Content)
	}
	if files[1].Content != "project" {
		t.Errorf("files[1] = %q, want project", files[1].Content)
	}
}

func TestLoadProjectContextFiles_NoDuplicates(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("same"), 0644); err != nil {
		t.Fatal(err)
	}
	// agentDir == cwd: same file should appear only once
	files := LoadProjectContextFiles(dir, dir)
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1 (deduped)", len(files))
	}
}

func TestLoadProjectContextFilesNestedWorktreeSkipsMainDuplicate(t *testing.T) {
	outer := t.TempDir()
	mainDir := filepath.Join(outer, "main")
	worktree := filepath.Join(mainDir, "worktrees", "feat")
	leaf := filepath.Join(worktree, "src")
	gitDir := filepath.Join(mainDir, ".git", "worktrees", "feat")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeContextFile(t, filepath.Join(mainDir, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeContextFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feat\n")
	writeContextFile(t, filepath.Join(gitDir, "commondir"), "../..")
	writeContextFile(t, filepath.Join(worktree, ".git"), "gitdir: "+gitDir+"\n")
	writeContextFile(t, filepath.Join(mainDir, "AGENTS.md"), "main")
	writeContextFile(t, filepath.Join(worktree, "AGENTS.md"), "worktree")

	files := LoadProjectContextFiles(leaf, "")
	if len(files) != 1 || files[0].Content != "worktree" {
		t.Fatalf("context files = %#v", files)
	}
}

func TestLoadProjectContextFilesNestedWorktreeKeepsDifferentFilename(t *testing.T) {
	outer := t.TempDir()
	mainDir := filepath.Join(outer, "main")
	worktree := filepath.Join(mainDir, "worktrees", "feat")
	gitDir := filepath.Join(mainDir, ".git", "worktrees", "feat")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeContextFile(t, filepath.Join(mainDir, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeContextFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feat\n")
	writeContextFile(t, filepath.Join(gitDir, "commondir"), "../..")
	writeContextFile(t, filepath.Join(worktree, ".git"), "gitdir: "+gitDir+"\n")
	writeContextFile(t, filepath.Join(mainDir, "CLAUDE.md"), "main")
	writeContextFile(t, filepath.Join(worktree, "AGENTS.md"), "worktree")

	files := LoadProjectContextFiles(worktree, "")
	if len(files) != 2 || files[0].Content != "main" || files[1].Content != "worktree" {
		t.Fatalf("context files = %#v", files)
	}
}

func TestLoadProjectContextFiles_DeduplicatesByLoadedPath(t *testing.T) {
	realDir := t.TempDir()
	writeContextFile(t, filepath.Join(realDir, "AGENTS.md"), "same")
	aliasParent := t.TempDir()
	alias := filepath.Join(aliasParent, "alias")
	testenv.Symlink(t, realDir, alias)
	files := LoadProjectContextFiles(realDir, alias)
	if len(files) != 2 || files[0].Path != filepath.Join(alias, "AGENTS.md") || files[1].Path != filepath.Join(realDir, "AGENTS.md") {
		t.Fatalf("context files = %#v", files)
	}
}

func TestLoadProjectContextFiles_Empty(t *testing.T) {
	dir := t.TempDir()
	files := LoadProjectContextFiles(dir, "")
	if len(files) != 0 {
		t.Fatalf("got %d files, want 0", len(files))
	}
}
