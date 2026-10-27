package ai

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// longStreamShape is a long assistant turn: textDeltas text deltas, thinkingDeltas thinking deltas, and toolCalls tool calls whose arguments stream as argDeltas fragments each.
type longStreamShape struct {
	textDeltas, thinkingDeltas, toolCalls, argDeltas, argLines int
}

// The benchmark shape is 50k tokens of text and 20 tool calls (docs/project/test-economy.md: allocs/op and ns/op on a long stream).
var benchLongStream = longStreamShape{textDeltas: 50_000, thinkingDeltas: 2_000, toolCalls: 20, argDeltas: 400, argLines: 400}

// toolArgumentFragments splits one tool call's arguments JSON, with the given number of content lines, into fragments.
func toolArgumentFragments(call, fragments, lines int) []string {
	var whole strings.Builder
	whole.WriteString(`{"path":"src/file` + strconv.Itoa(call) + `.go","content":"`)
	for i := range lines {
		whole.WriteString("line " + strconv.Itoa(i) + `\n`)
	}
	whole.WriteString(`","mode":"w"}`)
	text := whole.String()
	out := make([]string, 0, fragments)
	step := max(1, len(text)/fragments)
	for len(text) > step && len(out) < fragments-1 {
		out = append(out, text[:step])
		text = text[step:]
	}
	return append(out, text)
}

// drive pushes the shape's events through a managed builder and returns the builder after done.
func (shape longStreamShape) drive(ctx context.Context, afterPush func(*assistantStreamBuilder)) *assistantStreamBuilder {
	builder := newAssistantStreamBuilder(ctx, APIOpenAIResponses, "test", "model")
	if afterPush != nil {
		builder.afterPush = func() { afterPush(builder) }
	}
	builder.partial.Timestamp = 1
	runManagedBuilder(builder, func() {
		builder.start()
		for i := range shape.thinkingDeltas {
			builder.thinkingDelta("think "+strconv.Itoa(i)+" ", false)
		}
		for i := range shape.textDeltas {
			builder.textDelta("tok" + strconv.Itoa(i%97) + " ")
		}
		for call := range shape.toolCalls {
			for n, fragment := range toolArgumentFragments(call, shape.argDeltas, shape.argLines) {
				delta := streamToolCallDelta{index: call, argumentsDelta: fragment}
				if n == 0 {
					delta.id, delta.name = "call_"+strconv.Itoa(call), "write"
				}
				builder.toolCallDelta(delta)
			}
			builder.endToolCall(call)
		}
		builder.done(StopReasonToolUse, &Usage{Input: 1, Output: 2, TotalTokens: 3}, "")
	})
	return builder
}

// digest folds every event of the stream, its type, delta and the partial it carries, into one hash.
func streamDigest(t testing.TB, builder *assistantStreamBuilder) string {
	t.Helper()
	hash := sha256.New()
	count := 0
	for event := range builder.stream.Events(t.Context()) {
		count++
		partial := eventPartial(event)
		if start, ok := event.(StartEvent); ok {
			partial = start.Partial
		}
		_, _ = fmt.Fprintf(hash, "%s|", event.EventType())
		switch event := event.(type) {
		case TextDeltaEvent:
			_, _ = fmt.Fprintf(hash, "%d|%s|", event.ContentIndex, event.Delta)
		case ThinkingDeltaEvent:
			_, _ = fmt.Fprintf(hash, "%d|%s|", event.ContentIndex, event.Delta)
		case ToolCallDeltaEvent:
			_, _ = fmt.Fprintf(hash, "%d|%s|", event.ContentIndex, event.Delta)
		case ToolCallEndEvent:
			encoded, err := json.Marshal(event.ToolCall)
			if err != nil {
				t.Fatal(err)
			}
			hash.Write(encoded)
		}
		// Hash only the final shape of every retained partial: delivered partials observe the state at delivery, which for a finished producer is the final message.
		if partial != nil {
			encoded, err := json.Marshal(partial)
			if err != nil {
				t.Fatal(err)
			}
			hash.Write(encoded)
		}
	}
	return fmt.Sprintf("%d:%x", count, hash.Sum(nil))
}

// A long stream's event sequence is byte-identical across the allocation diet: the golden digest below was recorded before the change.
func TestLongStreamOutputUnchanged(t *testing.T) {
	shape := longStreamShape{textDeltas: 600, thinkingDeltas: 40, toolCalls: 4, argDeltas: 24, argLines: 30}
	// pushed folds the cell's state at every push: delivered partials observe the finished producer, so only this hook sees the intermediate states.
	pushed := sha256.New()
	builder := shape.drive(t.Context(), func(builder *assistantStreamBuilder) {
		encoded, err := json.Marshal(builder.cell.view().Observe())
		if err != nil {
			t.Fatal(err)
		}
		pushed.Write(encoded)
		encoded, err = json.Marshal(builder.partial)
		if err != nil {
			t.Fatal(err)
		}
		pushed.Write(encoded)
	})
	got := fmt.Sprintf("%x|%s", pushed.Sum(nil), streamDigest(t, builder))
	const want = "e470afff1b6925d9c4b7ce85caf6775df9e1d15f9130c61e30df6b58ce22e353|750:48229871c9f331e9ae2cc96c45aa8b0a9c24a81cf6b0c3bc39edb2b61ab0da81"
	if got != want {
		t.Fatalf("stream digest = %s, want %s", got, want)
	}
	result := builder.stream.Result()
	if text := result.Content[1].(TextContent).Text; !strings.HasPrefix(text, "tok0 tok1 ") || len(text) == 0 {
		t.Fatalf("text = %q", text[:min(20, len(text))])
	}
}

func BenchmarkAssistantStreamBuilderLongStream(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		builder := benchLongStream.drive(b.Context(), nil)
		builder.stream.Result()
	}
}
