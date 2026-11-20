package agent

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// eventViewCase is one kind of message, built twice (the message an event carries and the message that replaces it in place) with the text its JSON must show before and after the replacement.
type eventViewCase struct {
	name        string
	original    func() AgentMessage
	replacement func() AgentMessage
}

func eventViewCases(t *testing.T) []eventViewCase {
	t.Helper()
	return []eventViewCase{
		{
			name: "system",
			original: func() AgentMessage {
				return AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText("system before")}}
			},
			replacement: func() AgentMessage {
				return AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText("system after")}}
			},
		},
		{
			name: "user text",
			original: func() AgentMessage {
				return AgentMessage{User: &UserMessage{Role: RoleUser, Content: ai.UserText("user before"), Timestamp: 1}}
			},
			replacement: func() AgentMessage {
				return AgentMessage{User: &UserMessage{Role: RoleUser, Content: ai.UserText("user after"), Timestamp: 2}}
			},
		},
		{
			name: "user blocks",
			original: func() AgentMessage {
				return AgentMessage{User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "blocks before"}}, Timestamp: 1}}
			},
			replacement: func() AgentMessage {
				return AgentMessage{User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "blocks after"}, ai.TextContent{Text: "second"}}, Timestamp: 2}}
			},
		},
		{
			name: "assistant",
			original: func() AgentMessage {
				return AgentMessage{Assistant: &AssistantMessage{
					Role:       RoleAssistant,
					Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "assistant before"}, ai.ThinkingContent{Thinking: "thought before"}},
					Usage:      &ai.Usage{Input: 1, Output: 2, TotalTokens: 3},
					Provider:   "faux",
					ModelID:    "faux-1",
					StopReason: ai.StopReasonStop,
					Timestamp:  1,
				}}
			},
			replacement: func() AgentMessage {
				return AgentMessage{Assistant: &AssistantMessage{
					Role:         RoleAssistant,
					Content:      []ai.AssistantContentBlock{ai.TextContent{Text: "assistant after"}},
					Usage:        &ai.Usage{Input: 10, Output: 20, TotalTokens: 30},
					Provider:     "faux",
					ModelID:      "faux-2",
					StopReason:   ai.StopReasonError,
					ErrorMessage: "failed after",
					Timestamp:    2,
				}}
			},
		},
		{
			name: "toolResult",
			original: func() AgentMessage {
				return AgentMessage{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "call-1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "result before"}}, Timestamp: 1}}
			},
			replacement: func() AgentMessage {
				return AgentMessage{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "call-1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "result after"}}, IsError: true, Timestamp: 2}}
			},
		},
		{
			name: "custom",
			original: func() AgentMessage {
				return decodeCustom(t, `{"role":"custom","customType":"c","content":"custom before","display":true,"timestamp":1}`).WithOrderRecord()
			},
			replacement: func() AgentMessage {
				return decodeCustom(t, `{"timestamp":2,"display":false,"customType":"c","role":"custom","content":"custom after","extra":{"n":1}}`)
			},
		},
	}
}

// The view of a message gives the key the message gives, so a consumer that matches the events and the transcript's message by Identity finds the one the view stands for; a view of a view keeps it too.
func TestEventViewKeepsTheIdentityOfTheMessage(t *testing.T) {
	for _, tc := range eventViewCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			message := tc.original()
			if message.Identity() == nil {
				t.Fatal("the message has no identity")
			}
			view := message.EventView()
			if view.Identity() != message.Identity() {
				t.Errorf("the view's identity %v is not the message's %v", view.Identity(), message.Identity())
			}
			if again := view.EventView(); again.Identity() != message.Identity() {
				t.Errorf("the view of the view has identity %v, want the message's", again.Identity())
			}
			if other := tc.original(); other.EventView().Identity() == view.Identity() {
				t.Error("the views of two messages that merely look alike share an identity")
			}
			original, reachable := view.Original()
			if want := tc.name != "custom"; reachable != want {
				t.Fatalf("Original reachable = %v, want %v", reachable, want)
			}
			if reachable && original.Identity() != message.Identity() {
				t.Errorf("Original has identity %v, want the message's", original.Identity())
			}
		})
	}
	if (AgentMessage{}).EventView().Identity() != nil {
		t.Error("the view of an empty message has an identity")
	}
}

// A replacement of the message in place (every in-place edit of an event's message is one) reaches the message and every holder of its pointers, and does not reach the view.
func TestEventViewIsNotChangedByAReplacementOfTheMessage(t *testing.T) {
	for _, tc := range eventViewCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			message := tc.original()
			view := message.EventView()
			before := customOrderJSON(t, view)
			if want := customOrderJSON(t, tc.original()); before != want {
				t.Fatalf("the view reads\n got %s\nwant %s", before, want)
			}
			ReplaceMessageInPlace(message, tc.replacement())
			if got := customOrderJSON(t, view); got != before {
				t.Errorf("the view after the replacement\n got %s\nwant %s", got, before)
			}
			if got, want := customOrderJSON(t, message), customOrderJSON(t, tc.replacement()); got != want {
				t.Errorf("the replaced message\n got %s\nwant %s", got, want)
			}
		})
	}
}

