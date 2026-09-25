package ai

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestNormalizeContextRejectsInvalidJSONValues(t *testing.T) {
	tests := []struct {
		name      string
		arguments JsonObject
	}{
		{name: "non-finite", arguments: JsonObject{"value": math.Inf(1)}},
		{name: "unsupported", arguments: JsonObject{"value": make(chan int)}},
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	tests = append(tests, struct {
		name      string
		arguments JsonObject
	}{name: "cycle", arguments: JsonObject(cycle)})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context := NormalizeContext(Context{Messages: []Message{AssistantMessage{Content: []AssistantContentBlock{
				ToolCall{ID: "call", Name: "tool", Arguments: test.arguments},
			}}}})
			if context.err == nil {
				t.Fatal("NormalizeContext accepted invalid JSON")
			}
			if err := validateTranscriptContext(context); err == nil {
				t.Fatal("provider boundary accepted invalid transcript")
			}
		})
	}
}

func TestNormalizeContextDeepCopiesEveryAcceptedJSONContainer(t *testing.T) {
	type namedSlice []string
	type namedMap map[string]namedSlice
	original := namedMap{"values": {"original"}}
	context := NormalizeContext(Context{Messages: []Message{ToolResultMessage{Details: original}}})
	if context.err != nil {
		t.Fatal(context.err)
	}
	original["values"][0] = "changed"
	message := context.Messages()[0].(ToolResultMessage)
	details := message.Details.(map[string]any)
	values := details["values"].([]any)
	if values[0] != "original" {
		t.Fatalf("normalized details changed through source alias: %#v", details)
	}
	values[0] = "mutated read"
	second := context.Messages()[0].(ToolResultMessage).Details.(map[string]any)
	if second["values"].([]any)[0] != "original" {
		t.Fatalf("Messages returned aliased details: %#v", second)
	}
}

func TestNormalizeContextCreatesLeadingSystemMessageOnlyWhenNeeded(t *testing.T) {
	empty := NormalizeContext(Context{})
	if got := empty.Messages(); len(got) != 0 {
		t.Fatalf("empty messages = %#v", got)
	}

	withTools := NormalizeContext(Context{Tools: []ToolSchema{{Name: "read", Description: "Read", Parameters: map[string]any{"type": "object"}}}})
	messages := withTools.Messages()
	system, ok := messages[0].(SystemMessage)
	if len(messages) != 1 || !ok || system.Content != SystemText("") || len(system.ToolsAdded) != 1 {
		t.Fatalf("tool-only normalized messages = %#v", messages)
	}
}

func TestNormalizeContextDoesNotExposeMutableState(t *testing.T) {
	endTurn := true
	ctx := NormalizeContext(Context{Messages: []Message{
		AssistantMessage{
			Content: []AssistantContentBlock{ToolCall{ID: "call", Name: "read", Arguments: JsonObject{"nested": []any{map[string]any{"path": "original"}}}}},
			API:     APIOpenAIResponses, Provider: "test", Model: "model", Usage: Usage{}, StopReason: StopReasonStop,
			Diagnostics: []AssistantMessageDiagnostic{{Details: map[string]any{"nested": map[string]any{"value": "original"}}}},
			Deferred:    &DeferredHandle{Data: map[string]any{"token": "original"}}, EndTurn: &endTurn,
		},
	}})
	first := ctx.Messages()[0].(AssistantMessage)
	first.Content[0].(ToolCall).Arguments["nested"].([]any)[0].(map[string]any)["path"] = "changed"
	first.Diagnostics[0].Details["nested"].(map[string]any)["value"] = "changed"
	first.Deferred.Data.(map[string]any)["token"] = "changed"
	*first.EndTurn = false

	second := ctx.Messages()[0].(AssistantMessage)
	input := second.Content[0].(ToolCall).Arguments["nested"].([]any)[0].(map[string]any)
	if input["path"] != "original" || second.Diagnostics[0].Details["nested"].(map[string]any)["value"] != "original" || second.Deferred.Data.(map[string]any)["token"] != "original" || !*second.EndTurn {
		t.Fatalf("Messages exposed mutable state: %#v", second)
	}
}

func TestMessageJSONContainsOnlyVariantFields(t *testing.T) {
	message := AssistantMessage{Content: []AssistantContentBlock{}, API: APIOpenAIResponses, Provider: "test", Model: "m", Usage: Usage{}, StopReason: StopReasonStop, Timestamp: 1}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"role", "content", "api", "provider", "model", "usage", "stopReason", "timestamp"} {
		if _, ok := fields[required]; !ok {
			t.Errorf("missing %s in %s", required, encoded)
		}
	}
	for _, invalid := range []string{"sections", "toolsAdded", "toolsRemoved", "toolCallId", "toolName", "isError"} {
		if _, ok := fields[invalid]; ok {
			t.Errorf("assistant JSON contains %s: %s", invalid, encoded)
		}
	}
}

