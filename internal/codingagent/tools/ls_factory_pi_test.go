package tools

// pi: packages/coding-agent/src/core/tools/ls.ts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCreateLsToolBindsItsCwdAndOperations ports createLsTool(cwd, options) (ls.ts, tools/index.ts:107-116):
// the tool lists relative to the cwd it was created with, including dotfiles and dot-directories
// (tools.test.ts:984 "should list dotfiles and directories"), and options.operations replaces the local filesystem.
func TestCreateLsToolBindsItsCwdAndOperations(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".hidden-file"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(cwd, ".hidden-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := CreateLsTool(cwd, nil).Execute(t.Context(), "id", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := resultText(t, result)
	if !strings.Contains(got, ".hidden-file") || !strings.Contains(got, ".hidden-dir/") {
		t.Fatalf("listing of the created cwd = %q, want the dotfile and the dot-directory with a slash", got)
	}

	virtual := CreateLsTool("/virtual", &LsToolOptions{Operations: &LsOperations{
		Exists:  func(path string) (bool, error) { return path == "/virtual", nil },
		Stat:    func(string) (LsStat, error) { return fakeFileInfo{dir: true}, nil },
		Readdir: func(string) ([]string, error) { return []string{"only"}, nil },
	}})
	result, err = virtual.Execute(t.Context(), "id", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultText(t, result); got != "only/" {
		t.Fatalf("listing through the options' operations = %q, want only/", got)
	}
}
