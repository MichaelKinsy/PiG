package coding

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream agent-session.ts:1661-1665,2020: every prompt applies the whole tool loadout (_preparePromptAndToolLoadout calls _applyToolLoadout), so the descriptions a prepareLoadout hook sets are the ones the request declares. The loadout must not fall back to the registered descriptions when a prompt starts.
func TestPrepareLoadoutDescriptionsReachThePromptRequest(t *testing.T) {
	var toolCalls []string
	var mu sync.Mutex
	session, faux := newOrchestrationSession(t, orchestratorExtension(&toolCalls, &mu))
	var declared []string
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			for _, tool := range ai.GetCurrentTools(request.Messages()) {
				declared = append(declared, tool.Name+": "+tool.Description)
			}
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}.AssistantMessage(), nil
		}),
	})
	if err := session.Prompt(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	// echo's declaration is hidden; run_tools is declared with the hook's description.
	assertEqual(t, "declared tools", declared, []string{"run_tools: Runs tools: echo, helper"})
	for _, tool := range session.Agent().Tools() {
		if tool.Name() == "echo" && tool.Schema().Description != "Echo text (also callable from run_tools)." {
			t.Fatalf("echo description after the prompt = %q", tool.Schema().Description)
		}
	}
}

// upstream agent-session.ts:1500-1560: prepareLoadout is a plain synchronous call, so a hook may read the session's tools (pi.getAllTools(), ctx.tools). A subprocess hook always does: the host pushes extension state, which lists the tools, before it asks. The Session must not hold its tool registry lock across the hook.
func TestPrepareLoadoutHookMayReadTheSessionTools(t *testing.T) {
	var session *Session
	var mu sync.Mutex
	var seen []string
	hooked := orchestrationTool("hooked", "Hooked.", extension.ToolDefinition{
		PrepareLoadout: func(extension.ToolLoadout) *extension.ToolLoadoutChanges {
			mu.Lock()
			current := session
			mu.Unlock()
			if current != nil {
				for _, tool := range current.GetAllTools() {
					seen = append(seen, tool.Name)
				}
				_ = current.CallableToolNames()
			}
			return &extension.ToolLoadoutChanges{Descriptions: map[string]string{"hooked": "Hooked, rewritten."}}
		},
		Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			return textResult("x"), nil
		},
	})
	created, _ := newOrchestrationSession(t, extension.Extension{Tools: map[string]extension.RegisteredTool{"hooked": hooked}, ToolOrder: []string{"hooked"}})
	mu.Lock()
	session = created
	mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		created.SetActiveToolsByName([]string{"hooked"})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("SetActiveToolsByName did not return: a prepareLoadout hook that reads the session's tools deadlocked")
	}
	assertEqual(t, "tools the hook read", seen, []string{"hooked"})
	if got := created.Agent().Tools()[0].Schema().Description; got != "Hooked, rewritten." {
		t.Fatalf("description = %q", got)
	}
}

// Agent events read the nested-call runner on the agent goroutine (or a parallel tool's goroutine, which reports progress through the agent) while the first ctx.executeTool() of another tool creates it on its own goroutine. Upstream is single-threaded (agent-session.ts:695 `this._nestedToolCalls ??=`, :1063 `if (this._nestedToolCalls)`). Run with -race.
func TestNestedCallRunnerCreationDoesNotRaceAgentEvents(t *testing.T) {
	session, _ := newOrchestrationSession(t, extension.Extension{})
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		_ = session.nestedToolCalls()
	})
	wg.Go(func() {
		<-start
		_ = session.handleAgentEvent(context.Background(), agent.ToolExecutionUpdateEvent{ToolCallID: "call", ToolName: "chatty", PartialResult: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "tick"}}}})
	})
	close(start)
	wg.Wait()
	if session.nestedToolCalls() == nil {
		t.Fatal("no nested-call runner")
	}
}
