package codemode_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
)

// outcomeLine is [kind, text] as one JSON line: "ok" with the rendered text, or "error" with the failure as Pi names it.
func outcomeLine(text string, err error) []any {
	if err != nil {
		return []any{"error", "URIError: " + err.Error()}
	}
	return []any{"ok", text}
}

func printCodemodeLine(t *testing.T, kind string, parts ...any) {
	t.Helper()
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(append([]any{kind}, parts...)); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("codemode-observation:%s", out.String())
}

// TestCodemodeDeclarationsParity prints PiG's rendering of every corpus input of testdata/upstream-golden.json (schemaToType, renderToolSignature, renderToolSample, renderDeclarations
// and renderToolOutputType), one JSON line per case, for the extensions-runtime/78 parity scenario; the Pi side regenerates the same corpus with Pi.
func TestCodemodeDeclarationsParity(t *testing.T) {
	golden := loadOracle(t)
	for _, c := range golden.SchemaToType {
		got, err := codemode.SchemaToType(json.RawMessage(c.Schema), codemode.SchemaToTypeOptions{MaxChars: c.MaxChars})
		printCodemodeLine(t, "schemaToType", outcomeLine(got, err))
	}
	for _, c := range golden.Signature {
		var tool oracleTool
		if err := json.Unmarshal([]byte(c.Tool), &tool); err != nil {
			t.Fatal(err)
		}
		got, err := codemode.RenderToolSignature(tool.tool(), codemode.ToolRenderOptions{InputMaxChars: c.InputMaxChars})
		printCodemodeLine(t, "signature", outcomeLine(got, err))
	}
	for _, c := range golden.Sample {
		var tool oracleTool
		if err := json.Unmarshal([]byte(c.Tool), &tool); err != nil {
			t.Fatal(err)
		}
		got, err := codemode.RenderToolSample(tool.tool(), codemode.ToolRenderOptions{InputMaxChars: c.InputMaxChars})
		printCodemodeLine(t, "sample", outcomeLine(got, err))
	}
	for _, c := range golden.Declarations {
		var options struct {
			Tools   []oracleTool `json:"tools"`
			Globals []oracleTool `json:"globals"`
		}
		if err := json.Unmarshal([]byte(c.Options), &options); err != nil {
			t.Fatal(err)
		}
		var rendered codemode.RenderDeclarationsOptions
		for _, tool := range options.Tools {
			rendered.Tools = append(rendered.Tools, tool.tool())
		}
		for _, tool := range options.Globals {
			rendered.Globals = append(rendered.Globals, tool.tool())
		}
		got, err := codemode.RenderDeclarations(rendered)
		printCodemodeLine(t, "declarations", outcomeLine(got, err))
	}
	for _, c := range golden.OutputType {
		schema := json.RawMessage(c.Schema)
		if c.Schema == "undefined" {
			schema = nil
		}
		got, err := codemode.RenderToolOutputType(schema)
		printCodemodeLine(t, "outputType", outcomeLine(got, err))
	}
}
