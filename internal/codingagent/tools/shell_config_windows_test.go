//go:build windows

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Windows shell resolution (defaultShellConfig) mirrors upstream getShellConfig's
// win32 branch. These run natively on a Windows runner; the _windows build tag
// keeps them out of the unix suite. Test names share the Win_ prefix so CI can
// select them with -test.run=Win_ without a regex alternation.

func TestWin_ShellResolvesGitBash(t *testing.T) {
	dir := t.TempDir()
	gitBin := filepath.Join(dir, "Git", "bin")
	if err := os.MkdirAll(gitBin, 0o755); err != nil {
		t.Fatal(err)
	}
	bash := filepath.Join(gitBin, "bash.exe")
	if err := os.WriteFile(bash, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ProgramFiles", dir)
	t.Setenv("ProgramFiles(x86)", "")

	cfg, err := GetShellConfig(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.Path != bash {
		t.Fatalf("path = %q, want %q", cfg.Path, bash)
	}
	if len(cfg.Args) != 1 || cfg.Args[0] != "-c" {
		t.Fatalf("args = %v, want [-c]", cfg.Args)
	}
}

func TestWin_ShellNoBashReturnsHelpfulError(t *testing.T) {
	t.Setenv("ProgramFiles", t.TempDir()) // no Git subdir
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("PATH", t.TempDir()) // no bash.exe on PATH

	_, err := GetShellConfig(nil)
	if err == nil {
		t.Fatal("expected an error when no bash is installed")
	}
	if !strings.Contains(err.Error(), "Git for Windows") {
		t.Fatalf("error is not the helpful guidance: %v", err)
	}
}

func TestWin_ShellSettingsOverrideWins(t *testing.T) {
	want := filepath.Join(t.TempDir(), "bash.exe")
	if err := os.WriteFile(want, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := GetShellConfig(fakeSettings{path: want})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.Path != want {
		t.Fatalf("path = %q, want %q", cfg.Path, want)
	}
	if len(cfg.Args) != 1 || cfg.Args[0] != "-c" {
		t.Fatalf("args = %v, want [-c]", cfg.Args)
	}
}
