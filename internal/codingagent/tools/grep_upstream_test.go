package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

func requireRG(t *testing.T) string {
	t.Helper()
	rg, err := exec.LookPath("rg")
	if err != nil {
		t.Fatalf("these tests need ripgrep on PATH: %v", err)
	}
	return rg
}

func runGrep(t *testing.T, dir string, params map[string]any) agent.AgentToolResult {
	t.Helper()
	args, _ := json.Marshal(params)
	res, err := (&GrepTool{CWD: dir, RgPath: requireRG(t)}).Execute(context.Background(), "", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// Ported from upstream test/tools.test.ts "grep tool".
func TestGrepToolUpstreamCases(t *testing.T) {
	dir := t.TempDir()
	single := filepath.Join(dir, "example.txt")
	if err := os.WriteFile(single, []byte("first line\nmatch line\nlast line"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := runGrep(t, dir, map[string]any{"pattern": "match", "path": single}); !strings.Contains(res.Content, "example.txt:2: match line") {
		t.Errorf("single file: %q", res.Content)
	}

	ctxFile := filepath.Join(dir, "context.txt")
	if err := os.WriteFile(ctxFile, []byte("before\nmatch one\nafter\nmiddle\nmatch two\nafter two"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := runGrep(t, dir, map[string]any{"pattern": "match", "path": ctxFile, "limit": 1, "context": 1})
	for _, want := range []string{"context.txt-1- before", "context.txt:2: match one", "context.txt-3- after",
		"[1 matches limit reached. Use limit=2 for more, or refine pattern]"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("context/limit output missing %q: %q", want, res.Content)
		}
	}
	if strings.Contains(res.Content, "match two") {
		t.Errorf("second match present: %q", res.Content)
	}

	injection := t.TempDir()
	marker := filepath.Join(injection, "grep-injection-marker")
	payload := filepath.Join(injection, "payload.sh")
	if err := os.WriteFile(payload, []byte("#!/bin/sh\necho executed > "+marker+"\ncat \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(injection, "target.txt"), []byte("target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The pattern is also a regex; the '/' spelling keeps a Windows payload path
	// free of backslash escapes such as \U that rg would reject.
	res = runGrep(t, injection, map[string]any{"pattern": "--pre=" + filepath.ToSlash(payload), "path": injection})
	if !strings.Contains(res.Content, "No matches found") {
		t.Errorf("flag-like pattern: %q", res.Content)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("flag-like pattern executed the payload")
	}
}

// TOOL-04: a match line over 1 MB must not hang grep or drop later matches.
func TestGrep_HugeMatchLineDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a-bundle.js"), []byte("needle"+strings.Repeat("x", 2<<20)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b-other.txt"), []byte("needle here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args, _ := json.Marshal(map[string]any{"pattern": "needle"})
	done := make(chan agent.AgentToolResult, 1)
	go func() {
		res, _ := (&GrepTool{CWD: dir, RgPath: requireRG(t)}).Execute(ctx, "", args, nil)
		done <- res
	}()
	select {
	case res := <-done:
		if res.IsError || !strings.Contains(res.Content, "b-other.txt:1: needle here") ||
			!strings.Contains(res.Content, "a-bundle.js:1: needle") || !strings.Contains(res.Content, "Some lines truncated to 500 chars") {
			t.Fatalf("res = %.300q", res.Content)
		}
	case <-ctx.Done():
		t.Fatal("grep hung on a match line over 1 MB")
	}
}

// TOOL-06: rg's stderr is the error message (upstream: stderr.trim() ||
// "ripgrep exited with code N").
func TestGrep_InvalidRegexReportsStderr(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("foo(bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := runGrep(t, dir, map[string]any{"pattern": "foo(bar"})
	if !res.IsError || !strings.Contains(res.Content, "regex parse error") {
		t.Fatalf("res = %+v", res)
	}
}

// Missing paths and aborts report upstream's messages.
func TestGrep_PathNotFoundAndAbort(t *testing.T) {
	dir := t.TempDir()
	res := runGrep(t, dir, map[string]any{"pattern": "x", "path": "nope"})
	if !res.IsError || res.Content != "Path not found: "+filepath.Join(dir, "nope") {
		t.Fatalf("res = %+v", res)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	args, _ := json.Marshal(map[string]any{"pattern": "x"})
	res, err := (&GrepTool{CWD: dir, RgPath: requireRG(t)}).Execute(ctx, "", args, nil)
	if err != nil || !res.IsError || res.Content != "Operation aborted" {
		t.Fatalf("abort: %+v %v", res, err)
	}
}
