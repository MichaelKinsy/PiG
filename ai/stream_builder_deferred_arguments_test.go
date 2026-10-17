package ai

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// randomStreamedArguments is a JSON argument text a model might stream: an object (or, rarely, another value) whose strings
// hold escapes, multi-byte runes and surrogate pairs, with nested arrays and objects.
func randomStreamedArguments(rng *rand.Rand, depth int) string {
	value := func() string {
		switch k := rng.IntN(9); {
		case k < 4 || depth > 2:
			return randomStreamedString(rng)
		case k == 4:
			return fmt.Sprint(rng.IntN(100000) - 500)
		case k == 5:
			return []string{"true", "false", "null", "1.5e3", "-0"}[rng.IntN(5)]
		case k == 6:
			var items []string
			for range rng.IntN(4) {
				items = append(items, randomStreamedArguments(rng, depth+1))
			}
			return "[" + strings.Join(items, ",") + "]"
		default:
			return randomStreamedArguments(rng, depth+1)
		}
	}
	if depth == 0 && rng.IntN(20) == 0 {
		return "[" + value() + "]"
	}
	var members []string
	for i := range 1 + rng.IntN(4) {
		key := fmt.Sprintf("%q", []string{"path", "content", "edits", "zeta", "alpha", "__proto__"}[rng.IntN(6)]+fmt.Sprint(i%2))
		members = append(members, key+":"+value())
	}
	return "{" + strings.Join(members, ",") + "}"
}

func randomStreamedString(rng *rand.Rand) string {
	pieces := []string{"x", "line", `\n`, `\"`, `\\`, `\u00e9`, "é", "日本", `\ud83d\ude00`, "😀", " ", `\t`, "/"}
	var text strings.Builder
	text.WriteByte('"')
	for range rng.IntN(40) {
		text.WriteString(pieces[rng.IntN(len(pieces))])
	}
	text.WriteByte('"')
	return text.String()
}

// randomStreamedChunks splits text at random byte offsets, splitting escapes and runes too.
func randomStreamedChunks(rng *rand.Rand, text string) []string {
	var chunks []string
	for len(text) > 0 {
		size := min(1+rng.IntN(8), len(text))
		chunks = append(chunks, text[:size])
		text = text[size:]
	}
	return chunks
}

// eagerToolCall is the call a publication held when the builder parsed every delta at once.
func eagerToolCall(text string) ToolCall {
	call := ToolCall{ID: "c", Name: "t"}
	call.SetStreamingArguments(text)
	return call
}

// eagerFinalToolCall is the call a finished stream holds: its complete text parsed strictly (Pi 87f9a364).
func eagerFinalToolCall(text string) ToolCall {
	call := ToolCall{ID: "c", Name: "t"}
	call.SetFinalArguments(text)
	return call
}

func sameArguments(t *testing.T, label string, got, want ToolCall) {
	t.Helper()
	if got.deferredArguments != "" {
		t.Fatalf("%s: a read call still defers %q", label, got.deferredArguments)
	}
	gotJSON, gotErr := got.ArgumentsJSON()
	wantJSON, wantErr := want.ArgumentsJSON()
	if !bytes.Equal(gotJSON, wantJSON) || (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("%s: arguments %s (%v), want %s (%v)", label, gotJSON, gotErr, wantJSON, wantErr)
	}
	if got.ArgumentsError != want.ArgumentsError {
		t.Fatalf("%s: argumentsError %q, want %q", label, got.ArgumentsError, want.ArgumentsError)
	}
	if !reflect.DeepEqual(got.Arguments, want.Arguments) || !reflect.DeepEqual(got.nonObjectArguments, want.nonObjectArguments) {
		t.Fatalf("%s: arguments %#v / %s, want %#v / %s", label, got.Arguments, got.nonObjectArguments, want.Arguments, want.nonObjectArguments)
	}
}

