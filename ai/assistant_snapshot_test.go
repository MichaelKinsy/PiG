package ai

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func snapshotSource() []AssistantContentBlock {
	return []AssistantContentBlock{
		TextContent{Text: "hello", TextSignature: "sig"},
		ThinkingContent{Thinking: "hmm", ThinkingSignature: "s", Redacted: true},
		ToolCall{ID: "call_1", Name: "write", Arguments: JsonObject{"path": "a.go", "n": 1.5, "flag": true, "none": nil, "list": []any{"x", 2.0}, "obj": map[string]any{"k": "v"}}},
		ToolCall{ID: "call_2", Name: "read", Arguments: JsonObject{}},
		ToolCall{ID: "call_3", Name: "bare"},
	}
}

// A snapshot taken from an earlier snapshot equals a snapshot taken from nothing, whatever changed in between.
func TestCloneAssistantContentFromMatchesCloneAssistantContent(t *testing.T) {
	changes := map[string]func([]AssistantContentBlock){
		"none": func([]AssistantContentBlock) {},
		"text grows": func(blocks []AssistantContentBlock) {
			blocks[0] = TextContent{Text: "hello world", TextSignature: "sig"}
		},
		"argument value":   func(blocks []AssistantContentBlock) { blocks[2].(ToolCall).Arguments["path"] = "b.go" },
		"argument added":   func(blocks []AssistantContentBlock) { blocks[2].(ToolCall).Arguments["extra"] = "e" },
		"argument removed": func(blocks []AssistantContentBlock) { delete(blocks[2].(ToolCall).Arguments, "n") },
		"nested in place": func(blocks []AssistantContentBlock) {
			blocks[2].(ToolCall).Arguments["obj"].(map[string]any)["k"] = "w"
		},
		"nested list":   func(blocks []AssistantContentBlock) { blocks[2].(ToolCall).Arguments["list"].([]any)[0] = "y" },
		"negative zero": func(blocks []AssistantContentBlock) { blocks[2].(ToolCall).Arguments["n"] = math.Copysign(0, -1) },
		"type changes":  func(blocks []AssistantContentBlock) { blocks[2].(ToolCall).Arguments["flag"] = "true" },
		"scratch changes": func(blocks []AssistantContentBlock) {
			call := blocks[3].(ToolCall)
			call.scratch.partialJson = `{"a"`
			blocks[3] = call
		},
		"text signature": func(blocks []AssistantContentBlock) { blocks[0] = TextContent{Text: "hello", TextSignature: "other"} },
		"nil becomes empty": func(blocks []AssistantContentBlock) {
			call := blocks[4].(ToolCall)
			call.Arguments = JsonObject{}
			blocks[4] = call
		},
		"invalid utf-8":  func(blocks []AssistantContentBlock) { blocks[2].(ToolCall).Arguments["path"] = "bad\xff" },
		"block replaced": func(blocks []AssistantContentBlock) { blocks[1] = TextContent{Text: "now text"} },
		"order differs": func(blocks []AssistantContentBlock) {
			call := blocks[3].(ToolCall)
			call.argumentOrder = schemaObjectOrder{"": {"b", "a"}}
			blocks[3] = call
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			source := snapshotSource()
			earlier := cloneAssistantContent(source)
			change(source)
			want := cloneAssistantContent(source)
			got := cloneAssistantContentFrom(source, earlier)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("snapshot from earlier = %#v, want %#v", got, want)
			}
			// The earlier snapshot is immutable: nothing the source changed shows in it.
			if !reflect.DeepEqual(earlier, cloneAssistantContent(snapshotSource())) {
				t.Fatalf("earlier snapshot changed: %#v", earlier)
			}
		})
	}
}

