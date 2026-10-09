package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type promptCharacterizationTool struct {
	name, label, description, argument string
	delay                              time.Duration
	run                                func(string)
}

func (tool promptCharacterizationTool) Name() string  { return tool.name }
func (tool promptCharacterizationTool) Label() string { return tool.label }
func (tool promptCharacterizationTool) ExecutionMode() agent.ToolExecutionMode {
	return agent.ToolModeParallel
}
func (tool promptCharacterizationTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: tool.name, Description: tool.description, Parameters: map[string]any{"type": "object", "properties": map[string]any{tool.argument: map[string]any{"type": "string"}}, "required": []string{tool.argument}}}
}
func (tool promptCharacterizationTool) Execute(_ context.Context, _ string, raw json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var params map[string]string
	if err := json.Unmarshal(raw, &params); err != nil {
		return agent.AgentToolResult{}, err
	}
	value := params[tool.argument]
	time.Sleep(tool.delay)
	tool.run(value)
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: tool.name + ":" + value}}, Details: map[string]any{tool.argument: value}}, nil
}

func promptCharacterizationRoles(h *recoveryHarness) []string {
	roles := []string{}
	for _, message := range h.session.Messages() {
		roles = append(roles, message.Role())
	}
	return roles
}

func recordPromptCharacterization(t *testing.T, h *recoveryHarness, site int) {
	t.Helper()
	t.Cleanup(func() {
		if t.Failed() || os.Getenv("PIG_PROMPT_CHARACTERIZATION_PROBE") != "1" {
			return
		}
		name := strings.ReplaceAll(strings.TrimPrefix(t.Name(), "TestUpstreamSessionPromptCharacterization/"), "_", " ")
		record := []any{site, name, promptCharacterizationRoles(h), max(0, len(h.provider.responses)-h.provider.callCount())}
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("SESSION_PROMPT_CASE " + string(raw))
	})
}

