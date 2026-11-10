package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi v1.5.1 244e5f5d test/grep-operations.test.ts: the deprecated readFile member is accepted and never called; context lines come from ripgrep (#9276).
func TestGrepOperationsAcceptsADeprecatedReadFileHookWithoutCallingIt(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep unavailable")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("before\nneedle\nafter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const want = "file.txt-1- before\nfile.txt:2: needle\nfile.txt-3- after"
	t.Run("a readFile hook", func(t *testing.T) {
		calls := 0
		ops := &GrepOperations{
			IsDirectory: func(string) (bool, error) { return true, nil },
			ReadFile:    func(string) (string, error) { calls++; return "custom context", nil },
		}
		result := runFileTool(t, &GrepTool{CWD: dir, Operations: ops}, t.Context(), map[string]any{"pattern": "needle", "context": 1})
		if result.IsError || result.Text() != want || calls != 0 {
			t.Fatalf("result = %q (error %v), readFile calls = %d; want %q and none", result.Text(), result.IsError, calls, want)
		}
	})
	t.Run("custom operations without a readFile hook", func(t *testing.T) {
		ops := &GrepOperations{IsDirectory: func(string) (bool, error) { return true, nil }}
		result := runFileTool(t, &GrepTool{CWD: dir, Operations: ops}, t.Context(), map[string]any{"pattern": "needle", "context": 1})
		if result.IsError || result.Text() != want {
			t.Fatalf("result = %q (error %v), want %q", result.Text(), result.IsError, want)
		}
	})
}
