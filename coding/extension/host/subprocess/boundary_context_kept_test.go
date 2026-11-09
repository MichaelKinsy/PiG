package subprocess

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/boundarywire"
)

// keptContextMessages is a conversation of n turns: a user prompt, an assistant message and a tool result that holds HTML characters, escapes and a long text.
func keptContextMessages(n int) []extension.AgentMessage {
	var messages []extension.AgentMessage
	for i := range n {
		messages = append(messages,
			extension.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(fmt.Sprintf("prompt %d <&>", i)), Timestamp: int64(i)}},
			extension.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d \u2028", i)}}, StopReason: ai.StopReasonStop}},
			extension.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: fmt.Sprintf("call-%d", i), ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("func f() { return a < b && c > d }\n\t\"quoted\"\n", 200)}}}},
		)
	}
	return messages
}

// A context event whose dispatch keeps the session's encodings is the encoding the event always had, on the first request, on a later one that holds the same messages and more, and when messages came and went.
func TestKeptContextWireIsTheContextWire(t *testing.T) {
	var encodings boundarywire.Encodings
	messages := keptContextMessages(6)
	custom := extension.AgentMessage{Custom: map[string]any{"role": "custom", "customType": "note", "content": "first", "display": true, "timestamp": float64(1)}}
	lists := [][]extension.AgentMessage{
		messages[:6],
		messages[:9],
		append(slicesCloneMessages(messages[:9]), custom),
		messages[3:12],
		messages[:0],
		nil,
		messages,
	}
	for round, list := range lists {
		for _, eventType := range []string{"context", "context_with_system"} {
			want, err := marshalContextWire(eventType, list)
			if err != nil {
				t.Fatal(err)
			}
			got, err := marshalKeptContextWire(&encodings, eventType, list)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("round %d %s: kept encoding differs from the context wire\n got %.200s\nwant %.200s", round, eventType, got, want)
			}
		}
	}
}

func slicesCloneMessages(messages []extension.AgentMessage) []extension.AgentMessage {
	return append([]extension.AgentMessage(nil), messages...)
}

// A custom message has no key: a change to its members between two requests reaches the second encoding.
func TestKeptContextWireEncodesACustomMessageEachTime(t *testing.T) {
	var encodings boundarywire.Encodings
	custom := extension.AgentMessage{Custom: map[string]any{"role": "custom", "customType": "note", "content": "first", "display": true, "timestamp": float64(1)}}
	list := []extension.AgentMessage{custom}
	for _, content := range []string{"first", "second"} {
		custom.Custom["content"] = content
		got, err := marshalKeptContextWire(&encodings, "context", list)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(got, []byte(`"content":"`+content+`"`)) {
			t.Fatalf("encoding %s does not carry content %q", got, content)
		}
	}
}

// The second request of a conversation encodes only the messages that are new: its allocations are a fraction of the first request's.
func TestKeptContextWireEncodesOnlyNewMessages(t *testing.T) {
	var encodings boundarywire.Encodings
	messages := keptContextMessages(10)
	if _, err := marshalKeptContextWire(&encodings, "context", messages[:27]); err != nil {
		t.Fatal(err)
	}
	cold := testing.AllocsPerRun(1, func() {
		var fresh boundarywire.Encodings
		if _, err := marshalKeptContextWire(&fresh, "context", messages[:27]); err != nil {
			t.Fatal(err)
		}
	})
	warm := testing.AllocsPerRun(5, func() {
		if _, err := marshalKeptContextWire(&encodings, "context", messages[:27]); err != nil {
			t.Fatal(err)
		}
	})
	if warm > cold/10 {
		t.Fatalf("a request of the same 27 messages allocates %.0f, a first request %.0f: the kept encodings are not used", warm, cold)
	}
}

// marshalEventWireIn keeps encodings only for a dispatch that carries them, and only for the two conversation events.
func TestMarshalEventWireInKeepsOnlyForAContextDispatch(t *testing.T) {
	messages := keptContextMessages(3)
	var encodings boundarywire.Encodings
	kept := boundarywire.WithContextEncodings(context.Background(), &encodings)
	for _, event := range []any{
		extension.ContextEvent{Type: "context", Messages: messages},
		extension.ContextWithSystemEvent{Type: "context_with_system", Messages: messages},
		extension.ContextEvent{Type: "context"},
		extension.ModelSelectEvent{Type: "model_select"},
	} {
		want, err := marshalEventWire(event)
		if err != nil {
			t.Fatal(err)
		}
		for name, ctx := range map[string]context.Context{"plain": context.Background(), "kept": kept} {
			got, err := marshalEventWireIn(ctx, event)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%T under a %s context: %.120s, want %.120s", event, name, got, want)
			}
		}
	}
}

// BenchmarkContextEventEncoding is the encoding of one context event of a conversation of 150 messages (50 tool turns of 10 KB results), as a subscriber's request pays it: every message encoded each time, and with the encodings of the request before kept (the messages are the same objects, so only the join is paid; a new turn adds the encoding of its three messages).
func BenchmarkContextEventEncoding(b *testing.B) {
	messages := keptContextMessages(50)
	b.Run("whole", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := marshalContextWire("context", messages); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("kept", func(b *testing.B) {
		var encodings boundarywire.Encodings
		if _, err := marshalKeptContextWire(&encodings, "context", messages); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			if _, err := marshalKeptContextWire(&encodings, "context", messages); err != nil {
				b.Fatal(err)
			}
		}
	})
}
