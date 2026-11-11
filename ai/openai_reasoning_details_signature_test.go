package ai

import (
	"fmt"
	"strings"
	"testing"
)

func reasoningDetailsChunk(detailsJSON string) string {
	return `data: {"choices":[{"delta":{"reasoning_details":[` + detailsJSON + `]},"finish_reason":null}]}` + "\n\n"
}

func reasoningDetailsSSE(chunks ...string) string {
	return strings.Join(chunks, "") +
		`data: {"choices":[{"delta":{"content":"a"},"finish_reason":"stop"}]}` + "\n\n" + "data: [DONE]\n"
}

// The thinking signature is serialized once, when the thinking block ends, as in Pi (7aab6c26). These
// bytes are what the per-delta merge and marshal produced before: field order of first appearance,
// merged text re-encoded without HTML escaping, fields filled in by later deltas appended last.
func TestParseSSE_ReasoningDetailsSignatureBytes(t *testing.T) {
	tests := []struct {
		name string
		sse  string
		want string
	}{
		{
			name: "discrete",
			sse: reasoningDetailsSSE(
				reasoningDetailsChunk(`{"type":"reasoning.encrypted","id":"e1","data":"c1"}`),
				reasoningDetailsChunk(`{"type":"reasoning.encrypted","id":"e2","data":"c2","format":"v1"},{"type":"reasoning.text","text":"t"}`),
				reasoningDetailsChunk(`{"type":"reasoning.encrypted","id":"e3","data":"c3","index":2}`),
			),
			want: `[{"type":"reasoning.encrypted","id":"e1","data":"c1"},{"type":"reasoning.encrypted","id":"e2","data":"c2","format":"v1"},{"type":"reasoning.text","text":"t"},{"type":"reasoning.encrypted","id":"e3","data":"c3","index":2}]`,
		},
		{
			name: "merged text and summary",
			sse: reasoningDetailsSSE(
				reasoningDetailsChunk(`{"type":"reasoning.text","text":"a <b> \u00e9","index":0}`),
				reasoningDetailsChunk(`{"type":"reasoning.text","text":" & c\u2028","signature":"sig","format":"v1","id":"r1"}`),
				reasoningDetailsChunk(`{"type":"reasoning.text","text":"!","signature":"later","id":"r2","index":9}`),
				reasoningDetailsChunk(`{"type":"reasoning.summary","summary":"S","index":1}`),
				reasoningDetailsChunk(`{"type":"reasoning.summary","summary":"um","format":"v2"}`),
			),
			want: `[{"type":"reasoning.text","text":"a <b> é & c\u2028!","index":0,"signature":"sig","id":"r1","format":"v1"},{"type":"reasoning.summary","summary":"Sum","index":1,"format":"v2"}]`,
		},
		{
			name: "mixed",
			sse: reasoningDetailsSSE(
				reasoningDetailsChunk(`{"type":"reasoning.text","text":"one"}`),
				reasoningDetailsChunk(`{"type":"reasoning.text","text":" two"},{"type":"reasoning.encrypted","id":"e","data":"d"}`),
				reasoningDetailsChunk(`{"type":"reasoning.text","text":"three"},{"type":"reasoning.text","text":" four"}`),
				reasoningDetailsChunk(`{"type":"reasoning.summary","summary":"s"}`),
			),
			want: `[{"type":"reasoning.text","text":"one two"},{"type":"reasoning.encrypted","id":"e","data":"d"},{"type":"reasoning.text","text":"three four"},{"type":"reasoning.summary","summary":"s"}]`,
		},
		{
			name: "single entry keeps its raw text",
			sse:  reasoningDetailsSSE(reasoningDetailsChunk(`{"type":"reasoning.text","text":"a <b>"}`)),
			want: `[{"type":"reasoning.text","text":"a <b>"}]`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := runOpenAICompletionsSSE(t, test.sse)
			thinking, ok := message.Content[0].(ThinkingContent)
			if !ok {
				t.Fatalf("content[0] = %T, want ThinkingContent", message.Content[0])
			}
			if thinking.ThinkingSignature != test.want {
				t.Fatalf("signature =\n%s\nwant\n%s", thinking.ThinkingSignature, test.want)
			}
		})
	}
}

// Pi keeps reasoning_details in memory while streaming and writes the signature at thinking_end, and on
// an error to every thinking block.
func TestParseSSE_ReasoningDetailsSignatureSetAtThinkingEndAndOnError(t *testing.T) {
	run := func(sse string) []AssistantMessageEvent {
		provider := &openAIProvider{cfg: OpenAIConfig{ProviderID: "openrouter"}}
		builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "openrouter", "model")
		provider.parseSSE(t.Context(), strings.NewReader(sse), builder, nil)
		var events []AssistantMessageEvent
		for event := range builder.stream.Events(t.Context()) {
			events = append(events, event)
		}
		return events
	}
	signatureOf := func(message *AssistantMessage) string {
		for _, content := range message.Content {
			if thinking, ok := content.(ThinkingContent); ok {
				return thinking.ThinkingSignature
			}
		}
		return ""
	}
	want := `[{"type":"reasoning.text","text":"ab"}]`

	var sawEnd bool
	for _, event := range run(reasoningDetailsSSE(
		reasoningDetailsChunk(`{"type":"reasoning.text","text":"a"}`),
		reasoningDetailsChunk(`{"type":"reasoning.text","text":"b"}`),
	)) {
		if end, ok := event.(ThinkingEndEvent); ok {
			sawEnd = true
			if got := signatureOf(end.Partial); got != want {
				t.Fatalf("signature at thinking_end = %q, want %q", got, want)
			}
		}
	}
	if !sawEnd {
		t.Fatal("no thinking_end event")
	}

	events := run(reasoningDetailsChunk(`{"type":"reasoning.text","text":"a"}`) + reasoningDetailsChunk(`{"type":"reasoning.text","text":"b"}`) + "data: {not json\n\n")
	last, ok := events[len(events)-1].(ErrorEvent)
	if !ok {
		t.Fatalf("last event = %T, want ErrorEvent", events[len(events)-1])
	}
	if got := signatureOf(last.Error); got != want {
		t.Fatalf("signature on error = %q, want %q", got, want)
	}
}

func openAIReasoningDetailsBody(n int, kind string) string {
	var chunks []string
	for i := range n {
		switch kind {
		case "encrypted":
			chunks = append(chunks, reasoningDetailsChunk(fmt.Sprintf(`{"type":"reasoning.encrypted","id":"e%d","data":"cipher%d"}`, i, i)))
		default:
			chunks = append(chunks, reasoningDetailsChunk(`{"type":"reasoning.text","text":"some thinking token "}`))
		}
	}
	return reasoningDetailsSSE(chunks...)
}