// Unchanged arguments are shared between snapshots rather than copied, and a snapshot never shares a map with its source.
func TestCloneAssistantContentFromSharesOnlyImmutableSnapshots(t *testing.T) {
	source := snapshotSource()
	first := cloneAssistantContentFrom(source, cloneAssistantContent(source))
	second := cloneAssistantContentFrom(source, first)
	firstArguments, secondArguments := first[2].(ToolCall).Arguments, second[2].(ToolCall).Arguments
	if reflect.ValueOf(firstArguments).Pointer() != reflect.ValueOf(secondArguments).Pointer() {
		t.Fatal("unchanged arguments were copied again")
	}
	if reflect.ValueOf(secondArguments).Pointer() == reflect.ValueOf(source[2].(ToolCall).Arguments).Pointer() {
		t.Fatal("snapshot shares the source's arguments map")
	}
	source[2].(ToolCall).Arguments["path"] = "changed"
	if second[2].(ToolCall).Arguments["path"] != "a.go" {
		t.Fatal("source mutation reached a snapshot")
	}
	third := cloneAssistantContentFrom(source, second)
	if third[2].(ToolCall).Arguments["path"] != "changed" || second[2].(ToolCall).Arguments["path"] != "a.go" {
		t.Fatalf("third = %#v, second = %#v", third[2], second[2])
	}
}

// A growing argument string is checked for UTF-8 validity only where it grew, and a split rune is still caught.
func TestCloneArgumentsFromGrowingStringKeepsUTF8Validation(t *testing.T) {
	var buffer accumulatedString
	text := buffer.append("", strings.Repeat("é", 1000))
	earlier := cloneArgumentsFrom(JsonObject{"content": text}, nil)
	grown := buffer.append(text, "tail")
	got := cloneArgumentsFrom(JsonObject{"content": grown}, earlier)
	if got["content"] != grown {
		t.Fatalf("grown content = %q", got["content"])
	}
	broken := buffer.append(grown, "\xe2\x82")
	got = cloneArgumentsFrom(JsonObject{"content": broken}, got)
	want := JsonObject(cloneJSONValue(JsonObject{"content": broken}).(map[string]any))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid tail: got %q, want %q", got["content"], want["content"])
	}
}

func TestAccumulatedStringAppendsInPlaceAndRestartsOnReplacement(t *testing.T) {
	var accumulated accumulatedString
	text := ""
	var seen []string
	for _, delta := range []string{"ab", "", "cd", "é"} {
		text = accumulated.append(text, delta)
		seen = append(seen, text)
	}
	if !reflect.DeepEqual(seen, []string{"ab", "ab", "abcd", "abcdé"}) {
		t.Fatalf("seen = %q", seen)
	}
	// A string returned earlier never changes when the buffer grows.
	if seen[2] != "abcd" || seen[0] != "ab" {
		t.Fatalf("an earlier string changed: %q", seen)
	}
	text = accumulated.append("replaced", "!")
	if text != "replaced!" || seen[3] != "abcdé" {
		t.Fatalf("after replacement = %q", text)
	}
}

func BenchmarkCloneAssistantContent(b *testing.B) {
	source := snapshotSource()
	for range 20 {
		source = append(source, ToolCall{ID: "c", Name: "write", Arguments: JsonObject{"path": "a.go", "content": strings.Repeat("line\n", 400)}})
	}
	earlier := cloneAssistantContent(source)
	b.Run("deep-copy", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			cloneAssistantContent(source)
		}
	})
	b.Run("from-earlier-snapshot", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			cloneAssistantContentFrom(source, earlier)
		}
	})
}

// DeepEqual cannot tell -0 from 0, so the sign is checked directly: a snapshot of 0 is not reused for -0.
func TestCloneArgumentsFromKeepsNegativeZero(t *testing.T) {
	earlier := cloneArgumentsFrom(JsonObject{"z": 0.0}, nil)
	got := cloneArgumentsFrom(JsonObject{"z": math.Copysign(0, -1)}, earlier)
	if !math.Signbit(got["z"].(float64)) {
		t.Fatalf("z = %v, want -0", got["z"])
	}
}
