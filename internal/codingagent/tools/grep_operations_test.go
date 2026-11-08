package tools

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepOperationsIsDirectoryErrorIsPathNotFound(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep unavailable")
	}
	dir := t.TempDir()
	tool := &GrepTool{CWD: dir, Operations: &GrepOperations{
		IsDirectory: func(string) (bool, error) { return false, errors.New("boom") },
	}}
	result := runFileTool(t, tool, t.Context(), map[string]any{"pattern": "x", "path": "missing"})
	if !result.IsError || !strings.HasPrefix(result.Text(), "Path not found: "+filepath.Join(dir, "missing")) {
		t.Fatalf("result = %+v", result)
	}
}

// grep.ts: operations.isDirectory receives the resolved absolute search path, and its answer picks the path form of every match line: relative to the searched directory when it says
// directory, the file's basename when it says file (formatPath). A custom operation can therefore say "file" for a directory, and the output follows it.
// The relative form always uses '/', also on Windows, where path.relative yields backslashes that grep.ts:141 replaces.
func TestGrepOperationsIsDirectoryReceivesTheResolvedPathAndItsAnswerPicksThePathForm(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep unavailable")
	}
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "a.txt"), []byte("one\nTARGET\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		answer bool
		want   string
	}{
		{true, "deep/a.txt:2: TARGET"},
		{false, "a.txt:2: TARGET"},
	} {
		var asked []string
		tool := &GrepTool{CWD: dir, Operations: &GrepOperations{
			IsDirectory: func(p string) (bool, error) { asked = append(asked, p); return tc.answer, nil },
		}}
		result := runFileTool(t, tool, t.Context(), map[string]any{"pattern": "TARGET", "path": "sub"})
		if result.IsError || result.Text() != tc.want {
			t.Fatalf("isDirectory=%v: result = %q (error %v), want %q", tc.answer, result.Text(), result.IsError, tc.want)
		}
		if len(asked) != 1 || asked[0] != filepath.Join(dir, "sub") {
			t.Fatalf("isDirectory was asked %q, want the resolved path %q", asked, filepath.Join(dir, "sub"))
		}
	}
}
