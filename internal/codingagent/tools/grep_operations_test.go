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
