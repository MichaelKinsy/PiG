package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// sequentialAdmissionTool is an admissionTool whose calls run one after the other.
type sequentialAdmissionTool struct{ *admissionTool }

func (sequentialAdmissionTool) ExecutionMode() agent.ToolExecutionMode {
	return agent.ToolModeSequential
}

// Pi 4438c58ba (#9986), packages/coding-agent/CHANGELOG.md: "Fixed pressing Esc during a batch of tool calls leaving the calls
// that had not started yet without a result in the session; they now get an "Operation aborted" result". An extension's
// tool_call handler aborts while it decides about the first of three calls. The session file then holds a tool result for each
// call, in call order, and the handler was not asked about the calls that will not run.
func TestSessionAbortedBatchPersistsAResultForEveryCall(t *testing.T) {
	for _, sequential := range []bool{false, true} {
		name := "parallel"
		if sequential {
			name = "sequential"
		}
		t.Run(name, func(t *testing.T) {
			write := &admissionTool{name: "external_write", parameter: "value", label: "External write", description: "Perform an external write"}
			var tool agent.AgentTool = write
			if sequential {
				tool = sequentialAdmissionTool{write}
			}
			var preflights []string
			ext := extension.Extension{Path: "/ext/abort-at-first", Handlers: map[string][]extension.HandlerFn{
				"tool_call": {func(args ...any) (any, error) {
					value := args[0].(extension.CustomToolCallEvent).Input["value"].(string)
					preflights = append(preflights, value)
					if value == "first" {
						return nil, extension.FromContext(args[1].(context.Context)).Abort()
					}
					return nil, nil
				}},
			}}
			h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{tool}, extension: ext}, admissionResponse("external_write", "value", "first", "second", "third"))
			messages, err := h.session.Send(t.Context(), "run three writes")
			if err != nil {
				t.Fatal(err)
			}
			h.settle(t)
			if !slices.Equal(preflights, []string{"first"}) || len(write.executions()) != 0 {
				t.Errorf("tool_call handler asked about %q, executions %q; want only the first call asked and nothing executed", preflights, write.executions())
			}
			ids := []string{"call-first", "call-second", "call-third"}
			var returned []string
			for _, result := range toolResultMessages(messages) {
				returned = append(returned, result.ToolCallID)
				if !result.IsError || result.Text() != "Operation aborted" {
					t.Errorf("result %s = %q isError=%v, want Operation aborted as an error", result.ToolCallID, result.Text(), result.IsError)
				}
			}
			if !slices.Equal(returned, ids) {
				t.Errorf("returned tool results = %q, want %q", returned, ids)
			}

			// The session entries, and the lines of the session file itself.
			type line struct {
				Type    string `json:"type"`
				Message struct {
					Role       string `json:"role"`
					ToolCallID string `json:"toolCallId"`
					IsError    bool   `json:"isError"`
					StopReason string `json:"stopReason"`
				} `json:"message"`
			}
			shape := func(raws [][]byte) []string {
				var out []string
				for _, raw := range raws {
					var entry line
					if err := json.Unmarshal(raw, &entry); err != nil {
						t.Fatalf("%s: %v", raw, err)
					}
					if entry.Type != "message" {
						continue
					}
					switch entry.Message.Role {
					case "toolResult":
						out = append(out, "toolResult "+entry.Message.ToolCallID)
						if !entry.Message.IsError {
							t.Errorf("persisted result %s is not an error", entry.Message.ToolCallID)
						}
					case "assistant":
						out = append(out, "assistant "+entry.Message.StopReason)
					default:
						out = append(out, entry.Message.Role)
					}
				}
				return out
			}
			// The first line declares the session's tools (a system message with toolsAdded).
			want := []string{"system", "user", "assistant toolUse", "toolResult call-first", "toolResult call-second", "toolResult call-third", "assistant aborted"}
			var entries [][]byte
			for _, entry := range h.entries("message") {
				entries = append(entries, entry.Raw())
			}
			if got := shape(entries); !slices.Equal(got, want) {
				t.Errorf("session entries = %q, want %q", got, want)
			}
			file, err := os.ReadFile(h.session.Inner().Path())
			if err != nil {
				t.Fatal(err)
			}
			if got := shape(bytes.Split(bytes.TrimSpace(file), []byte("\n"))); !slices.Equal(got, want) {
				t.Errorf("session file lines = %q, want %q", got, want)
			}
		})
	}
}
