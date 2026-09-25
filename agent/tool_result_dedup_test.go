package agent

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A session carrying two tool_result blocks for one tool_use is rejected by the
// provider: "each tool_use must have a single result. Found multiple
// `tool_result` blocks with id: toolu_...". The request fails on every
// subsequent turn, so the session is unusable until edited by hand.
//
// Orphan stripping (D48) does not catch this: both duplicates match a surviving
// tool_use, so both pass. Nothing else in the pipeline counts results per id.

func toolUseTurn(id, name string) AgentMessage {
	return AgentMessage{Assistant: &AssistantMessage{
		Role:    RoleAssistant,
		Content: []ai.AssistantContentBlock{ai.ToolCall{ID: id, Name: name}},
	}}
}

func toolResultFor(id, text string) AgentMessage {
	return AgentMessage{ToolResult: &ToolResultMessage{
		Role:       RoleToolResult,
		ToolCallID: id,
		Content:    []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
	}}
}

func countResultsFor(msgs []AgentMessage, id string) int {
	n := 0
	for _, msg := range msgs {
		if msg.ToolResult != nil && msg.ToolResult.ToolCallID == id {
			n++
		}
	}
	return n
}

func TestDuplicateToolResultsCollapseToOne(t *testing.T) {
	msgs := []AgentMessage{
		{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "go"}}}},
		toolUseTurn("toolu_01VMLTBnpSh575pvALvMaXBx", "read"),
		toolResultFor("toolu_01VMLTBnpSh575pvALvMaXBx", "first"),
		toolResultFor("toolu_01VMLTBnpSh575pvALvMaXBx", "duplicate"),
	}

	got := NormalizeMessages(msgs, nil)

	if n := countResultsFor(got, "toolu_01VMLTBnpSh575pvALvMaXBx"); n != 1 {
		t.Fatalf("tool_use kept %d results, want 1; the provider rejects the request and the session is stuck", n)
	}
	// The surviving result must be the real first one, not the later copy.
	for _, msg := range got {
		if msg.ToolResult != nil && msg.ToolResult.ToolCallID == "toolu_01VMLTBnpSh575pvALvMaXBx" {
			if text, ok := msg.ToolResult.Content[0].(ai.TextContent); ok && text.Text != "first" {
				t.Errorf("kept %q, want the first result", text.Text)
			}
		}
	}
}

// Distinct tool calls must keep their own results. A dedup keyed too broadly
// would silently drop real output.
func TestDistinctToolCallsKeepTheirOwnResults(t *testing.T) {
	msgs := []AgentMessage{
		toolUseTurn("call_a", "read"),
		toolResultFor("call_a", "a"),
		toolUseTurn("call_b", "read"),
		toolResultFor("call_b", "b"),
	}
	got := NormalizeMessages(msgs, nil)
	for _, id := range []string{"call_a", "call_b"} {
		if n := countResultsFor(got, id); n != 1 {
			t.Errorf("%s kept %d results, want 1", id, n)
		}
	}
}

// The synthetic "No result provided" filler is injected for a tool_use whose
// result never arrived. If the real result then appears out of order, the pair
// is still one tool_use with two results.
func TestSyntheticFillerDoesNotDoubleUpWithALateRealResult(t *testing.T) {
	msgs := []AgentMessage{
		toolUseTurn("call_late", "read"),
		{Assistant: &AssistantMessage{Role: RoleAssistant, Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: "moving on"},
		}}},
		toolResultFor("call_late", "arrived late"),
	}
	got := NormalizeMessages(msgs, nil)
	if n := countResultsFor(got, "call_late"); n != 1 {
		t.Fatalf("call_late kept %d results, want 1", n)
	}
	// The real output must survive, in the slot that must directly follow the
	// tool_use. Keeping the placeholder instead would tell the model the call
	// failed when it succeeded, which can make it retry an operation that
	// already ran.
	for _, msg := range got {
		if msg.ToolResult != nil && msg.ToolResult.ToolCallID == "call_late" {
			text, ok := msg.ToolResult.Content[0].(ai.TextContent)
			if !ok || text.Text != "arrived late" {
				t.Errorf("kept %v, want the real tool output", msg.ToolResult.Content[0])
			}
			if msg.ToolResult.IsError {
				t.Error("a successful call was reported to the model as an error")
			}
		}
	}
}

