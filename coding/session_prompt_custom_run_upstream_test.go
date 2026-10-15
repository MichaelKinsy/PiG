package coding

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// promptCustomRunNoopTool is the suite's "noop" tool: no parameters, and it answers "ok".
type promptCustomRunNoopTool struct{}

func (promptCustomRunNoopTool) Name() string  { return "noop" }
func (promptCustomRunNoopTool) Label() string { return "Noop" }
func (promptCustomRunNoopTool) ExecutionMode() agent.ToolExecutionMode {
	return agent.ToolModeParallel
}
func (promptCustomRunNoopTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "noop", Description: "Does nothing", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (promptCustomRunNoopTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: map[string]any{}}, nil
}

// Ports packages/coding-agent/test/suite/agent-session-prompt.test.ts (Pi 1.2.0) "runs started by a custom message" (issues #10267, #5581):
// a run started by sendMessage(..., { triggerTurn: true }) skipped before_agent_start, so its second request patched away the sections
// handlers had added.
func TestUpstreamSessionPromptRunsStartedByACustomMessage(t *testing.T) {
	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:569.
	t.Run("keeps the same system prompt for every request of the run", func(t *testing.T) {
		var mu sync.Mutex
		var events []extension.BeforeAgentStartEvent
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
			event := args[0].(extension.BeforeAgentStartEvent)
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
			*event.SystemPromptOptions.Sections = append(*event.SystemPromptOptions.Sections, ai.PromptSection{Name: "extension_rules", Value: new("Answer in one word.")})
			return nil, nil
		}}}}
		h := newRecoveryHarness(t, harnessOptions{extension: ext, tools: []agent.AgentTool{promptCustomRunNoopTool{}}})
		var prompts []string
		record := func(response scriptedResponse) scriptedResponse {
			return func(messages []ai.Message) *ai.AssistantMessage {
				prompts = append(prompts, ai.GetCurrentSystemPrompt(messages))
				return response(messages)
			}
		}
		h.provider.responses = []scriptedResponse{record(fauxReply("hi", ai.StopReasonStop, 0)), record(fauxToolCall("noop")), record(fauxReply("done", ai.StopReasonStop, 0))}

		if err := h.session.Prompt(t.Context(), "hello"); err != nil {
			t.Fatal(err)
		}
		if err := h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "task-done", Content: "background task finished", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)}); err != nil {
			t.Fatal(err)
		}

		if len(prompts) != 3 {
			t.Fatalf("requests = %d, want 3", len(prompts))
		}
		if !strings.Contains(prompts[0], "Answer in one word.") {
			t.Errorf("the first request's system prompt lacks the handler's section: %q", prompts[0])
		}
		if prompts[1] != prompts[0] || prompts[2] != prompts[0] {
			t.Errorf("the system prompt changed between requests:\n%q\n%q\n%q", prompts[0], prompts[1], prompts[2])
		}
		mu.Lock()
		defer mu.Unlock()
		types := []extension.BeforeAgentStartMessageType{}
		for _, event := range events {
			types = append(types, event.MessageType)
		}
		if !slices.Equal(types, []extension.BeforeAgentStartMessageType{extension.BeforeAgentStartUser, extension.BeforeAgentStartCustom}) {
			t.Fatalf("before_agent_start messageType = %v, want [user custom]", types)
		}
		if events[1].Message == nil || events[1].Message.CustomType != "task-done" {
			t.Errorf("the custom event's message = %+v, want customType task-done", events[1].Message)
		}
	})

	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:615.
	t.Run("queues a custom message sent while before_agent_start runs into the starting run", func(t *testing.T) {
		handlerStarted, release := make(chan struct{}), make(chan struct{})
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
			event := args[0].(extension.BeforeAgentStartEvent)
			if event.MessageType != extension.BeforeAgentStartCustom || event.Message == nil || event.Message.CustomType != "first" {
				return nil, nil
			}
			close(handlerStarted)
			<-release
			return nil, nil
		}}}}
		h := newRecoveryHarness(t, harnessOptions{extension: ext})
		var requests [][]ai.Message
		h.provider.responses = []scriptedResponse{func(messages []ai.Message) *ai.AssistantMessage {
			requests = append(requests, messages)
			return fauxReply("handled both", ai.StopReasonStop, 0)(messages)
		}}

		first := make(chan error, 1)
		go func() {
			first <- h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "first", Content: "task one finished", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)})
		}()
		<-handlerStarted
		if h.session.IsIdle() {
			t.Error("the Session is idle while the starting run's before_agent_start handler runs")
		}
		second := h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "second", Content: "task two finished", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)})
		close(release)
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		if second != nil {
			t.Fatal(second)
		}

		if len(requests) != 1 {
			t.Fatalf("requests = %d, want 1", len(requests))
		}
		if got := queueProviderUserTexts(requests[0]); !slices.Equal(got, []string{"task one finished", "task two finished"}) {
			t.Fatalf("user texts = %q, want [task one finished task two finished]", got)
		}
	})

	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:662.
	t.Run("includes a custom message sent without triggerTurn while the run starts in its first request", func(t *testing.T) {
		var h *recoveryHarness
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
			return nil, h.session.SendMessage(extension.CustomMessageRef{CustomType: "note", Content: "context from handler", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(false)})
		}}}}
		h = newRecoveryHarness(t, harnessOptions{extension: ext})
		var requests [][]ai.Message
		h.provider.responses = []scriptedResponse{func(messages []ai.Message) *ai.AssistantMessage {
			requests = append(requests, messages)
			return fauxReply("done", ai.StopReasonStop, 0)(messages)
		}}

		if err := h.session.Prompt(t.Context(), "hello"); err != nil {
			t.Fatal(err)
		}

		if len(requests) != 1 {
			t.Fatalf("requests = %d, want 1", len(requests))
		}
		raw, err := json.Marshal(requests[0])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "context from handler") {
			t.Fatalf("the first request lacks the handler's message: %s", raw)
		}
	})
}