func TestUpstreamSessionPromptCharacterization(t *testing.T) {
	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:44.
	t.Run("prompts while idle and records a single text response", func(t *testing.T) {
		h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
		recordPromptCharacterization(t, h, 44)
		h.provider.responses = []scriptedResponse{fauxReply("hello", ai.StopReasonStop, 0)}
		if err := h.session.Prompt(t.Context(), "hi", nil); err != nil {
			t.Fatal(err)
		}
		if got := promptCharacterizationRoles(h); !reflect.DeepEqual(got, []string{"system", "user", "assistant"}) {
			t.Fatalf("roles=%v", got)
		}
		if got := extractUserMessageText(h.session.Messages()[1].User.Content); got != "hi" {
			t.Fatalf("user=%q", got)
		}
		if pending := len(h.provider.responses) - h.provider.callCount(); pending != 0 {
			t.Fatalf("pending responses=%d", pending)
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:57.
	t.Run("handles a tool call turn and waits for the follow-up LLM response", func(t *testing.T) {
		toolRuns := []string{}
		tool := promptCharacterizationTool{name: "echo", label: "Echo", description: "Echo text back", argument: "text", run: func(value string) { toolRuns = append(toolRuns, value) }}
		h := newQueueCharacterizationHarness(t, extension.Extension{}, []agent.AgentTool{tool})
		recordPromptCharacterization(t, h, 57)
		h.provider.responses = []scriptedResponse{bashPersistenceEchoCalls("hello"), fauxReply("done", ai.StopReasonStop, 0)}
		if err := h.session.Prompt(t.Context(), "start", nil); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(toolRuns, []string{"hello"}) {
			t.Fatalf("toolRuns=%v", toolRuns)
		}
		if got := promptCharacterizationRoles(h); !reflect.DeepEqual(got, []string{"system", "user", "assistant", "toolResult", "assistant"}) {
			t.Fatalf("roles=%v", got)
		}
		if h.session.Messages()[3].Role() != "toolResult" || h.session.Messages()[4].Role() != "assistant" {
			t.Fatal("follow-up turn was not awaited")
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:95.
	t.Run("executes multiple tool calls from one response and continues with a single follow-up response", func(t *testing.T) {
		toolRuns := []string{}
		var mu sync.Mutex
		makeTool := func(name string, delay time.Duration) agent.AgentTool {
			return promptCharacterizationTool{name: name, label: name, description: name + " tool", argument: "value", delay: delay, run: func(value string) { mu.Lock(); defer mu.Unlock(); toolRuns = append(toolRuns, name+":"+value) }}
		}
		h := newQueueCharacterizationHarness(t, extension.Extension{}, []agent.AgentTool{makeTool("slow", 25*time.Millisecond), makeTool("fast", 0)})
		recordPromptCharacterization(t, h, 95)
		h.provider.responses = []scriptedResponse{func([]ai.Message) *ai.AssistantMessage {
			return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "slow", Name: "slow", Arguments: ai.JsonObject{"value": "a"}}, ai.ToolCall{ID: "fast", Name: "fast", Arguments: ai.JsonObject{"value": "b"}}}}
		}, func(messages []ai.Message) *ai.AssistantMessage {
			count := 0
			for _, message := range messages {
				if _, ok := message.(ai.ToolResultMessage); ok {
					count++
				}
			}
			return fauxReply(fmt.Sprintf("tool results: %d", count), ai.StopReasonStop, 0)(messages)
		}}
		if err := h.session.Prompt(t.Context(), "run tools", nil); err != nil {
			t.Fatal(err)
		}
		slices.Sort(toolRuns)
		if !reflect.DeepEqual(toolRuns, []string{"fast:b", "slow:a"}) {
			t.Fatalf("toolRuns=%v", toolRuns)
		}
		results := 0
		for _, message := range h.session.Messages() {
			if message.Role() == "toolResult" {
				results++
			}
		}
		if results != 2 {
			t.Fatalf("tool results=%d", results)
		}
		if messages := h.session.Messages(); messages[len(messages)-1].Role() != "assistant" {
			t.Fatal("last message is not assistant")
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:313.
	t.Run("dispatches extension commands without consuming a provider response", func(t *testing.T) {
		commandRuns := []string{}
		h := newQueueCharacterizationHarness(t, queueCommandExtension(func(_ context.Context, args string) error { commandRuns = append(commandRuns, args); return nil }), nil)
		recordPromptCharacterization(t, h, 313)
		h.provider.responses = []scriptedResponse{fauxReply("should stay queued", ai.StopReasonStop, 0)}
		if err := h.session.Prompt(t.Context(), "/testcmd hello world", nil); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(commandRuns, []string{"hello world"}) {
			t.Fatalf("commands=%v", commandRuns)
		}
		if messages := h.session.Messages(); len(messages) != 0 {
			t.Fatalf("messages=%v", messages)
		}
		if pending := len(h.provider.responses) - h.provider.callCount(); pending != 1 {
			t.Fatalf("pending responses=%d", pending)
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:378.
	t.Run("does not report streamingBehavior to input handlers while idle", func(t *testing.T) {
		inputEvents := []extension.InputEvent{}
		h := newQueueCharacterizationHarness(t, extension.Extension{Handlers: map[string][]extension.HandlerFn{"input": {func(args ...any) (any, error) {
			inputEvents = append(inputEvents, args[0].(extension.InputEvent))
			return nil, nil
		}}}}, nil)
		recordPromptCharacterization(t, h, 378)
		h.provider.responses = []scriptedResponse{fauxReply("ok", ai.StopReasonStop, 0)}
		if err := h.session.Prompt(t.Context(), "idle", &PromptOptions{StreamingBehavior: extension.DeliverAsFollowUp}); err != nil {
			t.Fatal(err)
		}
		if len(inputEvents) != 1 {
			t.Fatalf("input events=%v", inputEvents)
		}
		if inputEvents[0].StreamingBehavior != "" {
			t.Fatalf("streamingBehavior=%q", inputEvents[0].StreamingBehavior)
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:398.
	t.Run("reports streamingBehavior to input handlers while streaming", func(t *testing.T) {
		inputEvents := []extension.InputEvent{}
		waiting := createQueueWaitingHarness(t, extension.Extension{Handlers: map[string][]extension.HandlerFn{"input": {func(args ...any) (any, error) {
			inputEvents = append(inputEvents, args[0].(extension.InputEvent))
			return nil, nil
		}}}})
		waiting.setResponses(fauxToolCall("wait"), fauxReply("done", ai.StopReasonStop, 0))
		<-waiting.waitForToolStart
		recordPromptCharacterization(t, waiting.h, 398)
		if err := waiting.h.session.Prompt(t.Context(), "queued", &PromptOptions{StreamingBehavior: extension.DeliverAsFollowUp}); err != nil {
			t.Fatal(err)
		}
		behaviors := []string{}
		for _, event := range inputEvents {
			behaviors = append(behaviors, event.StreamingBehavior)
		}
		if !reflect.DeepEqual(behaviors, []string{"", "followUp"}) {
			t.Fatalf("streamingBehavior=%v", behaviors)
		}
		waiting.releaseToolExecution()
		waiting.join()
	})
	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:452.
	t.Run("throws when prompted during streaming without a streamingBehavior", func(t *testing.T) {
		waiting := createQueueWaitingHarness(t, extension.Extension{})
		waiting.setResponses(fauxToolCall("wait"), fauxReply("done", ai.StopReasonStop, 0))
		<-waiting.waitForToolStart
		recordPromptCharacterization(t, waiting.h, 452)
		err := waiting.h.session.Prompt(t.Context(), "second", nil)
		if err == nil || !strings.Contains(err.Error(), "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message.") {
			t.Fatalf("error=%v", err)
		}
		waiting.releaseToolExecution()
		waiting.join()
	})
	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:497.
	t.Run("throws when prompted during manual compaction", func(t *testing.T) {
		started, released := make(chan struct{}), make(chan struct{})
		release := sync.OnceFunc(func() { close(released) })
		h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true, defaultTools: true, settings: `{"compaction":{"keepRecentTokens":1}}`, extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_before_compact": {func(args ...any) (any, error) {
				event := args[0].(extension.SessionBeforeCompactEvent)
				close(started)
				<-released
				raw, err := json.Marshal(event.Preparation)
				if err != nil {
					return nil, err
				}
				var preparation struct {
					FirstKeptEntryID string
					TokensBefore     int
				}
				if err := json.Unmarshal(raw, &preparation); err != nil {
					return nil, err
				}
				return extension.SessionBeforeCompactResult{Compaction: &extension.CompactionResult{Summary: "manual compacted", FirstKeptEntryID: preparation.FirstKeptEntryID, TokensBefore: preparation.TokensBefore, Details: map[string]any{}}}, nil
			}},
		}}})
		if err := h.session.services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
			t.Fatal(err)
		}
		recordPromptCharacterization(t, h, 497)
		h.provider.responses = []scriptedResponse{fauxReply("one", ai.StopReasonStop, 0), fauxReply("two", ai.StopReasonStop, 0)}
		for _, text := range []string{"first", "second"} {
			if err := h.session.Prompt(t.Context(), text, nil); err != nil {
				t.Fatal(err)
			}
		}
		done := make(chan error, 1)
		go func() { _, err := h.session.Compact(t.Context(), ""); done <- err }()
		defer func() {
			release()
			if err := <-done; err != nil {
				t.Error(err)
			}
		}()
		<-started
		err := h.session.Prompt(t.Context(), "third", nil)
		if err == nil || !strings.Contains(err.Error(), "Cannot submit a prompt while compaction is in progress. Wait for compaction to finish and retry.") {
			t.Fatalf("error=%v", err)
		}
		release()
	})
	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:543.
	t.Run("throws when prompting without a model", func(t *testing.T) {
		h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
		recordPromptCharacterization(t, h, 543)
		h.session.Agent().SetModel(nil)
		if err := h.session.Prompt(t.Context(), "hi", nil); err == nil || !strings.Contains(err.Error(), "No model selected.") {
			t.Fatalf("error=%v", err)
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:551.
	t.Run("throws when prompting without configured auth", func(t *testing.T) {
		h := newModelExtensionHarness(t, []bool{false}, "", false, extension.Extension{}, nil)
		if err := h.session.Prompt(t.Context(), "hi", nil); err == nil || !strings.Contains(err.Error(), "No API key found for faux.") {
			t.Fatalf("error=%v", err)
		}
	})
}
