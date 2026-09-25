package agent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// TestNormalizeMessages_OpenAI_Passthrough verifies that a clean conversation
// (no aborted/errored messages, no orphaned tool calls) passes through
// NormalizeMessages unchanged.
func TestNormalizeMessages_OpenAI_Passthrough(t *testing.T) {
	msgs := []AgentMessage{
		{User: &UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "hello"}}}},
		{Assistant: &AssistantMessage{Role: "assistant", StopReason: "stop", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "world"}}}},
	}
	got := NormalizeMessages(msgs, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
}

// TestNormalizeMessages_SkipsAborted verifies that assistant messages with
// StopReason="aborted" are dropped from the normalized output.
func TestNormalizeMessages_SkipsAborted(t *testing.T) {
	msgs := []AgentMessage{
		{User: &UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "hi"}}}},
		{Assistant: &AssistantMessage{Role: "assistant", StopReason: "aborted"}},
		{Assistant: &AssistantMessage{Role: "assistant", StopReason: "stop", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}}},
	}
	got := NormalizeMessages(msgs, nil)
	// Aborted middle message should be dropped.
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (user + final assistant), got %d", len(got))
	}
	if got[1].Assistant == nil || got[1].Assistant.StopReason != "stop" {
		t.Fatalf("expected final assistant with stop reason, got %+v", got[1])
	}
}

// TestNormalizeMessages_SkipsErrored verifies that assistant messages with
// StopReason="error" are dropped.
func TestNormalizeMessages_SkipsErrored(t *testing.T) {
	msgs := []AgentMessage{
		{User: &UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "hi"}}}},
		{Assistant: &AssistantMessage{Role: "assistant", StopReason: "error", ErrorMessage: "network error"}},
	}
	got := NormalizeMessages(msgs, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 message (user only), got %d", len(got))
	}
	if got[0].User == nil {
		t.Fatalf("expected user message, got %+v", got[0])
	}
}

// TestNormalizeMessages_ToolUse_RoundTrip verifies that a complete
// tool_use + tool_result pair passes through without synthetic injection.
func TestNormalizeMessages_ToolUse_RoundTrip(t *testing.T) {
	msgs := []AgentMessage{
		{Assistant: &AssistantMessage{
			Role:       "assistant",
			StopReason: "toolUse",
			Content: []ai.AssistantContentBlock{
				ai.ToolCall{ID: "call_1", Name: "read", Arguments: ai.JsonObject{"path": "/tmp/x"}},
			},
		}},
		{ToolResult: &ToolResultMessage{
			Role: RoleToolResult, ToolCallID: "call_1", ToolName: "read",
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "file contents"}},
		}},
	}
	got := NormalizeMessages(msgs, nil)
	// Exactly the original 2 messages: no synthetic injection needed.
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(got), got)
	}
}

// TestNormalizeMessages_OrphanedToolCall verifies that a tool_use block with
// no matching tool_result gets a synthetic error result injected.
func TestNormalizeMessages_OrphanedToolCall(t *testing.T) {
	msgs := []AgentMessage{
		{Assistant: &AssistantMessage{
			Role:       "assistant",
			StopReason: "aborted", // aborted mid-tool so no result
			Content: []ai.AssistantContentBlock{
				ai.ToolCall{ID: "call_orphan", Name: "bash"},
			},
		}},
	}
	// Aborted assistant is dropped; its orphaned tool call is not flushed
	// because the message was never added to result. Confirm no panic.
	got := NormalizeMessages(msgs, nil)
	// Aborted assistant dropped → 0 messages.
	if len(got) != 0 {
		t.Fatalf("expected 0 messages (aborted dropped), got %d", len(got))
	}
}

// TestNormalizeMessages_OrphanedToolCall_StopTurn verifies synthetic injection
// when a "stop" assistant message has a tool_use but no matching result follows.
func TestNormalizeMessages_OrphanedToolCall_StopTurn(t *testing.T) {
	// This can happen if the session file is truncated mid-execution.
	msgs := []AgentMessage{
		{Assistant: &AssistantMessage{
			Role:       "assistant",
			StopReason: "toolUse",
			Content: []ai.AssistantContentBlock{
				ai.ToolCall{ID: "call_x", Name: "read"},
			},
		}},
		// No tool result follows: simulate truncated session.
		{User: &UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "continue"}}}},
	}
	got := NormalizeMessages(msgs, nil)
	// Should be: assistant + synthetic tool result + user = 3 messages.
	if len(got) != 3 {
		t.Fatalf("expected 3 messages (assistant + synthetic + user), got %d", len(got))
	}
	if got[1].ToolResult == nil {
		t.Fatalf("expected synthetic ToolResult at index 1, got %+v", got[1])
	}
	if got[1].ToolResult.ToolCallID != "call_x" {
		t.Fatalf("unexpected synthetic tool call ID: %q", got[1].ToolResult.ToolCallID)
	}
	if !got[1].ToolResult.IsError {
		t.Fatal("expected synthetic result to be IsError=true")
	}
}