// A managed builder defers each delta's argument parse to the readers of its publications. Whatever a reader reads, from
// the delta it starts reading at (a reader appearing mid-stream) on, holds the arguments an eager parse of the text so far
// gave; a capture taken at one delta and read later holds that delta's; the final message holds the final parse, with no
// deferred text left in any value that leaves the builder, also when the stream fails inside the call.
func TestDeferredToolArgumentsReadAsTheEagerParse(t *testing.T) {
	rng := rand.New(rand.NewPCG(27, 0x1a2b))
	for trial := range 300 {
		text := randomStreamedArguments(rng, 0)
		chunks := randomStreamedChunks(rng, text)
		observeFrom := rng.IntN(len(chunks) + 1)
		fail := rng.IntN(4) == 0
		label := fmt.Sprintf("trial %d (%q, reader from delta %d)", trial, text, observeFrom)
		builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "p", "m")
		prefix := ""
		type captured struct {
			capture ShallowCapture
			text    string
		}
		var captures []captured
		for index, chunk := range chunks {
			runManagedBuilder(builder, func() {
				builder.toolCallDelta(streamToolCallDelta{index: 0, id: "c", name: "t", argumentsDelta: chunk})
			})
			prefix += chunk
			view := builder.cell.view()
			if index%3 == 0 {
				var capture ShallowCapture
				capture.Capture(view)
				captures = append(captures, captured{capture, prefix})
			}
			if index < observeFrom {
				continue
			}
			observed := view.Observe()
			sameArguments(t, fmt.Sprintf("%s delta %d", label, index), observed.Content[0].(ToolCall), eagerToolCall(prefix))
			projected, _, _ := view.ObserveProjection(nil)
			if !reflect.DeepEqual(projected[0].Arguments, observed.Content[0].(ToolCall).Arguments) {
				t.Fatalf("%s delta %d: projection %#v, observation %#v", label, index, projected[0].Arguments, observed.Content[0].(ToolCall).Arguments)
			}
		}
		for i, captured := range captures {
			for _, message := range []*AssistantMessage{captured.capture.Observe(), captured.capture.ShallowCopy()} {
				sameArguments(t, fmt.Sprintf("%s capture %d", label, i), message.Content[0].(ToolCall), eagerToolCall(captured.text))
			}
		}
		runManagedBuilder(builder, func() {
			if fail {
				// A provider failure pushed inside the call: the message keeps the call's partial arguments, unended.
				builder.push(ErrorEvent{Reason: StopReasonError, Error: builder.partial})
				return
			}
			builder.done(StopReasonToolUse, nil, "")
		})
		final := builder.stream.Result().Content[0].(ToolCall)
		if fail {
			sameArguments(t, label+" final", final, eagerToolCall(text))
		} else {
			sameArguments(t, label+" final", final, eagerFinalToolCall(text))
		}
		if !fail {
			if want := eagerFinalToolCall(text); !reflect.DeepEqual(final, want) {
				t.Fatalf("%s: final call %#v, want %#v", label, final, want)
			}
		}
		sameArguments(t, label+" final view", builder.cell.view().Observe().Content[0].(ToolCall), finalWant(fail, text))
	}
}

func finalWant(fail bool, text string) ToolCall {
	if fail {
		return eagerToolCall(text)
	}
	return eagerFinalToolCall(text)
}

// An OpenAI Completions call that streams function arguments and then switches to custom (grammar) input keeps the custom
// arguments: the function text deferred before the switch is dropped, so neither a reader nor the final message parses it
// over them.
func TestCustomInputSwitchDropsDeferredFunctionArguments(t *testing.T) {
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "p", "m")
	runManagedBuilder(builder, func() {
		builder.toolCallDelta(streamToolCallDelta{index: 0, id: "c", name: "t", argumentsDelta: `{"stale":"function`})
		builder.setCustomToolCallInput(0, "input", "custom text", grammarToolInputJSONBuffer{})
		// The provider publishes the switch with its next delta.
		builder.push(ToolCallDeltaEvent{ContentIndex: 0, Delta: "custom text", Partial: builder.partial})
	})
	want := JsonObject{"input": "custom text"}
	observed := builder.cell.view().Observe().Content[0].(ToolCall)
	if observed.deferredArguments != "" || !reflect.DeepEqual(observed.Arguments, want) {
		t.Fatalf("observed call = %#v, want custom arguments %#v", observed, want)
	}
	runManagedBuilder(builder, func() {
		builder.push(ErrorEvent{Reason: StopReasonError, Error: builder.partial})
	})
	final := builder.stream.Result().Content[0].(ToolCall)
	if final.deferredArguments != "" || !reflect.DeepEqual(final.Arguments, want) {
		t.Fatalf("final call = %#v, want custom arguments %#v", final, want)
	}
}
