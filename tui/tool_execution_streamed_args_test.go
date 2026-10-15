package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

type countingStreamedArgs struct {
	raw    string
	fields map[string]any
	calls  *int
}

func (args countingStreamedArgs) Object() map[string]any { return args.fields }

func (args countingStreamedArgs) JSON() json.RawMessage {
	*args.calls++
	return json.RawMessage(args.raw)
}

// Streamed arguments cost a card nothing until it draws: however many deltas arrive between two frames, the card encodes
// only the newest arguments, once, and the renderers receive them. Complete arguments set afterwards replace them.
func TestStreamedArgsEncodeOncePerDraw(t *testing.T) {
	var got json.RawMessage
	definition := &ToolDefinitionRenderers{Call: func(input ToolRenderInput) (Component, bool) {
		got = append(got[:0], input.Args...)
		return NewPaddedText(string(input.Args), 0, 0, nil), true
	}}
	card := NewToolExecutionComponent("write", "call-1", nil, ToolExecutionOptions{}, definition, nil, "/work")
	calls := 0
	for i := range 100 {
		content := string(rune('a' + i%26))
		card.SetStreamedArgs(countingStreamedArgs{raw: `{"content":"` + content + `"}`, fields: map[string]any{"content": content}, calls: &calls})
	}
	if calls != 0 {
		t.Fatalf("updates encoded the arguments %d times before a draw", calls)
	}
	card.Render(80)
	if calls != 1 || string(got) != `{"content":"v"}` {
		t.Fatalf("one draw encoded %d times and drew %s, want once and the newest arguments", calls, got)
	}
	card.Render(80)
	if calls != 1 {
		t.Fatalf("a second draw encoded again (%d)", calls)
	}
	card.SetStreamedArgs(countingStreamedArgs{raw: `{"content":"partial"}`, calls: &calls})
	card.SetDefinitionArgs(json.RawMessage(`{"content":"complete"}`))
	card.Render(80)
	if calls != 1 || string(got) != `{"content":"complete"}` {
		t.Fatalf("complete arguments did not replace pending streamed ones: %d encodes, drew %s", calls, got)
	}
}

// A Call that reads streamed arguments decoded receives the parser's object and no text, so drawing a streaming card never
// encodes its arguments; the header a frontend reads names the path from the same object. Complete arguments replace the
// object and reach the Call as text.
func TestStreamedArgsReachAFieldsReaderDecoded(t *testing.T) {
	var got []ToolRenderInput
	definition := &ToolDefinitionRenderers{CallReadsFields: true, Call: func(input ToolRenderInput) (Component, bool) {
		got = append(got, input)
		return NewPaddedText("call", 0, 0, nil), true
	}}
	card := NewToolExecutionComponent("write", "call-1", nil, ToolExecutionOptions{}, definition, nil, "/work")
	calls := 0
	fields := map[string]any{"path": "a.go", "content": "package a\n"}
	card.SetStreamedArgs(countingStreamedArgs{raw: `{"path":"a.go","content":"package a\n"}`, fields: fields, calls: &calls})
	card.Render(80)
	last := got[len(got)-1]
	if calls != 0 || last.Args != nil || last.Fields["content"] != "package a\n" {
		t.Fatalf("a fields reader got Args %q and Fields %v after %d encodes", last.Args, last.Fields, calls)
	}
	card.applyStreamedArgs()
	if calls != 0 || card.ArgsPreview != HeaderForTool("write", json.RawMessage(`{"path":"a.go"}`), "/work") || card.decodedArguments()["path"] != "a.go" {
		t.Fatalf("the header %q and arguments %v of the streamed object (%d encodes)", card.ArgsPreview, card.decodedArguments(), calls)
	}
	card.SetDefinitionArgs(json.RawMessage(`{"path":"a.go","content":"package a\nvar x = 1\n"}`))
	card.Render(80)
	if last := got[len(got)-1]; last.Fields != nil || !strings.Contains(string(last.Args), "var x") {
		t.Fatalf("complete arguments reached the Call as Args %q and Fields %v", last.Args, last.Fields)
	}
}

// A card without a definition draws its header from the streamed arguments, read once per update.
func TestStreamedArgsDrawTheHeaderOfACardWithoutDefinition(t *testing.T) {
	card := NewToolExecutionComponent("grep", "call-1", nil, ToolExecutionOptions{}, nil, nil, "/work")
	calls := 0
	card.SetStreamedArgs(countingStreamedArgs{raw: `{"pattern":"needle"}`, fields: map[string]any{"pattern": "needle"}, calls: &calls})
	card.Render(80)
	card.Render(80)
	if calls != 1 || card.ArgsPreview != HeaderForTool("grep", json.RawMessage(`{"pattern":"needle"}`), "/work") {
		t.Fatalf("header %q after %d encodes", card.ArgsPreview, calls)
	}
}
