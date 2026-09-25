package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestContentBlocksUnmarshalText(t *testing.T) {
	raw := []byte(`[{"type":"text","text":"hello"}]`)
	var blocks ContentBlocks
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(blocks, ContentBlocks{TextContent{Text: "hello"}}) {
		t.Fatalf("blocks = %#v", blocks)
	}
}

func TestContentBlocksUnmarshalClosedUnion(t *testing.T) {
	raw := []byte(`[
		{"type":"text","text":"hi","textSignature":"text-signature"},
		{"type":"image","mimeType":"image/png","data":"base64bytes"},
		{"type":"toolCall","id":"call-1","name":"bash","arguments":{"command":"ls"},"thoughtSignature":"thought-signature","namespace":"tools"},
		{"type":"thinking","thinking":"reasoning","thinkingSignature":"thinking-signature"}
	]`)
	var blocks ContentBlocks
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	want := ContentBlocks{
		TextContent{Text: "hi", TextSignature: "text-signature"},
		ImageContent{MimeType: "image/png", Data: "base64bytes"},
		ToolCall{ID: "call-1", Name: "bash", Arguments: JsonObject{"command": "ls"}, ThoughtSignature: "thought-signature", Namespace: "tools"},
		ThinkingContent{Thinking: "reasoning", ThinkingSignature: "thinking-signature"},
	}
	if !reflect.DeepEqual(blocks, want) {
		t.Fatalf("blocks = %#v, want %#v", blocks, want)
	}
}

func TestContentBlocksRejectUnknownType(t *testing.T) {
	var blocks ContentBlocks
	err := json.Unmarshal([]byte(`[{"type":"future_block","payload":"x"}]`), &blocks)
	if err == nil {
		t.Fatal("unknown content block type succeeded")
	}
}

func TestContentBlocksRoundTrip(t *testing.T) {
	input := ContentBlocks{
		ThinkingContent{Thinking: "Let me think", ThinkingSignature: "sig123"},
		TextContent{Text: "hello", TextSignature: "text123"},
		ToolCall{ID: "x", Name: "bash", Arguments: JsonObject{"cmd": "ls"}, Namespace: "tools"},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output ContentBlocks
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(output, input) {
		t.Fatalf("round trip = %#v, want %#v", output, input)
	}
}
