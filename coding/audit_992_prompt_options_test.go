package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// .upstream/v0.99.2/packages/coding-agent/src/core/extensions/runner.ts:1411-1462 hands every before_agent_start handler
// the same mutable `systemPromptOptions` object and returns it as the run's options; agent-session.ts:2032 then builds the
// run's prompt from those options with _preparePromptAndToolLoadout (agent-session.ts:1669-1683), which in 0.99.2 also
// drops the snippets of hidden tools after the handlers ran. A handler that edits `toolSnippets` therefore changes the
// tool list of the run's system prompt.
func TestAuditBeforeAgentStartToolSnippetEditReachesTheRunPrompt(t *testing.T) {
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		options := args[0].(extension.BeforeAgentStartEvent).SystemPromptOptions
		options.ToolSnippets["read"] = "audit edited read snippet"
		return nil, nil
	}}}}
	var prompts []string
	record := func(messages []ai.Message) *ai.AssistantMessage {
		prompts = append(prompts, ai.GetCurrentSystemPrompt(messages))
		return boundaryReply("ok", ai.StopReasonStop, 0)(messages)
	}
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true, extension: ext}, record)
	boundaryPrompt(t, h, "hello")
	if len(prompts) != 1 {
		t.Fatalf("requests = %d, want 1", len(prompts))
	}
	if !strings.Contains(prompts[0], "\n- read: audit edited read snippet") {
		t.Fatalf("run prompt ignores the handler's toolSnippets edit:\n%s", prompts[0])
	}
}
