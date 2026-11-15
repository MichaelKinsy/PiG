package ai

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// pi: packages/ai/src/types.ts (Tool), packages/coding-agent/src/core/session-manager.ts:1188 (a session line is JSON.stringify of the entry)

// A tool declaration read from JSON and written again is the text it was read from: a lone UTF-16 surrogate in its name, its description, a
// property name and the schema's text keeps its escape, as JSON.parse and JSON.stringify keep it, and U+FFFD stays U+FFFD.
func TestToolSchemaJSONKeepsALoneSurrogate(t *testing.T) {
	for name, text := range map[string]string{
		"high":        `{"name":"N[\ud83d]","description":"D[\ud83d]","parameters":{"type":"object","properties":{"K[\ud83d]":{"type":"string","description":"PD[\ud83d]","enum":["E[\ud83d]"],"default":"DF[\ud83d]"}},"required":["K[\ud83d]"]}}`,
		"low":         `{"name":"N[\ude00]","description":"D[\ude00]","parameters":{"type":"object","properties":{"K[\ude00]":{"type":"string","description":"PD[\ude00]","enum":["E[\ude00]"],"default":"DF[\ude00]"}},"required":["K[\ude00]"]}}`,
		"reversed":    `{"name":"N[\ude00\ud83d]","description":"D[\ude00\ud83d]","parameters":{"type":"object","properties":{"K[\ude00\ud83d]":{"type":"string","description":"PD[\ude00\ud83d]","enum":["E[\ude00\ud83d]"],"default":"DF[\ude00\ud83d]"}},"required":["K[\ude00\ud83d]"]}}`,
		"replacement": "{\"name\":\"N[\uFFFD]\",\"description\":\"D[\uFFFD\uFFFD\uFFFD]\",\"parameters\":{\"type\":\"object\",\"properties\":{\"K[\uFFFD]\":{\"type\":\"string\",\"description\":\"PD[\uFFFD]\"}},\"required\":[\"K[\uFFFD]\"]}}",
	} {
		t.Run(name, func(t *testing.T) {
			var tool ToolSchema
			if err := json.Unmarshal([]byte(text), &tool); err != nil {
				t.Fatal(err)
			}
			line, err := jsstring.MarshalJSON(tool)
			if err != nil {
				t.Fatal(err)
			}
			if string(line) != text {
				t.Errorf("the session line encoder wrote\n%s\nfor\n%s", line, text)
			}
			nested, err := json.Marshal([]ToolSchema{tool})
			if err != nil {
				t.Fatal(err)
			}
			if string(nested) != "["+text+"]" {
				t.Errorf("encoding/json wrote\n%s\nfor\n[%s]", nested, text)
			}
		})
	}
	var tool ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"N[\ud83d]","description":"D[\ude00]","parameters":{"type":"object","properties":{"K[\ud83d]":{"type":"string"}}}}`), &tool); err != nil {
		t.Fatal(err)
	}
	properties, _ := tool.Parameters["properties"].(map[string]any)
	if _, found := properties["K[\xed\xa0\xbd]"]; tool.Name != "N[\xed\xa0\xbd]" || tool.Description != "D[\xed\xb8\x80]" || !found {
		t.Errorf("the declaration does not hold each unit in its three-byte form: name %q, description %q, properties %q", tool.Name, tool.Description, properties)
	}
}

// A schema built in Go has no recorded member order and is written with sorted members; its text keeps a lone surrogate too.
func TestToolParametersWithoutRecordedOrderKeepALoneSurrogate(t *testing.T) {
	tool := ToolSchema{Name: "n", Parameters: map[string]any{"type": "object", "description": "PD[\xed\xa0\xbd]"}}
	encoded, err := tool.ParametersJSON()
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"description":"PD[\ud83d]","type":"object"}`; string(encoded) != want {
		t.Errorf("ParametersJSON = %s, want %s", encoded, want)
	}
}

// toolWireKind is the tools of a request as one API kind sends them.
type toolWireKind struct {
	name  string
	value any
}

// BenchmarkToolDeclarationWire encodes the tools of a request as each API kind sends them, a system message that adds them as a session line holds
// it, and decodes them, for tools whose text is plain, holds a lone surrogate, and holds U+FFFD.
func BenchmarkToolDeclarationWire(b *testing.B) {
	for _, form := range []struct{ name, unit string }{{"plain", "x"}, {"lone-surrogate", `\ud83d`}, {"replacement-character", `\ufffd`}} {
		text := []byte("[")
		for i := range 8 {
			if i > 0 {
				text = append(text, ',')
			}
			text = append(text, `{"name":"tool_`+string(rune('a'+i))+`[`+form.unit+`]","description":"Reads a file `+form.unit+` and returns its text.","parameters":{"type":"object","properties":{"path":{"type":"string","description":"The path `+form.unit+`"},"limit":{"type":"number","default":2000}},"required":["path"]}}`...)
		}
		text = append(text, ']')
		var tools []ToolSchema
		if err := json.Unmarshal(text, &tools); err != nil {
			b.Fatal(err)
		}
		anthropic, completions, responses := make([]anthTool, len(tools)), make([]oaiToolFunction, len(tools)), make([]ResponsesTool, len(tools))
		for i, tool := range tools {
			anthropic[i] = anthTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.Parameters, inputSchemaOrder: tool.parameterOrder}
			completions[i] = oaiToolFunction{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters, parameterOrder: tool.parameterOrder}
			responses[i] = ResponsesTool{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters, parameterOrder: tool.parameterOrder}
		}
		google, err := geminiConvertTools(tools, false, false)
		if err != nil {
			b.Fatal(err)
		}
		kinds := []toolWireKind{{"anthropic-messages", anthropic}, {"openai-completions", completions}, {"openai-responses", responses}, {"google-generative-ai", google}}
		for _, kind := range append(kinds, mistralToolWireKinds(tools)...) {
			b.Run(form.name+"/"+kind.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := marshalRequestBody(kind.value); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
		b.Run(form.name+"/session-line", func(b *testing.B) {
			message := SystemMessage{Content: SystemText(""), ToolsAdded: tools}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := jsstring.MarshalJSON(message); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(form.name+"/decode", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var decoded []ToolSchema
				if err := json.Unmarshal(text, &decoded); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
