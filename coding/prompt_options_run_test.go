package coding

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// .upstream/v0.99.2/packages/coding-agent/src/core/agent-session.ts:1747-1748,697-709 builds every request of a run from the options the before_agent_start handlers shared: toolGuidelines and appendSystemPrompt edits, not only toolSnippets, change the run's prompt, and later turns of the run keep them.
func TestBeforeAgentStartOptionEditsReachEveryTurnOfTheRunPrompt(t *testing.T) {
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		options := extension.BeforeAgentStartOptions(args[1].(context.Context))
		options.ToolSnippets["read"] = "audit edited read snippet"
		options.ToolGuidelines["read"] = []string{"AUDIT-GUIDELINE-MARK"}
		options.AppendSystemPrompt = "AUDIT-APPEND-MARK"
		return nil, nil
	}}}}
	var prompts []string
	record := func(reply scriptedResponse) scriptedResponse {
		return func(messages []ai.Message) *ai.AssistantMessage {
			prompts = append(prompts, ai.GetCurrentSystemPrompt(messages))
			return reply(messages)
		}
	}
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true, extension: ext},
		record(boundaryToolReply("read", ai.JsonObject{"path": "missing.txt"}, ai.StopReasonToolUse)),
		record(boundaryReply("ok", ai.StopReasonStop, 0)))
	boundaryPrompt(t, h, "hello")
	if len(prompts) != 2 {
		t.Fatalf("requests = %d, want 2", len(prompts))
	}
	for i, prompt := range prompts {
		for _, want := range []string{"\n- read: audit edited read snippet", "AUDIT-GUIDELINE-MARK", "AUDIT-APPEND-MARK"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("request %d prompt lacks %q:\n%s", i, want, prompt)
			}
		}
	}
	if strings.Contains(h.session.SystemPrompt(), "AUDIT-APPEND-MARK") {
		t.Errorf("the edits outlived the run:\n%s", h.session.SystemPrompt())
	}
}

// agent-session.ts:697-709 merges the base snippets under the run's before each later turn, so a snippet a handler deleted for the first request returns for the next one, while the first request honors the edit (agent-session.ts:1747).
func TestRunPromptRefreshesBaseSnippetsBeforeLaterTurns(t *testing.T) {
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		delete(extension.BeforeAgentStartOptions(args[1].(context.Context)).ToolSnippets, "read")
		return nil, nil
	}}}}
	var prompts []string
	record := func(reply scriptedResponse) scriptedResponse {
		return func(messages []ai.Message) *ai.AssistantMessage {
			prompts = append(prompts, ai.GetCurrentSystemPrompt(messages))
			return reply(messages)
		}
	}
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true, extension: ext},
		record(boundaryToolReply("read", ai.JsonObject{"path": "missing.txt"}, ai.StopReasonToolUse)),
		record(boundaryReply("ok", ai.StopReasonStop, 0)))
	boundaryPrompt(t, h, "hello")
	if len(prompts) != 2 {
		t.Fatalf("requests = %d, want 2", len(prompts))
	}
	if strings.Contains(prompts[0], "\n- read: ") {
		t.Errorf("the first request ignores the handler's deletion:\n%s", prompts[0])
	}
	if !strings.Contains(prompts[1], "\n- read: ") {
		t.Errorf("the next turn does not refresh the base snippets:\n%s", prompts[1])
	}
}

// The forced prompt is the run options' forceSystemPrompt (runner.ts:1445-1447 sets it from a returned systemPrompt; agent-session.ts:1724 projects `_runSystemPromptOptions?.forceSystemPrompt`). A handler that sets the field directly forces the request's prompt, and a later handler that clears it removes an earlier handler's forced prompt.
func TestBeforeAgentStartForceSystemPromptOptionDecidesTheForcedPrompt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		handlers []extension.HandlerFn
		want     string
		absent   string
	}{
		{"set directly", []extension.HandlerFn{func(args ...any) (any, error) {
			extension.BeforeAgentStartOptions(args[1].(context.Context)).ForceSystemPrompt = new("DIRECT-FORCED-PROMPT")
			return nil, nil
		}}, "DIRECT-FORCED-PROMPT", ""},
		{"cleared by a later handler", []extension.HandlerFn{func(...any) (any, error) {
			return &extension.BeforeAgentStartEventResult{SystemPrompt: new("RETURNED-FORCED-PROMPT")}, nil
		}, func(args ...any) (any, error) {
			extension.BeforeAgentStartOptions(args[1].(context.Context)).ForceSystemPrompt = nil
			return nil, nil
		}}, "\n- read: ", "RETURNED-FORCED-PROMPT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var prompts []string
			record := func(messages []ai.Message) *ai.AssistantMessage {
				prompts = append(prompts, ai.GetCurrentSystemPrompt(messages))
				return boundaryReply("ok", ai.StopReasonStop, 0)(messages)
			}
			ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": tc.handlers}}
			h := newRecoveryHarness(t, harnessOptions{defaultTools: true, extension: ext}, record)
			boundaryPrompt(t, h, "hello")
			if len(prompts) != 1 {
				t.Fatalf("requests = %d, want 1", len(prompts))
			}
			if !strings.Contains(prompts[0], tc.want) || (tc.absent != "" && strings.Contains(prompts[0], tc.absent)) {
				t.Fatalf("request prompt:\n%s", prompts[0])
			}
		})
	}
}
