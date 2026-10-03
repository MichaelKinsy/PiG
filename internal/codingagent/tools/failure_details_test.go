package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi 0.99.1 agent-loop.ts:906-910 turns built-in throws into content plus details:{}, and that result has no isError.
// These built-ins represent the same throws with IsError and Thrown rather than a Go error: the agent reports the call as
// failed and drops the result's own isError (agent.AgentToolResult.Thrown).
// The one exception is a shell command that exits non-zero: since upstream 0.99.1
// (.upstream/v0.99.1/packages/coding-agent/src/core/tools/bash.ts:401-407) it is a
// returned isError result, not a throw, so it keeps its own (absent) details and
// carries structuredContent.
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
				if tc.name == "bash" && !abort {
					if result.Details != nil || result.StructuredContent == nil {
						t.Fatalf("returned error result: details=%#v structured=%s; want no details and structuredContent", result.Details, result.StructuredContent)
					}
					if result.Thrown {
						t.Fatal("a non-zero exit is a returned isError result, not a thrown error (bash.ts:403-409)")
					}
					return
				}
				if !result.Thrown {
					t.Fatalf("result=%+v: a thrown error must be marked Thrown so its isError stays out of the result (agent-loop.ts:906-910)", result)
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