// TestNormalizeMessages_OrphanedToolResult verifies that tool results whose
// tool_call_id has no matching tool_use in any assistant message are stripped.
// This happens after compaction or model switching when the assistant turn
// was dropped but the tool result survived.
func TestNormalizeMessages_OrphanedToolResult(t *testing.T) {
	msgs := []AgentMessage{
		// Compaction summary replaced the original conversation.
		{User: &UserMessage{Role: "user", Content: []ai.UserContentBlock{
			ai.TextContent{Text: "compaction summary"},
		}}},
		// Orphaned tool result from before compaction: no matching tool_use.
		{ToolResult: &ToolResultMessage{
			Role: RoleToolResult, ToolCallID: "call_juCALP1trjCh5UT1mSMlKFwG", ToolName: "bash",
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}},
		}},
		{User: &UserMessage{Role: "user", Content: []ai.UserContentBlock{
			ai.TextContent{Text: "continue"},
		}}},
	}
	got := NormalizeMessages(msgs, nil)
	// The orphaned tool result should be stripped: 2 user messages remain.
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (orphaned tool result stripped), got %d: %+v", len(got), got)
	}
	for _, m := range got {
		if m.ToolResult != nil {
			t.Fatalf("orphaned tool result should have been stripped: %+v", m.ToolResult)
		}
	}
}

// TestNormalizeMessages_ThinkingNormalization locks the cross-model thinking
// rules ported from upstream transform-messages.ts (isSameModel branch). The
// target model is provider "static-fake" / id "claude". Cross-model thinking -
// including an OpenAI reasoning item whose signature is a JSON object, the case
// the removed D31 converter strip used to handle: is converted to plain text
// here, before any provider converter sees a foreign signature.
func TestNormalizeMessages_ThinkingNormalization(t *testing.T) {
	model := &ai.Model{ID: "claude", Provider: &staticProvider{}} // Provider.ID() == "static-fake"
	think := func(text, sig string) ai.ThinkingContent {
		return ai.ThinkingContent{Thinking: text, ThinkingSignature: sig}
	}
	redacted := ai.ThinkingContent{Thinking: "[redacted]", ThinkingSignature: "op", Redacted: true}
	text := func(s string) ai.TextContent { return ai.TextContent{Text: s} }

	tests := []struct {
		name     string
		provider string
		modelID  string
		in       []ai.AssistantContentBlock
		want     []ai.AssistantContentBlock
	}{
		{"cross-model JSON-object signature -> text", "static-fake", "gpt-5",
			[]ai.AssistantContentBlock{think("reasoning", `{"id":"rs_1"}`)}, []ai.AssistantContentBlock{text("reasoning")}},
		{"cross-model opaque signature -> text", "static-fake", "gpt-5",
			[]ai.AssistantContentBlock{think("reasoning", "opaque")}, []ai.AssistantContentBlock{text("reasoning")}},
		{"different provider -> text", "openai", "claude",
			[]ai.AssistantContentBlock{think("reasoning", "sig")}, []ai.AssistantContentBlock{text("reasoning")}},
		{"same-model signed thinking kept", "static-fake", "claude",
			[]ai.AssistantContentBlock{think("reasoning", "sig")}, []ai.AssistantContentBlock{think("reasoning", "sig")}},
		{"same-model empty signed thinking kept", "static-fake", "claude",
			[]ai.AssistantContentBlock{think("", "sig")}, []ai.AssistantContentBlock{think("", "sig")}},
		{"cross-model empty thinking dropped", "static-fake", "gpt-5",
			[]ai.AssistantContentBlock{think("", "sig")}, []ai.AssistantContentBlock{}},
		{"redacted cross-model dropped", "static-fake", "gpt-5",
			[]ai.AssistantContentBlock{redacted}, []ai.AssistantContentBlock{}},
		{"redacted same-model kept", "static-fake", "claude",
			[]ai.AssistantContentBlock{redacted}, []ai.AssistantContentBlock{redacted}},
		{"cross-model thinking dropped, siblings kept", "static-fake", "gpt-5",
			[]ai.AssistantContentBlock{think("", "sig"), text("answer")}, []ai.AssistantContentBlock{text("answer")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := []AgentMessage{{Assistant: &AssistantMessage{
				Role: "assistant", StopReason: "stop",
				Provider: tt.provider, ModelID: tt.modelID,
				Content: append([]ai.AssistantContentBlock(nil), tt.in...),
			}}}
			got := NormalizeMessages(in, model)
			if len(got) != 1 || got[0].Assistant == nil {
				t.Fatalf("want 1 assistant message, got %+v", got)
			}
			gotContent := got[0].Assistant.Content
			if len(gotContent) != len(tt.want) {
				t.Fatalf("content len = %d, want %d: %#v", len(gotContent), len(tt.want), gotContent)
			}
			if len(tt.want) > 0 && !reflect.DeepEqual(gotContent, tt.want) {
				t.Errorf("content = %#v, want %#v", gotContent, tt.want)
			}
			if len(in[0].Assistant.Content) != len(tt.in) {
				t.Errorf("input message content was mutated: got len %d, want %d", len(in[0].Assistant.Content), len(tt.in))
			}
		})
	}
}