func TestOrderedSectionsMarshalInInsertionOrderWithNullRemoval(t *testing.T) {
	sections := OrderedSections{{Name: "tools", Value: new("one")}, {Name: "guidelines", Value: nil}, {Name: "append", Value: new("tail")}}
	got, err := json.Marshal(sections)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"tools":"one","guidelines":null,"append":"tail"}`
	if string(got) != want {
		t.Fatalf("sections JSON = %s, want %s", got, want)
	}
	var decoded OrderedSections
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, sections) {
		t.Fatalf("round trip = %#v, want %#v", decoded, sections)
	}
}

func TestGetCurrentSystemMessageReplaysSectionsAndToolsInOrder(t *testing.T) {
	readV1 := ToolSchema{Name: "read", Description: "v1", Parameters: map[string]any{"type": "object"}}
	readV2 := ToolSchema{Name: "read", Description: "v2", Parameters: map[string]any{"type": "object"}}
	write := ToolSchema{Name: "write", Description: "write", Parameters: map[string]any{"type": "object"}}
	ctx := newTranscriptContext([]Message{
		SystemMessage{Content: SystemText("base"), Sections: OrderedSections{{Name: "a", Value: new("A")}, {Name: "b", Value: new("B")}}, ToolsAdded: []ToolSchema{readV1}, Timestamp: 10},
		UserMessage{Content: UserText("question"), Timestamp: 11},
		SystemMessage{Content: SystemText("later"), Sections: OrderedSections{{Name: "a", Value: nil}, {Name: "c", Value: new("C")}}, ToolsRemoved: []ToolReference{{Name: "read"}}, ToolsAdded: []ToolSchema{readV2, write}, Timestamp: 12},
	})
	got := GetCurrentSystemMessage(ctx.Messages())
	if got == nil || got.Content != SystemText("base\n\nlater") || got.Timestamp != 10 {
		t.Fatalf("system = %#v", got)
	}
	wantSections := OrderedSections{{Name: "b", Value: new("B")}, {Name: "c", Value: new("C")}}
	if !reflect.DeepEqual(got.Sections, wantSections) {
		t.Fatalf("sections = %#v", got.Sections)
	}
	if len(got.ToolsAdded) != 2 || got.ToolsAdded[0].Description != "v2" || got.ToolsAdded[1].Name != "write" {
		t.Fatalf("tools = %#v", got.ToolsAdded)
	}
}

func TestToolDeclarationsIgnorePromptOnlyMetadata(t *testing.T) {
	left := ToolSchema{Name: "read", Description: "read", Parameters: map[string]any{"type": "object"}, PromptGuidelines: []string{"first"}}
	right := left
	right.PromptGuidelines = []string{"second"}
	if !DeclarationsEqual(left, right) {
		t.Fatal("prompt-only guidelines changed declaration")
	}
}

func TestRenderSystemMessageUpdateFramesSectionChanges(t *testing.T) {
	message := SystemMessage{Content: SystemTextBlocks{{Text: "additional"}}, Sections: OrderedSections{{Name: "tools", Value: new("<tools>new</tools>")}, {Name: "old", Value: nil}}}
	want := "additional\n\nUpdated system prompt section \"tools\":\n\n<tools>new</tools>\n\nRemoved system prompt section \"old\"."
	if got := RenderSystemMessageUpdate(message); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestToolStateChangesTreatRedefinitionAsRemovalThenAddition(t *testing.T) {
	previous := []ToolSchema{{Name: "read", Description: "old", Parameters: map[string]any{"type": "object"}}}
	current := []ToolSchema{{Name: "read", Description: "new", Parameters: map[string]any{"type": "object"}}, {Name: "write", Description: "write", Parameters: map[string]any{"type": "object"}}}
	changes := GetToolStateChanges(previous, current)
	if len(changes.ToolsAdded) != 2 || !reflect.DeepEqual(changes.ToolsRemoved, []ToolReference{{Name: "read"}}) {
		t.Fatalf("changes = %#v", changes)
	}
}

func TestResolveTranscriptCollapsesUnsupportedMidConversationSystemMessages(t *testing.T) {
	ctx := newTranscriptContext([]Message{SystemMessage{Content: SystemText("base"), Timestamp: 1}, UserMessage{Content: UserText("question"), Timestamp: 2}, SystemMessage{Content: SystemText("later"), Timestamp: 3}, AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "answer"}}, StopReason: StopReasonStop, Timestamp: 4}})
	preserved := ResolveTranscript(ctx, true).Messages()
	if len(preserved) != 4 {
		t.Fatalf("preserved = %#v", preserved)
	}
	collapsed := ResolveTranscript(ctx, false).Messages()
	head, ok := collapsed[0].(SystemMessage)
	if len(collapsed) != 3 || !ok || head.Content != SystemText("base\n\nlater") {
		t.Fatalf("collapsed = %#v", collapsed)
	}
}
