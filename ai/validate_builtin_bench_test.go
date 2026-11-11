package ai_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// BenchmarkBuiltinToolArgumentValidation validates the arguments models send to the built-in tools: valid as sent, and coerced (numeric strings, null optionals, one value for an array).
// via-tool is the agent's path for a tool call: the tool's declared schema is encoded, then validated.
func BenchmarkBuiltinToolArgumentValidation(b *testing.B) {
	declared := map[string]ai.ToolSchema{
		"read": (&tools.ReadTool{}).Schema(), "edit": (&tools.EditTool{}).Schema(), "bash": (&tools.BashTool{}).Schema(), "write": (&tools.WriteTool{}).Schema(),
	}
	cases := []struct{ tool, name, args string }{
		{"read", "valid", `{"path":"file.txt","offset":2,"limit":100}`},
		{"read", "coerced", `{"path":"file.txt","offset":"2","limit":null}`},
		{"edit", "valid", `{"path":"file.txt","edits":[{"oldText":"old","newText":"new"}]}`},
		{"edit", "coerced", `{"path":"file.txt","edits":{"oldText":"old","newText":"new"}}`},
		{"bash", "valid", `{"command":"echo ok","timeout":10}`},
		{"bash", "coerced", `{"command":"echo ok","timeout":"10"}`},
		{"write", "valid", `{"path":"file.txt","content":"hello\nworld\n"}`},
		{"write", "coerced", `{"path":"file.txt","content":"hello","extra":null}`},
	}
	for _, c := range cases {
		parameters, err := declared[c.tool].ParametersJSON()
		if err != nil {
			b.Fatal(err)
		}
		args := json.RawMessage(c.args)
		b.Run(c.tool+"/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := ai.ValidateToolArgumentsJSON(c.tool, parameters, args); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(c.tool+"/"+c.name+"/via-tool", func(b *testing.B) {
			schema := declared[c.tool]
			b.ReportAllocs()
			for b.Loop() {
				encoded, err := schema.ParametersJSON()
				if err != nil {
					b.Fatal(err)
				}
				if _, err := ai.ValidateToolArgumentsJSON(c.tool, encoded, args); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
