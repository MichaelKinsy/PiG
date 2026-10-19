package ai

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// unmanagedPartialSteps grows a partial message the way a producer outside the executor does between pushes: it replaces the block that grew (text, thinking or a tool call whose arguments it parsed or assigned) and sometimes opens another block or changes a field of the message. Each step pushes the event of the block it changed.
type unmanagedPartialSteps struct {
	rng     *rand.Rand
	partial *AssistantMessage
	texts   []string
}

func (steps *unmanagedPartialSteps) open() {
	kind := steps.rng.IntN(3)
	index := len(steps.partial.Content)
	switch kind {
	case 0:
		steps.partial.Content = append(steps.partial.Content, TextContent{})
	case 1:
		steps.partial.Content = append(steps.partial.Content, ThinkingContent{})
	default:
		steps.partial.Content = append(steps.partial.Content, ToolCall{ID: fmt.Sprintf("call_%d", index), Name: "tool", Arguments: JsonObject{}})
	}
	steps.texts = append(steps.texts, "")
}

// argumentPieces are what the tool calls of the steps stream, including a split rune and bytes that are not UTF-8: a snapshot reads the strings it keeps for validity only once, and must still treat each text as the full copy does.
var argumentPieces = []string{"x", "line ", `\n`, `\"`, "é", "日本", "😀", "\xff", "\xc3", "\xa9", `\u00e9`, "word "}

// next grows one block and returns the event that announces it.
func (steps *unmanagedPartialSteps) next() AssistantMessageEvent {
	if len(steps.partial.Content) == 0 || steps.rng.IntN(6) == 0 {
		steps.open()
	}
	index := steps.rng.IntN(len(steps.partial.Content))
	piece := argumentPieces[steps.rng.IntN(len(argumentPieces))]
	if steps.rng.IntN(8) == 0 {
		steps.partial.Usage.Output += 3
	}
	switch block := steps.partial.Content[index].(type) {
	case TextContent:
		block.Text += piece
		steps.partial.Content[index] = block
		return TextDeltaEvent{ContentIndex: index, Delta: piece, Partial: steps.partial}
	case ThinkingContent:
		block.Thinking += piece
		steps.partial.Content[index] = block
		return ThinkingDeltaEvent{ContentIndex: index, Delta: piece, Partial: steps.partial}
	default:
		call := block.(ToolCall)
		steps.texts[index] += piece
		switch steps.rng.IntN(4) {
		case 0:
			// A producer that holds the argument values itself assigns them, nested values and native numbers included.
			call.Arguments = JsonObject{"v": steps.texts[index], "n": int64(index), "f": float32(1.5), "nested": map[string]any{"list": []any{steps.texts[index], 1.0, nil}}, "tags": []string{"a", steps.texts[index]}}
		case 1:
			call.Arguments = JsonObject{"v": steps.texts[index]}
		default:
			call.SetStreamingArguments(`{"v":"` + steps.texts[index] + `"`)
		}
		steps.partial.Content[index] = call
		return ToolCallDeltaEvent{ContentIndex: index, Delta: piece, Partial: steps.partial}
	}
}

// A message that streams from outside the executor reaches its consumer as the copy it would have been (cloneAssistantMessage) at each push, whatever an earlier push left, and each copy belongs to the consumer that holds it: no copy shares a map or slice with another or with the producer's message.
func TestUnmanagedProducerSnapshotsEqualTheirFullCopy(t *testing.T) {
	for trial := range 200 {
		rng := rand.New(rand.NewPCG(11, uint64(trial)))
		stream := NewAssistantMessageEventStream()
		steps := &unmanagedPartialSteps{rng: rng, partial: &AssistantMessage{API: APIOpenAICompletions, Provider: "p", Model: "m", StopReason: StopReasonPending, Content: []AssistantContentBlock{}}}
		var want []AssistantMessage
		for range 1 + rng.IntN(40) {
			event := steps.next()
			want = append(want, cloneAssistantMessage(*steps.partial))
			if err := stream.Push(event); err != nil {
				t.Fatal(err)
			}
		}
		steps.partial.StopReason = StopReasonToolUse
		if err := stream.Push(DoneEvent{Reason: StopReasonToolUse, Message: steps.partial}); err != nil {
			t.Fatal(err)
		}
		i := 0
		for event := range stream.Events(t.Context()) {
			partial := eventPartial(event)
			if partial == nil {
				// The final message is the producer's own, as Pi's is.
				if done, ok := event.(DoneEvent); !ok || done.Message != steps.partial {
					t.Fatalf("trial %d: event %d is %#v, want the done event of the producer's message", trial, i, event)
				}
				continue
			}
			if i >= len(want) {
				t.Fatalf("trial %d: event %d beyond the %d pushed", trial, i, len(want))
			}
			if got := *partial; !reflect.DeepEqual(got, want[i]) {
				gotJSON, _ := json.Marshal(got)
				wantJSON, _ := json.Marshal(want[i])
				t.Fatalf("trial %d event %d (%T): partial\n%s\nwant\n%s", trial, i, event, gotJSON, wantJSON)
			}
			// The consumer owns what it was given: rewriting it must not reach the snapshots after it.
			for _, block := range partial.Content {
				if call, ok := block.(ToolCall); ok && call.Arguments != nil {
					call.Arguments["v"] = "rewritten by the consumer"
					call.Arguments["added"] = []any{"x"}
					if nested, ok := call.Arguments["nested"].(map[string]any); ok {
						nested["list"] = nil
					}
					delete(call.Arguments, "tags")
				}
			}
			i++
		}
		if i != len(want) {
			t.Fatalf("trial %d: %d events delivered, want %d", trial, i, len(want))
		}
	}
}

// What follows a long valid argument string is checked on its own, and the whole string is still judged as a full copy judges it: a rune the text ends in the middle of and completes later, bytes that are not UTF-8, and the three-byte forms of lone surrogates (which a clone keeps, unless a pair of them is adjacent) change nothing about the snapshot of the text before or after them.
func TestUnmanagedProducerSnapshotsJudgeWhatFollowsALongValidString(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	partial := &AssistantMessage{API: APIOpenAICompletions, Provider: "p", Model: "m", StopReason: StopReasonPending}
	text := strings.Repeat("long valid text é日本😀 ", 40)
	pieces := []string{"", "more", "\xc3", "\xa9", "tail", "\xff", "after", "\xed\xa0\x80", "\xed\xb0\x80", "end", "\xe6\x97", "\xa5"}
	var want []AssistantMessage
	for _, piece := range pieces {
		text += piece
		partial.Content = []AssistantContentBlock{ToolCall{ID: "c", Name: "write", Arguments: JsonObject{"content": text, "other": []any{map[string]any{"deep": text}}}}}
		want = append(want, cloneAssistantMessage(*partial))
		if err := stream.Push(ToolCallDeltaEvent{ContentIndex: 0, Delta: piece, Partial: partial}); err != nil {
			t.Fatal(err)
		}
	}
	stream.End()
	i := 0
	for event := range stream.Events(t.Context()) {
		if got := *eventPartial(event); !reflect.DeepEqual(got, want[i]) {
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want[i])
			t.Fatalf("push %d (%q appended): partial\n%s\nwant\n%s", i, pieces[i], gotJSON, wantJSON)
		}
		i++
	}
	if i != len(want) {
		t.Fatalf("%d events delivered, want %d", i, len(want))
	}
}
