package harness

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// A throwing beforeTool hook blocks the call with its error text (tool.ts:62-72: the hook runs inside a try whose
// catch sets block). A Go hook throws by returning an error or by panicking; neither may let the tool run, and the
// first block skips later handlers.
func TestBeforeToolFailureBlocks(t *testing.T) {
	cases := []struct {
		name  string
		throw func() (*BeforeToolResult, error)
	}{
		{"error", func() (*BeforeToolResult, error) { return nil, errors.New("policy unavailable") }},
		{"panic", func() (*BeforeToolResult, error) { panic(errors.New("policy unavailable")) }},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			setup := chatSetup(t)
			var executed, later atomic.Int32
			addTool(t, setup.Registry, new(durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: "work", Description: "work", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}, Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				executed.Add(1)
				return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
			}}))
			addHooks(t, setup.Registry, ToolTask, &ToolHooks{BeforeTool: func(context.Context, ai.ToolCall, HookApi) (*BeforeToolResult, error) {
				return each.throw()
			}})
			addHooks(t, setup.Registry, ToolTask, &ToolHooks{BeforeTool: func(context.Context, ai.ToolCall, HookApi) (*BeforeToolResult, error) {
				later.Add(1)
				return nil, nil
			}})
			setup.Faux.SetResponses([]ai.FauxResponseStep{toolCalls([2]string{"work", "c1"}), fauxAnswer("done")})
			harness, root := openChat(t, storage.NewMemoryStorage(), setup)
			expectSettled(t, must(submitInput(t, root, "go").Wait(testContext)), durable.SubmissionDone, "")
			results := toolResults(t, root)
			if len(results) != 1 {
				t.Fatalf("tool results = %d, want 1", len(results))
			}
			if !results[0].IsError || resultText(results[0]) != "<harness>\n[error] Tool call blocked: policy unavailable\n</harness>" {
				t.Fatalf("result = %v %q", results[0].IsError, resultText(results[0]))
			}
			if executed.Load() != 0 || later.Load() != 0 {
				t.Fatalf("executed = %d, later hooks = %d; want 0, 0", executed.Load(), later.Load())
			}
			closeHarness(t, harness)
		})
	}
}

// resultText joins a tool result's text parts with "|".
func resultText(message ai.ToolResultMessage) string {
	parts := []string{}
	for _, item := range message.Content {
		text, _ := item.(ai.TextContent)
		parts = append(parts, text.Text)
	}
	return strings.Join(parts, "|")
}

// The prepared arguments are what validation sees (tool.ts:138-143 passes prepareArguments' return value to
// validateToolArguments unchanged). A Go repair may return any JSON-encodable object, such as an ai.JsonObject or a
// struct; it must reach validation as that object, not as missing arguments.
func TestPrepareArgumentsTypedObject(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(any) (any, error)
	}{
		{"map", func(any) (any, error) { return map[string]any{"path": "a.txt"}, nil }},
		{"ai.JsonObject", func(any) (any, error) { return ai.JsonObject{"path": "a.txt"}, nil }},
		{"struct", func(any) (any, error) {
			return struct {
				Path string `json:"path"`
			}{"a.txt"}, nil
		}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			setup := chatSetup(t)
			var received atomic.Value
			addTool(t, setup.Registry, new(durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: "work", Description: "work", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []any{"path"}}}, PrepareArguments: each.prepare, Execute: func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				object, _ := args.(map[string]any)
				received.Store(object["path"])
				return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}, nil
			}}))
			setup.Faux.SetResponses([]ai.FauxResponseStep{toolCalls([2]string{"work", "c1"}), fauxAnswer("done")})
			harness, root := openChat(t, storage.NewMemoryStorage(), setup)
			expectSettled(t, must(submitInput(t, root, "go").Wait(testContext)), durable.SubmissionDone, "")
			results := toolResults(t, root)
			if len(results) != 1 || results[0].IsError || resultText(results[0]) != "ok" {
				t.Fatalf("results = %d, first = %v %q", len(results), len(results) > 0 && results[0].IsError, func() string {
					if len(results) == 0 {
						return ""
					}
					return resultText(results[0])
				}())
			}
			if path := received.Load(); path != "a.txt" {
				t.Fatalf("execute received path %v, want a.txt", path)
			}
			closeHarness(t, harness)
		})
	}
}
