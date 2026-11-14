//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// BenchmarkBedrockToolDeclarationWire is the Bedrock row of ai's BenchmarkToolDeclarationWire: the tool configuration of a Converse request for
// eight tools whose text is plain, holds a lone UTF-16 surrogate, or holds U+FFFD. The body is written by bedrockWire, whose string writer writes
// the unit as its escape, so the three forms cost the same.
func BenchmarkBedrockToolDeclarationWire(b *testing.B) {
	for _, form := range []struct{ name, unit string }{{"plain", "x"}, {"lone-surrogate", `\ud83d`}, {"replacement-character", `\ufffd`}} {
		text := []byte("[")
		for i := range 8 {
			if i > 0 {
				text = append(text, ',')
			}
			text = append(text, `{"name":"tool_`+string(rune('a'+i))+`[`+form.unit+`]","description":"Reads a file `+form.unit+` and returns its text.","parameters":{"type":"object","properties":{"path":{"type":"string","description":"The path `+form.unit+`"},"limit":{"type":"number"}},"required":["path"]}}`...)
		}
		text = append(text, ']')
		var tools []ai.ToolSchema
		if err := json.Unmarshal(text, &tools); err != nil {
			b.Fatal(err)
		}
		config, _, err := convertBedrockTools(tools, nil, false, false)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(form.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var wire bedrockWire
				wire.toolConfiguration(config)
				if wire.err != nil {
					b.Fatal(wire.err)
				}
			}
		})
	}
}