// TestNormalizeMessages_NilModelKeepsThinking verifies the contextless/test
// path: with a nil target model, thinking blocks pass through unchanged rather
// than being downgraded to text.
func TestNormalizeMessages_NilModelKeepsThinking(t *testing.T) {
	in := []AgentMessage{{Assistant: &AssistantMessage{
		Role: "assistant", StopReason: "stop", Provider: "x", ModelID: "y",
		Content: []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "reasoning", ThinkingSignature: "sig"}},
	}}}
	got := NormalizeMessages(in, nil)
	tc, ok := got[0].Assistant.Content[0].(ai.ThinkingContent)
	if !ok || tc.Thinking != "reasoning" || tc.ThinkingSignature != "sig" {
		t.Fatalf("nil model should keep thinking unchanged, got %#v", got[0].Assistant.Content)
	}
}

// TestNormalizeMessages_ErroredAssistantOrphansToolResult pins the D48 path
// that upstream does NOT handle: an errored/aborted assistant that held the
// only tool_use for a persisted tool result (an abort race where the tool ran
// and recorded its result before the turn was marked aborted). The errored
// assistant is dropped, orphaning the result; pig strips it so the request
// stays valid, where upstream would send it and the provider would reject the
// whole request ("No tool call found for function call output").
func TestNormalizeMessages_ErroredAssistantOrphansToolResult(t *testing.T) {
	msgs := []AgentMessage{
		{User: &UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "run it"}}}},
		{Assistant: &AssistantMessage{
			Role: "assistant", StopReason: "aborted",
			Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call_race", Name: "bash"}},
		}},
		// The tool result was persisted before the turn was marked aborted.
		{ToolResult: &ToolResultMessage{
			Role: RoleToolResult, ToolCallID: "call_race", ToolName: "bash",
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "output"}},
		}},
		{User: &UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "again"}}}},
	}
	got := NormalizeMessages(msgs, nil)
	// Aborted assistant dropped; its now-orphaned tool result stripped; two
	// user messages remain.
	if len(got) != 2 {
		t.Fatalf("expected 2 user messages (aborted + orphaned result removed), got %d: %+v", len(got), got)
	}
	for _, m := range got {
		if m.ToolResult != nil {
			t.Fatalf("orphaned tool result must be stripped, found %+v", m.ToolResult)
		}
		if m.Assistant != nil {
			t.Fatalf("aborted assistant must be dropped, found %+v", m.Assistant)
		}
	}
}

// TestNormalizeMessages_PartialOrphanSynthesizesMissingResult pins the mixed
// case: one assistant makes two tool calls but only one result is recorded.
// The missing result is synthesized (orphaned-call path), and a separately
// orphaned result in the same message (no surviving tool_use) is stripped
// (D48), while the valid result is preserved.
func TestNormalizeMessages_PartialOrphanSynthesizesMissingResult(t *testing.T) {
	msgs := []AgentMessage{
		{Assistant: &AssistantMessage{
			Role: "assistant", StopReason: "toolUse",
			Content: []ai.AssistantContentBlock{
				ai.ToolCall{ID: "call_a", Name: "read"},
				ai.ToolCall{ID: "call_b", Name: "bash"},
			},
		}},
		// A valid result plus a separate orphaned result.
		{ToolResult: &ToolResultMessage{
			Role: RoleToolResult, ToolCallID: "call_a", ToolName: "read",
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "file"}},
		}},
		{ToolResult: &ToolResultMessage{
			Role: RoleToolResult, ToolCallID: "call_ghost", ToolName: "bash",
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "stale"}},
		}},
		{User: &UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "next"}}}},
	}
	got := NormalizeMessages(msgs, nil)
	// Expect: assistant, the real tool result (call_a only, call_ghost stripped),
	// a synthetic result for the missing call_b, then the user message.
	var haveA, haveGhost, haveSyntheticB bool
	var userCount int
	for _, m := range got {
		if m.User != nil {
			userCount++
		}
		if m.ToolResult == nil {
			continue
		}
		result := m.ToolResult
		switch result.ToolCallID {
		case "call_a":
			haveA = true
		case "call_ghost":
			haveGhost = true
		case "call_b":
			if result.IsError && result.Text() == "No result provided" {
				haveSyntheticB = true
			}
		}
	}
	if !haveA {
		t.Error("valid result call_a must be preserved")
	}
	if haveGhost {
		t.Error("orphaned result call_ghost must be stripped (D48)")
	}
	if !haveSyntheticB {
		t.Error("missing result for call_b must be synthesized as an error result")
	}
	if userCount != 1 {
		t.Errorf("user message count = %d, want 1", userCount)
	}
}
