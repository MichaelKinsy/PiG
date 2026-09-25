package gitsnapshot

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCreateIgnoresGitStderrNoise(t *testing.T) {
	root := t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	quiet, err := Create(t.Context(), root, filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	// GIT_TRACE makes every git command write trace lines to stderr, the same
	// channel git uses for warnings such as an unreadable global config.
	t.Setenv("GIT_TRACE", "2")
	noisy, err := Create(t.Context(), root, filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatalf("git stderr noise broke the snapshot: %v", err)
	}
	if noisy != quiet {
		t.Fatalf("git stderr noise changed the snapshot commit: %q, want %q", noisy, quiet)
	}
}