// A tool_use with no result anywhere still gets upstream's placeholder, byte for
// byte. Recovering a late result must not change the genuinely-orphaned case.
func TestTrulyOrphanedToolCallStillGetsTheUpstreamPlaceholder(t *testing.T) {
	msgs := []AgentMessage{
		toolUseTurn("call_missing", "read"),
		{Assistant: &AssistantMessage{Role: RoleAssistant, Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: "moving on"},
		}}},
	}
	got := NormalizeMessages(msgs, nil)
	if n := countResultsFor(got, "call_missing"); n != 1 {
		t.Fatalf("call_missing kept %d results, want 1", n)
	}
	for _, msg := range got {
		if msg.ToolResult != nil && msg.ToolResult.ToolCallID == "call_missing" {
			text, ok := msg.ToolResult.Content[0].(ai.TextContent)
			if !ok || text.Text != "No result provided" {
				t.Errorf("placeholder text = %v, want upstream's exact wording", msg.ToolResult.Content[0])
			}
			if !msg.ToolResult.IsError {
				t.Error("an unresolved call must stay marked as an error")
			}
		}
	}
}

// A call id is not unique across a conversation. A provider that numbers its
// calls per response emits `test-faux-tool-1` on every turn, so an id used once
// per turn legitimately carries one result per turn.
//
// Deduplicating by id alone drops every turn after the first, which silently
// truncates the conversation the model sees: it makes a tool call, the result
// never arrives, and the turn stalls. This is the regression that broke the
// tree parity scenario.
func TestAnIDReusedAcrossTurnsKeepsEveryTurnsResult(t *testing.T) {
	const reused = "test-faux-tool-1"
	var msgs []AgentMessage
	for turn := range 3 {
		msgs = append(msgs, toolUseTurn(reused, "bash"))
		msgs = append(msgs, toolResultFor(reused, fmt.Sprintf("output of turn %d", turn)))
	}

	got := NormalizeMessages(msgs, nil)

	if n := countResultsFor(got, reused); n != 3 {
		t.Fatalf("kept %d results for an id used by 3 separate calls, want 3", n)
	}
	var texts []string
	for _, msg := range got {
		if msg.ToolResult != nil {
			if text, ok := msg.ToolResult.Content[0].(ai.TextContent); ok {
				texts = append(texts, text.Text)
			}
		}
	}
	for turn := range 3 {
		want := fmt.Sprintf("output of turn %d", turn)
		if !slices.Contains(texts, want) {
			t.Errorf("%q is missing; that turn's call has no result", want)
		}
	}
}

// The count is per occurrence, so a genuine duplicate is still dropped even
// when the same id is used again later.
func TestADuplicateIsStillDroppedWhenTheIDIsAlsoReusedLater(t *testing.T) {
	const reused = "test-faux-tool-1"
	msgs := []AgentMessage{
		toolUseTurn(reused, "bash"),
		toolResultFor(reused, "first"),
		toolResultFor(reused, "duplicate of first"),
		toolUseTurn(reused, "bash"),
		toolResultFor(reused, "second turn"),
	}

	got := NormalizeMessages(msgs, nil)

	if n := countResultsFor(got, reused); n != 2 {
		t.Fatalf("kept %d results for 2 calls with one duplicate, want 2", n)
	}
	for _, msg := range got {
		if msg.ToolResult != nil {
			if text, ok := msg.ToolResult.Content[0].(ai.TextContent); ok {
				if text.Text == "duplicate of first" {
					t.Error("the duplicate survived; the provider rejects two results for one call")
				}
			}
		}
	}
}