// What the view shares with the message is what the replacement never writes: the slice a content member held before the replacement keeps its blocks, and the usage it pointed to keeps its counts, so the view that still refers to them reads what it read.
func TestEventViewSharesOnlyValuesAReplacementNeverWrites(t *testing.T) {
	t.Run("assistant", func(t *testing.T) {
		message := AgentMessage{Assistant: &AssistantMessage{
			Role:       RoleAssistant,
			Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "first"}, ai.TextContent{Text: "second"}},
			Usage:      &ai.Usage{Input: 1, Output: 2, TotalTokens: 3},
			StopReason: ai.StopReasonStop,
		}}
		view := message.EventView()
		blocks, usage := message.Assistant.Content, message.Assistant.Usage
		ReplaceMessageInPlace(message, AgentMessage{Assistant: &AssistantMessage{
			Role:       RoleAssistant,
			Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "replaced"}},
			Usage:      &ai.Usage{Input: 9},
			StopReason: ai.StopReasonError,
		}})
		if len(blocks) != 2 || blocks[0] != (ai.TextContent{Text: "first"}) || blocks[1] != (ai.TextContent{Text: "second"}) {
			t.Errorf("the content the message held was written: %v", blocks)
		}
		if *usage != (ai.Usage{Input: 1, Output: 2, TotalTokens: 3}) {
			t.Errorf("the usage the message held was written: %+v", *usage)
		}
		if view.Assistant.StopReason != ai.StopReasonStop || len(view.Assistant.Content) != 2 || view.Assistant.Usage != usage {
			t.Errorf("the view changed: stop %q, %d blocks", view.Assistant.StopReason, len(view.Assistant.Content))
		}
	})
	t.Run("custom", func(t *testing.T) {
		nested := map[string]any{"n": 1}
		message := AgentMessage{Custom: map[string]any{"role": RoleCustom, "customType": "c", "details": nested}}.WithOrderRecord()
		view := message.EventView()
		ReplaceMessageInPlace(message, AgentMessage{Custom: map[string]any{"role": RoleCustom, "customType": "c", "details": map[string]any{"n": 2}}})
		if nested["n"] != 1 || len(nested) != 1 {
			t.Errorf("the value a member held was written: %v", nested)
		}
		if got := view.Custom["details"].(map[string]any); got["n"] != 1 {
			t.Errorf("the view's member changed: %v", got)
		}
	})
}

// Building the view reads the message and writes nothing: the message keeps its pointers, its map and member-order record, and its encoding.
func TestEventViewLeavesTheMessageUnchanged(t *testing.T) {
	for _, tc := range eventViewCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			message := tc.original()
			before := customOrderJSON(t, message)
			system, user, assistant, toolResult, custom, source := message.System, message.User, message.Assistant, message.ToolResult, message.Custom, message.source
			var text string
			if source != nil {
				text = string(source.text)
			}
			view := message.EventView()
			if message.System != system || message.User != user || message.Assistant != assistant || message.ToolResult != toolResult || message.source != source {
				t.Error("building the view replaced a pointer of the message")
			}
			if (custom == nil) != (message.Custom == nil) || (custom != nil && len(custom) != len(message.Custom)) {
				t.Error("building the view replaced the member map of the message")
			}
			if source != nil && string(source.text) != text {
				t.Errorf("building the view changed the member order record %q to %q", text, source.text)
			}
			if got := customOrderJSON(t, message); got != before {
				t.Errorf("the message after building the view\n got %s\nwant %s", got, before)
			}
			if system != nil && view.System == system || user != nil && view.User == user || assistant != nil && view.Assistant == assistant || toolResult != nil && view.ToolResult == toolResult {
				t.Error("the view holds the message's own object")
			}
		})
	}
}

// A consumer that reads the view while the message is replaced in place shares no memory with the replacement: under -race a view that shared the struct, the member map or the member-order record is reported, and a map written while another goroutine reads it is a fatal error.
func TestEventViewIsRaceFreeWithAReplacementOfTheMessage(t *testing.T) {
	for _, tc := range eventViewCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			message := tc.original()
			view := message.EventView()
			replacement := tc.replacement()
			stop := make(chan struct{})
			var wg sync.WaitGroup
			wg.Go(func() {
				for {
					select {
					case <-stop:
						return
					default:
					}
					if _, err := json.Marshal(view); err != nil {
						t.Error(err)
						return
					}
				}
			})
			for range 200 {
				ReplaceMessageInPlace(message, replacement)
			}
			close(stop)
			wg.Wait()
		})
	}
}
