package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The original suite harness uses an empty in-memory manager and configured faux auth. Unlike the older recovery helpers, fauxAssistantMessage preserves empty text blocks and present zero usage.
func newBoundaryHarness(t *testing.T, opts harnessOptions, responses ...scriptedResponse) *recoveryHarness {
	t.Helper()
	opts.emptySessionManager = true
	if opts.maxTokens == 0 {
		opts.maxTokens = 16384
	}
	opts.defaultTools = opts.tools == nil
	if opts.extension.Handlers == nil {
		opts.extension.Handlers = map[string][]extension.HandlerFn{}
	}
	h := newRecoveryHarness(t, opts, responses...)
	if err := h.session.services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
		t.Fatal(err)
	}
	runner := h.session.currentRunner()
	runner.BindCore(extension.ExtensionActions{SendMessage: h.session.SendMessage, SendUserMessage: func(content any, options *extension.SendUserMessageOptions) error {
		return h.session.SendExtensionUserMessage(content, options)
	}}, extension.ContextActions{IsIdle: h.session.IsIdle, HasPendingMessages: h.session.HasPendingMessages, Abort: h.session.RequestAbort, SessionManager: h.session.inner}, nil)
	return h
}

func boundaryReply(text string, reason ai.StopReason, offset time.Duration) scriptedResponse {
	return func(messages []ai.Message) *ai.AssistantMessage {
		reply := fauxReply(text, reason, offset)(messages)
		reply.Content = []ai.AssistantContentBlock{ai.TextContent{Text: text}}
		reply.Usage = ai.Usage{}
		return reply
	}
}

func boundaryDrafts(continued bool, entries ...extension.SessionBoundaryDraft) extension.BoundaryResult {
	return extension.BoundaryResult{Entries: &entries, Continue: &continued}
}

func boundaryJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func boundaryRecord(t *testing.T, h *recoveryHarness, site int, name string) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() && os.Getenv("PIG_BOUNDARY_PROBE") == "1" {
			fmt.Println("SESSION_BOUNDARY_CASE " + boundaryJSON(t, []any{site, name, h.provider.callCount()}))
		}
	})
}

