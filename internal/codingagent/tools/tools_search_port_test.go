package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

func TestToolsGrepPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:829
	t.Run("should include filename when searching a single file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "example.txt")
		if err := os.WriteFile(path, []byte("first line\nmatch line\nlast line"), 0o600); err != nil {
			t.Fatal(err)
		}
		requireTextParts(t, runGrep(t, dir, map[string]any{"pattern": "match", "path": path}), []string{"example.txt:2: match line"}, nil)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:842
	t.Run("should respect global limit and include context lines", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "context.txt")
		if err := os.WriteFile(path, []byte("before\nmatch one\nafter\nmiddle\nmatch two\nafter two"), 0o600); err != nil {
			t.Fatal(err)
		}
		requireTextParts(t, runGrep(t, dir, map[string]any{"pattern": "match", "path": path, "limit": 1, "context": 1}), []string{"context.txt-1- before", "context.txt:2: match one", "context.txt-3- after", "[1 matches limit reached. Use limit=2 for more, or refine pattern]"}, []string{"match two"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:863
	t.Run("should treat flag-like patterns as search text", func(t *testing.T) {
		dir := t.TempDir()
		marker := filepath.Join(dir, "grep-injection-marker")
		payload := filepath.Join(dir, "payload.sh")
		if err := os.WriteFile(payload, []byte("#!/bin/sh\necho executed > "+marker+"\ncat \"$1\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte("target\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		// The pattern is a regex, so it names the payload with forward slashes:
		// a Windows path's backslashes are regex escapes (\U is a hex escape).
		requireTextParts(t, runGrep(t, dir, map[string]any{"pattern": "--pre=" + filepath.ToSlash(payload), "path": dir}), []string{"No matches found"}, nil)
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("marker stat = %v", err)
		}
	})
}

func TestToolsFindPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:882
	t.Run("should include hidden files that are not gitignored", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, ".secret"), 0o700); err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{".secret/hidden.txt": "hidden", "visible.txt": "visible"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		result := runFind(t, dir, map[string]any{"pattern": "**/*.txt", "path": dir})
		if result.IsError {
			t.Fatal(result.Text())
		}
		for _, want := range []string{"visible.txt", ".secret/hidden.txt"} {
			if !slices.Contains(outputLines(result.Text()), want) {
				t.Fatalf("output %q lacks %q", result.Text(), want)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:902
	t.Run("should respect .gitignore", func(t *testing.T) {
		dir := t.TempDir()
		for name, content := range map[string]string{".gitignore": "ignored.txt\n", "ignored.txt": "ignored", "kept.txt": "kept"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		requireTextParts(t, runFind(t, dir, map[string]any{"pattern": "**/*.txt", "path": dir}), []string{"kept.txt"}, []string{"ignored.txt"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:917
	t.Run("should surface fd glob parse errors", func(t *testing.T) {
		dir := t.TempDir()
		result := runFind(t, dir, map[string]any{"pattern": "[", "path": dir})
		if !result.IsError || !regexp.MustCompile(`(?i)error parsing glob|fd exited with code 1|fd error`).MatchString(result.Text()) {
			t.Fatal(result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:926
	t.Run("should treat flag-like patterns as search text", func(t *testing.T) {
		dir := t.TempDir()
		requireTextParts(t, runFind(t, dir, map[string]any{"pattern": "--help", "path": dir}), []string{"No files found matching pattern"}, nil)
	})
}
