package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi 0.87.1 agent-loop.ts:863-867 turns built-in throws into content plus details:{}.
// These built-ins represent the same throws with IsError rather than a Go error.
func TestBuiltinFailureDetails(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "file"), []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		tool        agent.AgentTool
		args, valid string
	}{
		{"read", &ReadTool{CWD: cwd}, `{"path":"missing"}`, `{"path":"file"}`},
		{"write", &WriteTool{CWD: cwd}, `{"path":".","content":"changed"}`, `{"path":"file","content":"changed"}`},
		{"edit", &EditTool{CWD: cwd}, `{"path":"missing","edits":[{"oldText":"a","newText":"b"}]}`, `{"path":"file","edits":[{"oldText":"unchanged","newText":"changed"}]}`},
		{"bash", &BashTool{CWD: cwd}, `{"command":"exit 7"}`, `{"command":"printf changed > file"}`},
		{"grep", &GrepTool{CWD: cwd}, `{"pattern":"["}`, `{"pattern":"unchanged","path":"file"}`},
		{"find", &FindTool{CWD: cwd}, `{"pattern":"*","path":"missing"}`, `{"pattern":"*"}`},
		{"ls", &LsTool{CWD: cwd}, `{"path":"missing"}`, `{}`},
	} {
		for _, abort := range []bool{false, true} {
			name := tc.name + "/failure"
			if abort {
				name = tc.name + "/abort"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				args := tc.args
				if abort {
					cancel()
					args = tc.valid
				}
				result, err := tc.tool.Execute(ctx, "call", json.RawMessage(args), nil)
				if err != nil || !result.IsError {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				data, err := json.Marshal(result.Details)
				if err != nil || string(data) != "{}" {
					t.Fatalf("failure details=%s err=%v; want {}", data, err)
				}
			})
		}
	}
	data, err := os.ReadFile(filepath.Join(cwd, "file"))
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("aborted mutation: %q %v", data, err)
	}
}
