package ai

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestStreamingJSONRepairControlCharacters(t *testing.T) {
	// Pi utils/json-parse.ts:10-24 uses JSON escapes for every U+0000..U+001F byte.
	for control := range rune(0x20) {
		t.Run(fmt.Sprintf("U+%04X", control), func(t *testing.T) {
			text := "before" + string(control) + "after"
			raw := `{"text":"` + text + `"}`
			if got := ParseStreamingJson(raw); !reflect.DeepEqual(got, JsonObject{"text": text}) {
				t.Fatalf("ParseStreamingJson(%q) = %#v", raw, got)
			}
			builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "custom", "model")
			builder.toolCallDelta(streamToolCallDelta{index: 0, id: "call", name: "edit", argumentsDelta: raw})
			builder.done(StopReasonToolUse, nil, "")
			result := builder.stream.Result()
			if call := result.Content[0].(ToolCall); !reflect.DeepEqual(call.Arguments, JsonObject{"text": text}) {
				t.Fatalf("streamed arguments=%#v", call.Arguments)
			}
		})
	}
}

func TestStreamingJSONRepairPreservesInvalidUnicodeEscapes(t *testing.T) {
	// Pi utils/json-parse.ts:3,61-73 leaves invalid \u escapes invalid, unlike \q.
	for _, input := range []string{`{"text":"a\uZZ"}`, `{"text":"a\u000z"}`} {
		if got := ParseStreamingJson(input); !reflect.DeepEqual(got, JsonObject{}) {
			t.Errorf("ParseStreamingJson(%s) = %#v, want empty object", input, got)
		}
	}
}

func BenchmarkStreamingToolJSONRepair(b *testing.B) {
	text := strings.Repeat("tool content\t", 128)
	raw := `{"path":"A\H","text":"` + text + `"}`
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		// Model the shared builder receiving growing tool arguments in chunks.
		for size := 128; size < len(raw); size += 128 {
			ParseStreamingJson(raw[:size])
		}
		got := ParseStreamingJson(raw)
		if got["text"] != text {
			b.Fatal("lost tool arguments")
		}
	}
}
