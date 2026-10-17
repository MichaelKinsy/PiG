package tui

import (
	"encoding/json"
	"testing"
)

// mayBeCompleteJSON never rejects a prefix that is valid JSON: over every prefix of documents ending in each kind of value, a valid one passes the check, so UpdateArgs skips a parse only when it would have failed.
func TestMayBeCompleteJSONNeverRejectsValidJSON(t *testing.T) {
	docs := []string{
		`{"path":"a.go","content":"line\n\"quoted\" \u00e9 }]","n":-12.5e+3,"ok":true,"no":false,"nil":null,"list":[1,2,[3]],"empty":{}}`,
		" \t\r\n{\"a\" : 1}\n ", `[1, 2.5, "x"]`, `"text"`, `12`, `-0.5E-7`, `true`, `false`, `null`, `{}`, `[]`, ``, ` `,
	}
	for _, doc := range docs {
		for end := 0; end <= len(doc); end++ {
			prefix := []byte(doc[:end])
			if json.Valid(prefix) && !mayBeCompleteJSON(prefix) {
				t.Fatalf("valid JSON %q rejected", prefix)
			}
		}
	}
	for _, text := range []string{`{"a":`, `{"a":1,`, `{"a`, `[1,`, `{"a":1}x`, `[1]]x`, `tru`, `-`, `1.`, `{"a":"b`, `{"a":[`} {
		if mayBeCompleteJSON([]byte(text)) {
			t.Errorf("%q cannot be complete JSON", text)
		}
	}
}

// A card fed the argument text received so far shows the header once the last delta completes the object, and not before.
func TestToolCardHeaderAppearsWhenArgumentsComplete(t *testing.T) {
	full := `{"path":"/work/project/src/file.go","content":"hello"}`
	card := NewToolExecutionComponent("write", "call-1", nil, ToolExecutionOptions{}, nil, nil, "/work/project")
	before := card.ArgsPreview
	for end := 1; end < len(full); end++ {
		card.UpdateArgs(json.RawMessage(full[:end]))
		if card.ArgsPreview != before {
			t.Fatalf("header changed at %d bytes (%q)", end, full[:end])
		}
	}
	card.UpdateArgs(json.RawMessage(full))
	if card.ArgsPreview == before || card.ArgsPreview != HeaderForTool("write", json.RawMessage(full), "/work/project") {
		t.Fatalf("header after the last delta = %q", card.ArgsPreview)
	}
}

// A card with a definition keeps, for every prefix of the argument text, the arguments the definition's renderers received last: the last prefix that was valid JSON, as when every prefix was validated.
func TestDefinitionArgsKeepTheLastValidPrefix(t *testing.T) {
	full := `{"path":"/work/project/src/file.go","content":"a } b ] c \"}\" d"}`
	card := NewToolExecutionComponent("write", "call-1", nil, ToolExecutionOptions{}, nil, nil, "/work/project")
	card.SetDefinition(&ToolDefinitionRenderers{}, json.RawMessage(`{"path":"first"}`))
	want := `{"path":"first"}`
	for end := 1; end <= len(full); end++ {
		prefix := json.RawMessage(full[:end])
		card.SetDefinitionArgs(prefix)
		if json.Valid(prefix) {
			want = string(prefix)
		}
		if string(card.definitionArgs) != want {
			t.Fatalf("after %q the renderers hold %q, want %q", prefix, card.definitionArgs, want)
		}
	}
}

// A definition card keeps the last complete arguments while later deltas are half-received, and takes each complete text, whitespace around it included, as SetDefinition and SetDefinitionArgs did when they validated every delta.
func TestDefinitionCardTakesOnlyCompleteArguments(t *testing.T) {
	card := NewToolExecutionComponent("write", "call-1", nil, ToolExecutionOptions{}, nil, nil, "/work/project")
	first := ` {"path":"a.go"} `
	card.SetDefinition(&ToolDefinitionRenderers{}, json.RawMessage(first))
	if string(card.definitionArgs) != first {
		t.Fatalf("SetDefinition kept %q", card.definitionArgs)
	}
	full := `{"path":"a.go","content":"x}"}`
	for end := 1; end < len(full); end++ {
		card.SetDefinitionArgs(json.RawMessage(full[:end]))
		if json.Valid([]byte(full[:end])) {
			if string(card.definitionArgs) != full[:end] {
				t.Fatalf("at %d bytes the arguments are %q, want %q", end, card.definitionArgs, full[:end])
			}
		} else if string(card.definitionArgs) != first {
			t.Fatalf("a half-received %q replaced the arguments with %q", full[:end], card.definitionArgs)
		}
	}
	card.SetDefinitionArgs(json.RawMessage(full + "\n"))
	if string(card.definitionArgs) != full+"\n" {
		t.Fatalf("complete arguments = %q", card.definitionArgs)
	}
}
