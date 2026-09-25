package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func requireFD(t *testing.T) string {
	t.Helper()
	fd := LookupToolPath("fd", "")
	if fd == "" {
		t.Fatal("these tests need fd (or fdfind) on PATH")
	}
	return fd
}

func runFind(t *testing.T, dir string, params map[string]any) agent.AgentToolResult {
	t.Helper()
	args, _ := json.Marshal(params)
	res, err := (&FindTool{CWD: dir, FdPath: requireFD(t)}).Execute(context.Background(), "", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func outputLines(content string) []string {
	var lines []string
	for line := range strings.SplitSeq(content, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// Ported from upstream test/tools.test.ts "find tool".
func TestFindToolUpstreamCases(t *testing.T) {
	t.Run("hidden files that are not gitignored", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".secret"), 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, ".secret", "hidden.txt"), []byte("hidden"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("visible"), 0o644)
		lines := outputLines(runFind(t, dir, map[string]any{"pattern": "**/*.txt", "path": dir}).Content)
		if !slices.Contains(lines, "visible.txt") || !slices.Contains(lines, ".secret/hidden.txt") {
			t.Fatalf("lines = %q", lines)
		}
	})
	t.Run("respects .gitignore", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.txt\n"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("ignored"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "kept.txt"), []byte("kept"), 0o644)
		out := runFind(t, dir, map[string]any{"pattern": "**/*.txt", "path": dir}).Content
		if !strings.Contains(out, "kept.txt") || strings.Contains(out, "ignored.txt") {
			t.Fatalf("out = %q", out)
		}
	})
	t.Run("surfaces fd glob parse errors", func(t *testing.T) {
		dir := t.TempDir()
		res := runFind(t, dir, map[string]any{"pattern": "[", "path": dir})
		if !res.IsError || !strings.Contains(strings.ToLower(res.Content), "glob") {
			t.Fatalf("res = %+v", res)
		}
	})
	t.Run("flag-like pattern", func(t *testing.T) {
		dir := t.TempDir()
		if res := runFind(t, dir, map[string]any{"pattern": "--help", "path": dir}); res.Content != "No files found matching pattern" {
			t.Fatalf("res = %+v", res)
		}
	})
}

// TOOL-07: reaching the limit reports upstream's notice and details.
func TestFind_LimitReachedNotice(t *testing.T) {
	dir := t.TempDir()
	for i := range 1500 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.ts", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := runFind(t, dir, map[string]any{"pattern": "*.ts"})
	if !strings.HasSuffix(res.Content, "\n\n[1000 results limit reached. Use limit=2000 for more, or refine pattern]") {
		t.Fatalf("tail = %q", res.Content[len(res.Content)-120:])
	}
	d, ok := res.Details.(*FindDetails)
	if !ok || d.ResultLimitReached != 1000 {
		t.Fatalf("details = %+v", res.Details)
	}
	if n := len(outputLines(res.Content)); n != 1001 {
		t.Fatalf("got %d lines, want 1000 results + notice", n)
	}
}

// TOOL-08: fd failures are errors, not "(no matches)".
func TestFind_MissingPathIsError(t *testing.T) {
	res := runFind(t, t.TempDir(), map[string]any{"pattern": "*", "path": "nope"})
	if !res.IsError || res.Content == "" {
		t.Fatalf("res = %+v", res)
	}
}

// TOOL-09: with no fd the call fails like upstream instead of walking the tree
// with different matching.
func TestFind_NoFdIsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "sub", "a.ts"), nil, 0o644)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PIG_OFFLINE", "1")
	args, _ := json.Marshal(map[string]any{"pattern": "sub/*.ts"})
	for _, tool := range []agent.AgentTool{
		&FindTool{CWD: dir},
		&FindTool{CWD: dir, Tools: NewToolsManager(t.TempDir())},
	} {
		res, err := tool.Execute(context.Background(), "", args, nil)
		if err != nil || !res.IsError || res.Content != "fd is not available and could not be downloaded" {
			t.Fatalf("res = %+v, %v", res, err)
		}
	}
	grepArgs, _ := json.Marshal(map[string]any{"pattern": "x"})
	res, err := (&GrepTool{CWD: dir}).Execute(context.Background(), "", grepArgs, nil)
	if err != nil || !res.IsError || res.Content != "ripgrep (rg) is not available and could not be downloaded" {
		t.Fatalf("grep res = %+v, %v", res, err)
	}
}

// TOOL-09: tools resolve rg/fd per call, so a binary installed under
// <agentDir>/bin after the tools were built is used.
func TestSearchToolsResolveAgentBinAtCallTime(t *testing.T) {
	realRG := requireRG(t)
	agentDir := t.TempDir()
	binDir := filepath.Join(agentDir, "bin")
	var grep agent.AgentTool
	for _, tool := range CreateCodingTools(t.TempDir(), nil, binDir) {
		if tool.Name() == "grep" {
			grep = tool
		}
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PIG_OFFLINE", "1")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Windows runs only executables with an extension, so the managed copy
	// there is rg.exe.
	name := "rg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	testenv.Symlink(t, realRG, filepath.Join(binDir, name))
	args, _ := json.Marshal(map[string]any{"pattern": "x"})
	res, err := grep.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError || res.Content != "No matches found" {
		t.Fatalf("res = %+v, %v", res, err)
	}
}

func TestRelativizeFindResultPath(t *testing.T) {
	for _, c := range [][3]string{
		{"/root/a/b.ts", "/root", "a/b.ts"},
		{"/root/dir/", "/root", "dir/"},
		{"rel/x", "/root", "rel/x"},
	} {
		if got := relativizeFindResultPath(c[0], c[1]); got != c[2] {
			t.Errorf("relativize(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
