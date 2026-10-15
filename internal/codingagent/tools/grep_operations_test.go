package tools

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pi core/tools/grep.ts:126-160: ops.isDirectory decides the path form (a throw is "Path not found"), and ops.readFile supplies the context lines.
func TestGrepOperationsSupplyDirectoryCheckAndContextLines(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep unavailable")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nTARGET\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var read []string
	tool := &GrepTool{CWD: dir, Operations: &GrepOperations{
		IsDirectory: func(p string) (bool, error) { return p == dir, nil },
		ReadFile: func(p string) (string, error) {
			read = append(read, filepath.Base(p))
			return "ONE\r\nTARGET\r\nTHREE", nil
		},
	}}
	result := runFileTool(t, tool, t.Context(), map[string]any{"pattern": "TARGET", "context": 1})
	if result.IsError || result.Text() != "a.txt-1- ONE\na.txt:2: TARGET\na.txt-3- THREE" {
		t.Fatalf("result = %+v", result)
	}
	if len(read) != 1 || read[0] != "a.txt" {
		t.Fatalf("readFile calls = %v, want one cached read of a.txt", read)
	}
}

func TestGrepOperationsIsDirectoryErrorIsPathNotFound(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep unavailable")
	}
	dir := t.TempDir()
	tool := &GrepTool{CWD: dir, Operations: &GrepOperations{
		IsDirectory: func(string) (bool, error) { return false, errors.New("boom") },
		ReadFile:    func(string) (string, error) { return "", nil },
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
			ReadFile:    func(string) (string, error) { return "one\nTARGET\n", nil },
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
