package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports packages/coding-agent/test/suite/regressions/1717-2113-agent-session-event-settlement.test.ts:29.
// Pi awaits a yielding message_end handler and persists the assistant before either tool result.
func TestSessionEventSettlementPreservesMessageOrderUpstream(t *testing.T) {
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"message_end": {func(args ...any) (any, error) {
			message := args[0].(extension.MessageEndEvent).Message
			if message.Assistant != nil {
				// The upstream handler deliberately yields for 20ms; this is the stimulus, not a wait for test completion.
				time.Sleep(20 * time.Millisecond)
			}
			return nil, nil
		}},
	}}
	tools := &modelExtensionEchoTool{}
	h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{tools}, extension: ext},
		func([]ai.Message) *ai.AssistantMessage {
			return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse,
				Content: []ai.AssistantContentBlock{
					ai.ToolCall{ID: "echo-one", Name: "echo", Arguments: ai.JsonObject{"text": "one"}},
					ai.ToolCall{ID: "echo-two", Name: "echo", Arguments: ai.JsonObject{"text": "two"}},
				}}
		}, fauxReply("done", ai.StopReasonStop, 0))
	if err := h.session.Prompt(t.Context(), "run tools"); err != nil {
		t.Fatal(err)
	}
	roles := settlementBranchRoles(h.session)
	want := []string{"system", "user", "assistant", "toolResult", "toolResult", "assistant"}
	if !slices.Equal(roles, want) {
		t.Fatalf("persisted branch roles = %v, want %v", roles, want)
	}
	firstToolResult := slices.Index(roles, "toolResult")
	if firstToolResult <= 0 || roles[firstToolResult-1] != "assistant" {
		t.Fatalf("first toolResult at %d must immediately follow its assistant: %v", firstToolResult, roles)
	}
}

// Ports packages/coding-agent/test/suite/regressions/1717-2113-agent-session-event-settlement.test.ts:69.
// tool_call observes the already-settled assistant tool-use message in the persisted branch.
func TestSessionEventSettlementPrecedesToolCallUpstream(t *testing.T) {
	var session *Session
	var branchRolesAtToolCall [][]string
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"tool_call": {func(...any) (any, error) {
			branchRolesAtToolCall = append(branchRolesAtToolCall, settlementBranchRoles(session))
			return nil, nil
		}},
	}}
	h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{&modelExtensionEchoTool{}}, extension: ext},
		modelExtensionToolCall, fauxReply("done", ai.StopReasonStop, 0))
	session = h.session
	if err := session.Prompt(t.Context(), "run tool"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"system", "user", "assistant"}}
	if !reflect.DeepEqual(branchRolesAtToolCall, want) {
		t.Fatalf("branch roles at tool_call = %v, want %v", branchRolesAtToolCall, want)
	}
}

func settlementBranchRoles(session *Session) []string {
	var roles []string
	for _, entry := range session.Inner().GetBranch() {
		if message, ok := entry.(icodingagent.MessageEntry); ok {
			roles = append(roles, message.Message.Role())
		}
	}
	return roles
}

func TestTurnEndIncludesPersistedEntryIDs(t *testing.T) {
	turns := make(chan extension.TurnEndEvent, 2)
	ext := extension.Extension{Path: "turn-ids", Handlers: map[string][]extension.HandlerFn{
		"turn_end": {func(args ...any) (any, error) {
			turns <- args[0].(extension.TurnEndEvent)
			return nil, nil
		}},
	}}
	h := newRecoveryHarness(t, harnessOptions{
		tools:     []agent.AgentTool{&fakeTool{name: "echo"}},
		extension: ext,
	}, fauxToolCall("echo"), fauxReply("done", ai.StopReasonStop, time.Millisecond))
	if _, err := h.session.Send(context.Background(), "run"); err != nil {
		t.Fatal(err)
	}
	h.settle(t)

	first := <-turns
	if first.MessageEntryID == "" {
		t.Fatal("turn_end omitted persisted assistant entry ID")
	}
	if len(first.ToolResultEntryIds) != 1 || first.ToolResultEntryIds[0] == "" {
		t.Fatalf("turn_end tool result entry IDs = %q", first.ToolResultEntryIds)
	}
	second := <-turns
	if second.MessageEntryID == "" || second.MessageEntryID == first.MessageEntryID || len(second.ToolResultEntryIds) != 0 {
		t.Fatalf("second turn retained the first turn's IDs: %+v", second)
	}
	for _, pair := range []struct {
		id      string
		message any
	}{
		{first.MessageEntryID, first.Message},
		{first.ToolResultEntryIds[0], first.ToolResults[0]},
		{second.MessageEntryID, second.Message},
	} {
		entry, ok := h.session.Inner().GetEntry(pair.id)
		if !ok {
			t.Fatalf("turn_end ID %q does not resolve to a persisted entry", pair.id)
		}
		message, ok := entry.(icodingagent.MessageEntry)
		if !ok {
			t.Fatalf("turn_end ID %q resolved to a %s entry", pair.id, entry.Base().Type)
		}
		got, err := json.Marshal(message.Message)
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(pair.message)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("entry %s = %s, want event message %s", pair.id, got, want)
		}
	}
}
