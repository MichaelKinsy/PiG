package ai

import (
	"fmt"
	"reflect"
	"testing"
)

// toolBorrowTranscript replays tools added, removed and redefined across several system messages, with a schema nested deeply enough that a deep copy of it shows.
func toolBorrowTranscript() []Message {
	schema := func(depth int) map[string]any {
		leaf := map[string]any{"type": "string", "description": "leaf"}
		for i := range depth {
			leaf = map[string]any{"type": "object", "properties": map[string]any{fmt.Sprintf("p%d", i): leaf, "q": []any{"a", "b", map[string]any{"k": float64(i)}}}}
		}
		return leaf
	}
	return []Message{
		SystemMessage{Content: SystemText("base"), Sections: OrderedSections{{Name: "a", Value: new("1")}}, ToolsAdded: []ToolSchema{
			{Name: "read", Description: "read", Parameters: schema(4), PromptGuidelines: []string{"g1"}},
			{Name: "write", Description: "write", Parameters: schema(3)},
			{Name: "bash", Description: "bash", Parameters: schema(2), ConstrainedSampling: &ConstrainedSamplingConfig{Type: ConstrainedSamplingGrammar, Variants: GrammarVariants{GrammarFormatOpenAILark: "start: x"}}},
		}, Timestamp: 1},
		UserMessage{Content: UserText("hi"), Timestamp: 2},
		SystemMessage{Content: SystemText("more"), ToolsRemoved: []ToolReference{{Name: "write"}}, ToolsAdded: []ToolSchema{{Name: "edit", Description: "edit", Parameters: schema(3)}}, Timestamp: 3},
		AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "ok"}}, StopReason: StopReasonStop, Timestamp: 4},
		SystemMessage{Content: SystemText(""), Sections: OrderedSections{{Name: "a", Value: nil}, {Name: "b", Value: new("2")}}, ToolsAdded: []ToolSchema{{Name: "read", Description: "read v2", Parameters: schema(5)}}, Timestamp: 5},
	}
}

// The request builders read the replayed tools through borrowedCurrentTools. It must list exactly what GetCurrentTools lists, in its order.
func TestBorrowedCurrentToolsEqualGetCurrentTools(t *testing.T) {
	for name, messages := range map[string][]Message{
		"replayed":   toolBorrowTranscript(),
		"no systems": {UserMessage{Content: UserText("hi")}},
		"empty":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			got, want := borrowedCurrentTools(messages), GetCurrentTools(messages)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("borrowedCurrentTools = %#v\nGetCurrentTools = %#v", got, want)
			}
		})
	}
}

// CollapseSystemMessages builds its leading message from the transcript's tools without copying them; the transcript it returns must still be what the copying construction (GetCurrentSystemMessage, then a validated, copied one-message transcript) returned.
func TestCollapseSystemMessagesEqualsTheCopyingConstruction(t *testing.T) {
	legacy := func(context TranscriptContext) TranscriptContext {
		messages := context.messages
		head := GetCurrentSystemMessage(messages)
		out := make([]Message, 0, len(messages))
		if head != nil {
			checked := newTranscriptContext([]Message{*head})
			if checked.err != nil {
				return checked
			}
			out = append(out, checked.messages[0])
		}
		for _, message := range messages {
			if _, system := message.(SystemMessage); !system {
				out = append(out, message)
			}
		}
		return TranscriptContext{messages: out}
	}
	for name, messages := range map[string][]Message{
		"replayed":    toolBorrowTranscript(),
		"user only":   {UserMessage{Content: UserText("hi")}},
		"nil content": {SystemMessage{Timestamp: 3, ToolsAdded: []ToolSchema{{Name: "t", Parameters: map[string]any{"type": "object"}}}}, UserMessage{Content: UserText("x")}},
		"tools only":  {SystemMessage{Content: SystemText(""), ToolsAdded: []ToolSchema{{Name: "t", Parameters: map[string]any{"type": "object"}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			context := TranscriptContext{messages: messages}
			got, want := CollapseSystemMessages(context), legacy(context)
			if (got.err == nil) != (want.err == nil) || (got.err != nil && got.err.Error() != want.err.Error()) {
				t.Fatalf("error = %v, want %v", got.err, want.err)
			}
			if !reflect.DeepEqual(got.messages, want.messages) {
				t.Fatalf("collapsed = %#v\nwant       = %#v", got.messages, want.messages)
			}
		})
	}
}

// The public replays hand out values the caller owns. A caller that edits a returned tool schema must not edit the transcript it was replayed from.
func TestPublicToolReplaysReturnCopies(t *testing.T) {
	messages := toolBorrowTranscript()
	edit := func(tools []ToolSchema) {
		for _, tool := range tools {
			if properties, ok := tool.Parameters["properties"].(map[string]any); ok {
				properties["injected"] = true
			}
			tool.Parameters["injected"] = true
		}
	}
	snapshot := func() []Message { return cloneMessages(messages) }
	before := snapshot()
	edit(GetCurrentTools(messages))
	if current := GetCurrentSystemMessage(messages); current != nil {
		edit(current.ToolsAdded)
	}
	if !reflect.DeepEqual(messages, before) {
		t.Fatal("editing the tools GetCurrentTools or GetCurrentSystemMessage returned edited the transcript")
	}
}

// The request builders read the initial system message through borrowedInitialSystemMessage. A transcript they read has normalized content, and for it the message must equal what GetInitialSystemMessage returns, for a transcript that starts with a system message, with another message, and with none.
func TestBorrowedInitialSystemMessageEqualsGetInitialSystemMessage(t *testing.T) {
	for name, messages := range map[string][]Message{
		"replayed":      toolBorrowTranscript(),
		"user first":    {UserMessage{Content: UserText("hi")}, SystemMessage{Content: SystemText("late")}},
		"empty":         nil,
		"pointer first": {&SystemMessage{Content: SystemText("p"), Sections: OrderedSections{{Name: "a", Value: new("1")}}}},
	} {
		t.Run(name, func(t *testing.T) {
			got, want := borrowedInitialSystemMessage(messages), GetInitialSystemMessage(messages)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("borrowedInitialSystemMessage = %#v\nGetInitialSystemMessage = %#v", got, want)
			}
		})
	}
}

// GetInitialSystemMessage hands out a message the caller owns: editing it must not edit the transcript it was read from.
func TestGetInitialSystemMessageReturnsACopy(t *testing.T) {
	messages := toolBorrowTranscript()
	before := cloneMessages(messages)
	initial := GetInitialSystemMessage(messages)
	for _, tool := range initial.ToolsAdded {
		tool.Parameters["injected"] = true
	}
	initial.Sections[0].Name = "edited"
	if !reflect.DeepEqual(messages, before) {
		t.Fatal("editing the message GetInitialSystemMessage returned edited the transcript")
	}
}
