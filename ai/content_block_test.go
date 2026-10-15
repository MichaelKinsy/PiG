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

// JSON.parse keeps a lone UTF-16 unit, and so does a session line or a tool result block that carries one as an escape: the Go string holds its WTF-8 bytes and encodes back to the escape, never to U+FFFD.
func TestUnmarshalContentBlockKeepsALoneSurrogate(t *testing.T) {
	const lone = "a\xed\xa0\xbdb"
	for name, test := range map[string]struct {
		wire string
		text func(ContentBlock) string
	}{
		"text":     {`{"type":"text","text":"a\ud83db"}`, func(block ContentBlock) string { return block.(TextContent).Text }},
		"thinking": {`{"type":"thinking","thinking":"a\ud83db"}`, func(block ContentBlock) string { return block.(ThinkingContent).Thinking }},
	} {
		block, err := UnmarshalContentBlock([]byte(test.wire))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := test.text(block); got != lone {
			t.Errorf("%s decoded to %q, want the lone surrogate kept as %q", name, got, lone)
		}
		again, err := json.Marshal(block)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, want := string(again), test.wire; got != want {
			t.Errorf("%s re-encoded to %s, want %s", name, got, want)
		}
	}
}