func boundaryPrompt(t *testing.T, h *recoveryHarness, text string) {
	t.Helper()
	if err := h.session.Prompt(t.Context(), text, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.session.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func boundaryContains(t *testing.T, text, substring string, want bool) {
	t.Helper()
	if strings.Contains(text, substring) != want {
		t.Errorf("contains(%q, %q) != %v", text, substring, want)
	}
}

func boundaryEvents[T agent.AgentEvent](h *recoveryHarness) []T {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []T{}
	for _, event := range h.events {
		if event, ok := event.(T); ok {
			out = append(out, event)
		}
	}
	return out
}

func boundaryEntry(t *testing.T, h *recoveryHarness, kind, customType string) map[string]any {
	t.Helper()
	for _, entry := range h.entries(kind) {
		var fields map[string]any
		if err := json.Unmarshal(entry.Raw(), &fields); err != nil {
			t.Fatal(err)
		}
		if customType == "" || fields["customType"] == customType {
			return fields
		}
	}
	t.Errorf("missing %s/%s entry", kind, customType)
	return nil
}

func boundaryLastUser(t *testing.T, h *recoveryHarness) string {
	t.Helper()
	entries := h.session.Inner().GetBranch()
	for _, entry := range slices.Backward(entries) {
		if message, ok := entry.(icodingagent.MessageEntry); ok && message.Message.User != nil {
			return entry.Base().ID
		}
	}
	t.Error("missing user entry")
	return ""
}

func boundaryCapture(t *testing.T, requests *[]string, reply string) scriptedResponse {
	t.Helper()
	return func(messages []ai.Message) *ai.AssistantMessage {
		*requests = append(*requests, boundaryJSON(t, messages))
		return boundaryReply(reply, ai.StopReasonStop, 0)(messages)
	}
}

// Original sites refer to packages/coding-agent/test/suite/agent-session-boundaries.test.ts in Pi 0.87.1.
func TestUpstreamSessionBoundaries(t *testing.T) {
	t.Run("commits a retain-none turn_end compaction and explicitly continues once", func(t *testing.T) {
		handled := false
		observedIDs, requests := []string{}, []string{}
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"turn_end": {func(args ...any) (any, error) {
			event := args[0].(extension.TurnEndEvent)
			observedIDs = append(observedIDs, event.MessageEntryID)
			if handled {
				return nil, nil
			}
			handled = true
			return boundaryDrafts(true, extension.SessionBoundaryDraft{Type: "compaction", Summary: "exact handoff", FirstKeptEntryID: nil, Details: map[string]any{"source": "test"}}), nil
		}}}}
		h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("discarded response", ai.StopReasonStop, 0), boundaryCapture(t, &requests, "continued from handoff"))
		boundaryRecord(t, h, 22, "commits a retain-none turn_end compaction and explicitly continues once")
		boundaryPrompt(t, h, "discarded prompt")
		compaction := boundaryEntry(t, h, "compaction", "")
		if compaction["summary"] != "exact handoff" || compaction["firstKeptEntryId"] != compaction["id"] {
			t.Errorf("compaction=%v", compaction)
		}
		if len(requests) != 1 {
			t.Errorf("requests=%v", requests)
		} else {
			boundaryContains(t, requests[0], "exact handoff", true)
			boundaryContains(t, requests[0], "discarded prompt", false)
			boundaryContains(t, requests[0], "discarded response", false)
		}
		if len(observedIDs) != 2 {
			t.Errorf("observed IDs=%v", observedIDs)
		}
		h.settle(t)
		if got := len(boundaryEvents[agent.AgentSettledEvent](h)); got != 1 {
			t.Errorf("agent_settled=%d", got)
		}
	})
}

func boundaryAppendAssistant(t *testing.T, h *recoveryHarness, text string, reason ai.StopReason, timestamp int64, usage *ai.Usage) string {
	t.Helper()
	if usage == nil {
		usage = &ai.Usage{}
	}
	id, err := h.session.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, Provider: "faux", ModelID: "faux-1", StopReason: reason, Timestamp: timestamp, Usage: usage}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type boundaryTool struct {
	name, label, description, argument, text string
	run                                      func()
}

func (tool boundaryTool) Name() string                           { return tool.name }
func (tool boundaryTool) Label() string                          { return tool.label }
func (tool boundaryTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (tool boundaryTool) Schema() ai.ToolSchema {
	parameters := map[string]any{"type": "object", "properties": map[string]any{}}
	if tool.argument != "" {
		parameters["properties"] = map[string]any{tool.argument: map[string]any{"type": "string"}}
		parameters["required"] = []string{tool.argument}
	}
	return ai.ToolSchema{Name: tool.name, Description: tool.description, Parameters: parameters}
}
func (tool boundaryTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	if tool.run != nil {
		tool.run()
	}
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: tool.text}}, Details: map[string]any{}}, nil
}
func boundaryToolReply(name string, arguments ai.JsonObject, reason ai.StopReason) scriptedResponse {
	return func(messages []ai.Message) *ai.AssistantMessage {
		reply := boundaryReply("", reason, 0)(messages)
		reply.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "call-" + name, Name: name, Arguments: arguments}}
		return reply
	}
}

func boundaryEdit(t *testing.T, h *recoveryHarness, target string, replacement json.RawMessage) {
	t.Helper()
	var value *icodingagent.ContextEditReplacement
	if len(replacement) > 0 {
		value = &icodingagent.ContextEditReplacement{}
		if err := json.Unmarshal(replacement, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.session.Inner().AppendContextEdit(target, value); err != nil {
		t.Fatal(err)
	}
}
