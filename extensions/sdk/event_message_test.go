package sdk

import "testing"

// A message-shaped event carries the upstream flat union:
//
//	{"type":"message_end","message":{"role":"assistant","content":[{"type":"text",...}]}}
//
// Two independent readings of this payload got it wrong in the same week: one
// extension read content as a string, and a review of that extension proposed
// keying on "Assistant", a Go field name that never reaches the wire because
// agent.AgentMessage marshals to the role-discriminated union. The table
// below is built from the shape the host actually emits, verified by
// marshalling extension.MessageEndEvent.
func TestMessageTextReadsTheWireShape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		data     map[string]any
		wantRole string
		wantText string
	}{
		{
			name: "assistant text blocks are concatenated",
			data: map[string]any{
				"type": "message_end",
				"message": map[string]any{
					"role": "assistant",
					"content": []any{
						map[string]any{"type": "text", "text": "sure, "},
						map[string]any{"type": "text", "text": "do a barrel roll"},
					},
				},
			},
			wantRole: "assistant",
			wantText: "sure, do a barrel roll",
		},
		{
			name: "non-text blocks are skipped, not stringified",
			data: map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"content": []any{
						map[string]any{"type": "thinking", "thinking": "hmm"},
						map[string]any{"type": "text", "text": "answer"},
						map[string]any{"type": "tool_use", "id": "t1", "name": "bash"},
					},
				},
			},
			wantRole: "assistant",
			wantText: "answer",
		},
		{
			name: "user messages read the same way",
			data: map[string]any{
				"message": map[string]any{
					"role":    "user",
					"content": []any{map[string]any{"type": "text", "text": "do a barrel roll"}},
				},
			},
			wantRole: "user",
			wantText: "do a barrel roll",
		},
		{
			name:     "event without a message yields empty, not a panic",
			data:     map[string]any{"type": "turn_end"},
			wantRole: "",
			wantText: "",
		},
		{
			name: "empty content yields empty text",
			data: map[string]any{
				"message": map[string]any{"role": "assistant", "content": []any{}},
			},
			wantRole: "assistant",
			wantText: "",
		},
		{
			name: "a bare string content is tolerated",
			data: map[string]any{
				"message": map[string]any{"role": "assistant", "content": "plain"},
			},
			wantRole: "assistant",
			wantText: "plain",
		},
		{
			name: "a Go-side field name is not the wire shape and finds nothing",
			data: map[string]any{
				"message": map[string]any{
					"Assistant": map[string]any{"role": "assistant", "content": []any{
						map[string]any{"type": "text", "text": "never reaches the wire"},
					}},
				},
			},
			wantRole: "",
			wantText: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MessageRole(tc.data); got != tc.wantRole {
				t.Errorf("MessageRole = %q, want %q", got, tc.wantRole)
			}
			if got := MessageText(tc.data); got != tc.wantText {
				t.Errorf("MessageText = %q, want %q", got, tc.wantText)
			}
		})
	}
}
