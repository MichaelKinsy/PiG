package tools

// pi: packages/coding-agent/src/core/tools/ls.ts

import (
	"context"
	"testing"
)

// ls.ts:73-76 rejects with "Operation aborted" before it touches the file system when the signal is already aborted, and ls.ts:78-79's abort listener
// rejects the call when the signal aborts while it is still reading, so a call cancelled before it starts never calls an operation and a call cancelled
// during the directory read still fails (its late result is ignored).
// mutation-checked: dropping the up-front context check (ls.go, before the path is resolved) fails the first case with Exists called, and dropping the
// check after the entry loop fails the second case with the listing returned.
func TestLsAbortIsHonouredBeforeAndDuringTheRead(t *testing.T) {
	root := virtualPath("/virtual")
	t.Run("a call cancelled before it starts calls no operation", func(t *testing.T) {
		calls := 0
		ops := virtualTree(root, []string{"a"})
		exists := ops.Exists
		ops.Exists = func(path string) (bool, error) { calls++; return exists(path) }
		readdir := ops.Readdir
		ops.Readdir = func(path string) ([]string, error) { calls++; return readdir(path) }
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res := runFileTool(t, CreateLsTool(root, &LsToolOptions{Operations: ops}), ctx, map[string]any{})
		if !res.IsError || res.Text() != "Operation aborted" || calls != 0 {
			t.Fatalf("result = %+v after %d operation calls, want Operation aborted and none", res, calls)
		}
	})
	t.Run("a call cancelled during the directory read fails", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		ops := virtualTree(root, []string{"a", "b"})
		readdir := ops.Readdir
		ops.Readdir = func(path string) ([]string, error) { cancel(); return readdir(path) }
		res := runFileTool(t, CreateLsTool(root, &LsToolOptions{Operations: ops}), ctx, map[string]any{})
		if !res.IsError || res.Text() != "Operation aborted" {
			t.Fatalf("result = %+v, want Operation aborted", res)
		}
	})
}
