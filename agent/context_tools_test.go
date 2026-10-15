package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream AgentContext carries the executable tools: AgentTurnContext.context, PrepareRequestContext.context and
// the replacement a callback returns (types.ts AgentTurnContext, AgentLoopTurnUpdate, AgentRequestUpdate) replace
// both the transcript and the tools the run executes (agent-loop.ts currentContext).
func TestPrepareRequestContextReplacesTheExecutableTools(t *testing.T) {
	ran := ""
	original := &scriptTool{name: "echo", params: valueSchema, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		ran += "original;"
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "o"}}}, nil
	}}
	replacement := &scriptTool{name: "echo", params: valueSchema, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		ran += "replacement;"
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "r"}}}, nil
	}}
	for _, tc := range []struct {
		name  string
		tools []AgentTool
		want  string
	}{
		{"replacement tool executes", []AgentTool{replacement}, "replacement;"},
		{"no tools means none execute", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ran = ""
			var seen []AgentTool
			a := mustNewAgent(AgentOptions{
				Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "x"}))}),
				Tools: []AgentTool{original},
				PrepareRequest: func(_ context.Context, request PrepareRequestContext) (*AgentRequestUpdate, error) {
					if seen == nil {
						seen = request.Context.Tools
						return &AgentRequestUpdate{Context: &AgentContext{Messages: request.Context.Messages, Tools: tc.tools}}, nil
					}
					return nil, nil
				},
			})
			mustSend(t, a, "echo")
			if len(seen) != 1 || seen[0].Name() != "echo" {
				t.Fatalf("PrepareRequestContext.Context.Tools = %v, want the agent's tools", seen)
			}
			if ran != tc.want {
				t.Fatalf("executed %q, want %q", ran, tc.want)
			}
		})
	}
}

func TestTurnContextCarriesToolsAndNextTurnUpdateReplacesThem(t *testing.T) {
	ran := ""
	original := &scriptTool{name: "echo", params: valueSchema, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		ran += "original;"
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "o"}}}, nil
	}}
	var finished []AgentTool
	var next []AgentTool
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "x"}))}),
		Tools: []AgentTool{original},
		FinishTurn: func(_ context.Context, turn AgentTurnContext) (*AgentTurnDecision, error) {
			if finished == nil {
				finished = turn.Context.Tools
			}
			return nil, nil
		},
		PrepareNextTurnWithContext: func(_ context.Context, turn PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
			next = turn.Context.Tools
			return &AgentLoopTurnUpdate{Context: &AgentContext{Messages: turn.Context.Messages}}, nil
		},
	})
	mustSend(t, a, "echo")
	if len(finished) != 1 || len(next) != 1 || ran != "original;" {
		t.Fatalf("finishTurn tools=%d nextTurn tools=%d ran=%q, want the agent's tool in each", len(finished), len(next), ran)
	}
}
