package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func runFileTool(t *testing.T, tool agent.AgentTool, ctx context.Context, params map[string]any) agent.AgentToolResult {
	t.Helper()
	args, _ := json.Marshal(params)
	res, err := tool.Execute(ctx, "", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// Ported from upstream test/tools.test.ts "edit tool" error cases (TOOL-26).
func TestEditAccessErrorsUseNodeCodes(t *testing.T) {
	dir := t.TempDir()
	edit := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
	missing := filepath.Join(dir, "missing.txt")
	res := runFileTool(t, edit, context.Background(), map[string]any{"path": missing, "edits": []editEntry{{"hello", "world"}}})
	if !res.IsError || res.Content != "Could not edit file: "+missing+". Error code: ENOENT." {
		t.Fatalf("missing: %+v", res)
	}
	if os.Geteuid() == 0 {
		return // root bypasses the permission check
	}
	readonly := filepath.Join(dir, "edit-readonly.txt")
	if err := os.WriteFile(readonly, []byte("hello\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	res = runFileTool(t, edit, context.Background(), map[string]any{"path": readonly, "edits": []editEntry{{"hello", "world"}}})
	if !res.IsError || res.Content != "Could not edit file: "+readonly+". Error code: EACCES." {
		t.Fatalf("read-only: %+v", res)
	}
}

// Ported from upstream test/tools.test.ts "edit tool" success cases.
func TestEditToolUpstreamCases(t *testing.T) {
	text, details, after, isErr := runEdit(t, "alpha\nbeta\ngamma\ndelta\n", []editEntry{{"alpha\n", "ALPHA\n"}, {"gamma\n", "GAMMA\n"}})
	if isErr || !strings.Contains(text, "Successfully replaced 2 block(s)") || after != "ALPHA\nbeta\nGAMMA\ndelta\n" ||
		!strings.Contains(details.Diff, "ALPHA") || !strings.Contains(details.Diff, "GAMMA") {
		t.Fatalf("disjoint: %q %q", text, after)
	}
	_, _, after, _ = runEdit(t, "foo\nbar\nbaz\n", []editEntry{{"foo\n", "foo bar\n"}, {"bar\n", "BAR\n"}})
	if after != "foo bar\nBAR\nbaz\n" {
		t.Fatalf("original matching: %q", after)
	}
	text, _, after, isErr = runEdit(t, "alpha\nbeta\ngamma\n", []editEntry{{"alpha\n", "ALPHA\n"}, {"missing\n", "MISSING\n"}})
	if !isErr || !strings.Contains(text, "Could not find") || after != "alpha\nbeta\ngamma\n" {
		t.Fatalf("partial apply: %q %q", text, after)
	}
	text, _, _, isErr = runEdit(t, "one\ntwo\nthree\n", []editEntry{{"one\ntwo\n", "ONE\nTWO\n"}, {"two\nthree\n", "TWO\nTHREE\n"}})
	if !isErr || !strings.Contains(text, "overlap") {
		t.Fatalf("overlap: %q", text)
	}
	text, _, _, isErr = runEdit(t, "foo foo foo", []editEntry{{"foo", "bar"}})
	if !isErr || !strings.Contains(text, "Found 3 occurrences") {
		t.Fatalf("duplicates: %q", text)
	}
}

// TOOL-23: write reports upstream's text.
func TestWriteToolUpstreamCases(t *testing.T) {
	dir := t.TempDir()
	write := &WriteTool{CWD: dir, Queue: NewFileMutationQueue()}
	res := runFileTool(t, write, context.Background(), map[string]any{"path": "nested/dir/test.txt", "content": "hello"})
	if res.IsError || res.Content != "Successfully wrote to nested/dir/test.txt" {
		t.Fatalf("res = %+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "nested", "dir", "test.txt")); string(got) != "hello" {
		t.Fatalf("file = %q", got)
	}
}

// TOOL-24: cancelled file tools fail with "Operation aborted" and do not
// touch the file.
func TestFileToolsAbort(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	queue := NewFileMutationQueue()
	for _, c := range []struct {
		tool   agent.AgentTool
		params map[string]any
	}{
		{&EditTool{CWD: dir, Queue: queue}, map[string]any{"path": "f.txt", "edits": []editEntry{{"a", "b"}}}},
		{&WriteTool{CWD: dir, Queue: queue}, map[string]any{"path": "f.txt", "content": "x"}},
		{&LsTool{CWD: dir}, map[string]any{}},
		{&ReadTool{CWD: dir}, map[string]any{"path": "f.txt"}},
	} {
		res := runFileTool(t, c.tool, ctx, c.params)
		if !res.IsError || res.Content != "Operation aborted" {
			t.Errorf("%s: %+v", c.tool.Name(), res)
		}
	}
	if got, _ := os.ReadFile(file); string(got) != "a\n" {
		t.Fatalf("aborted tools changed the file: %q", got)
	}
}

// TOOL-25: ls follows symlinks, skips entries it cannot stat, and reports
// upstream's errors.
func TestLsToolUpstreamBehavior(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, ".hidden"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "file.txt"), nil, 0o644)
	testenv.Symlink(t, filepath.Join(dir, "real"), filepath.Join(dir, "linkdir"))
	testenv.Symlink(t, filepath.Join(dir, "gone"), filepath.Join(dir, "broken"))
	ls := &LsTool{CWD: dir}
	lines := strings.Split(runFileTool(t, ls, context.Background(), map[string]any{}).Content, "\n")
	if !slices.Equal(lines, []string{".hidden", "file.txt", "linkdir/", "real/"}) {
		t.Fatalf("lines = %q", lines)
	}
	missing := filepath.Join(dir, "nope")
	if res := runFileTool(t, ls, context.Background(), map[string]any{"path": "nope"}); !res.IsError || res.Content != "Path not found: "+missing {
		t.Fatalf("missing: %+v", res)
	}
	file := filepath.Join(dir, "file.txt")
	if res := runFileTool(t, ls, context.Background(), map[string]any{"path": "file.txt"}); !res.IsError || res.Content != "Not a directory: "+file {
		t.Fatalf("file: %+v", res)
	}
	// Upstream: with limit 0 the loop stops before the first entry.
	if res := runFileTool(t, ls, context.Background(), map[string]any{"limit": 0}); res.Content != "(empty directory)" {
		t.Fatalf("limit 0: %+v", res)
	}
}
