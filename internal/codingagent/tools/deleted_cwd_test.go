package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi resolves each tool path with path.resolve, which throws ENOENT from
// process.cwd() when the tool's cwd is relative and the working directory was
// removed; the tool call fails instead of touching a path relative to nowhere.
func TestToolsFailWhenTheWorkingDirectoryIsGone(t *testing.T) {
	testenv.DeletedWorkingDirectory(t)
	type executor interface {
		Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error)
	}
	tools := map[string]struct {
		tool   executor
		params string
	}{
		"read":  {&ReadTool{}, `{"path":"a.txt"}`},
		"write": {&WriteTool{}, `{"path":"a.txt","content":"x"}`},
		"edit":  {&EditTool{}, `{"path":"a.txt","edits":[{"oldText":"a","newText":"b"}]}`},
		"ls":    {&LsTool{}, `{}`},
		"grep":  {&GrepTool{}, `{"pattern":"a"}`},
		"find":  {&FindTool{}, `{"pattern":"*"}`},
	}
	for name, tc := range tools {
		t.Run(name, func(t *testing.T) {
			if _, err := tc.tool.Execute(context.Background(), "id", json.RawMessage(tc.params), nil); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("Execute = %v; want the process.cwd() error", err)
			}
		})
	}
	if got, err := resolveToCwd("a.txt", "/work"); err != nil || got != rooted(t, "/work/a.txt") {
		t.Fatalf("an absolute cwd never reads the working directory: %q, %v", got, err)
	}
	if _, err := resolveReadPath("a.txt", ""); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resolveReadPath = %v", err)
	}
	if _, err := canonicalKey("a.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canonicalKey = %v; the mutation queue key must not be a relative path", err)
	}
	if preview := ComputeEditsDiff("a.txt", nil, ""); preview.Error == "" {
		t.Fatal("ComputeEditsDiff must report the failure")
	}
}

func TestFindResultsKeepTheirPathWhenTheWorkingDirectoryIsGone(t *testing.T) {
	testenv.DeletedWorkingDirectory(t)
	if got := relativizeFindResultPath("/abs/dir/a.txt", "rel"); got != "/abs/dir/a.txt" {
		t.Fatalf("relativizeFindResultPath = %q", got)
	}
}
