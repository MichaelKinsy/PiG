package codemode_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
)

// The golden file holds Pi's packages/codemode pure functions (declarations, identifier, source) run on a
// deterministic corpus and seeded-random inputs by testdata/upstream-oracle.mjs. Regenerate it from the repository
// root with: node codemode/testdata/upstream-oracle.mjs > codemode/testdata/upstream-golden.json

type oracleOutcome struct {
	OK    *string `json:"ok"`
	Error *string `json:"error"`
}

type oracleTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema"`
	Signature    *string         `json:"signature"`
}

func (o oracleTool) tool() codemode.Tool {
	tool := codemode.Tool{Name: o.Name, Description: o.Description, InputSchema: o.InputSchema, OutputSchema: o.OutputSchema}
	tool.Signature = o.Signature
	return tool
}

type oracleGolden struct {
	SchemaToType []struct {
		oracleOutcome
		Schema   string `json:"schema"`
		MaxChars *int   `json:"maxChars"`
	} `json:"schemaToType"`
	Signature []struct {
		oracleOutcome
		Tool          string `json:"tool"`
		InputMaxChars *int   `json:"inputMaxChars"`
	} `json:"signature"`
	Sample []struct {
		oracleOutcome
		Tool          string `json:"tool"`
		InputMaxChars *int   `json:"inputMaxChars"`
	} `json:"sample"`
	Declarations []struct {
		oracleOutcome
		Options string `json:"options"`
	} `json:"declarations"`
	Identifier []struct {
		Name string `json:"name"`
		OK   string `json:"ok"`
	} `json:"identifier"`
	Source []struct {
		oracleOutcome
		Input string `json:"input"`
	} `json:"source"`
	OutputType []struct {
		Schema     string        `json:"schema"`
		Type       oracleOutcome `json:"type"`
		Structured oracleOutcome `json:"structured"`
	} `json:"outputType"`
	SourceError []struct {
		Message string `json:"message"`
		Name    string `json:"name"`
		Text    string `json:"text"`
		IsError bool   `json:"isError"`
	} `json:"sourceError"`
	Constants struct {
		DefaultInputSchemaMaxChars int    `json:"defaultInputSchemaMaxChars"`
		McpTypescriptPreamble      string `json:"mcpTypescriptPreamble"`
		SourceGrammar              string `json:"sourceGrammar"`
		MaxStoreValueChars         int    `json:"maxStoreValueChars"`
		MaxStoreTotalChars         int    `json:"maxStoreTotalChars"`
		MaxOutputChars             int    `json:"maxOutputChars"`
		MaxOutputItems             int    `json:"maxOutputItems"`
	} `json:"constants"`
}

func loadOracle(t *testing.T) oracleGolden {
	t.Helper()
	data, err := os.ReadFile("testdata/upstream-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden oracleGolden
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

// reportDifferences fails once with every differing case so a root cause shows its whole footprint.
func reportDifferences(t *testing.T, kind string, total int, differences []string) {
	t.Helper()
	if len(differences) == 0 {
		return
	}
	shown := differences
	if len(shown) > 12 {
		shown = shown[:12]
	}
	t.Errorf("%s: %d of %d cases differ from Pi:\n%s", kind, len(differences), total, strings.Join(shown, "\n"))
}

func TestSchemaToTypeMatchesPi(t *testing.T) {
	golden := loadOracle(t)
	var differences []string
	for _, c := range golden.SchemaToType {
		got, err := codemode.SchemaToType(json.RawMessage(c.Schema), codemode.SchemaToTypeOptions{MaxChars: c.MaxChars})
		if c.Error != nil {
			// Pi's decodeURIComponent throws a URIError out of schemaToType for a $ref segment that is not valid
			// percent-encoding; SchemaToType returns that failure as its error.
			if err == nil || "URIError: "+err.Error() != *c.Error {
				differences = append(differences, "schema "+c.Schema+"\n  got  "+got+" / "+fmt.Sprint(err)+"\n  want "+*c.Error)
			}
			continue
		}
		if err != nil {
			differences = append(differences, "schema "+c.Schema+"\n  got error "+err.Error())
			continue
		}
		if c.OK != nil && got != *c.OK {
			differences = append(differences, "schema "+c.Schema+"\n  got  "+got+"\n  want "+*c.OK)
		}
	}
	reportDifferences(t, "schemaToType", len(golden.SchemaToType), differences)
}

func TestToolSignatureAndSampleMatchPi(t *testing.T) {
	golden := loadOracle(t)
	var differences []string
	for _, c := range golden.Signature {
		var tool oracleTool
		if err := json.Unmarshal([]byte(c.Tool), &tool); err != nil {
			t.Fatal(err)
		}
		if got := mustSignature(t, tool.tool(), codemode.ToolRenderOptions{InputMaxChars: c.InputMaxChars}); got != *c.OK {
			differences = append(differences, "signature "+c.Tool+"\n  got  "+got+"\n  want "+*c.OK)
		}
	}
	reportDifferences(t, "renderToolSignature", len(golden.Signature), differences)
	differences = nil
	for _, c := range golden.Sample {
		var tool oracleTool
		if err := json.Unmarshal([]byte(c.Tool), &tool); err != nil {
			t.Fatal(err)
		}
		if got := mustSample(t, tool.tool(), codemode.ToolRenderOptions{InputMaxChars: c.InputMaxChars}); got != *c.OK {
			differences = append(differences, "sample "+c.Tool+"\n  got  "+got+"\n  want "+*c.OK)
		}
	}
	reportDifferences(t, "renderToolSample", len(golden.Sample), differences)
}

// Pi source: packages/codemode/src/declarations.ts
// mutation-checked: dropping the reads and writes of RenderDeclarationsOptions.Globals, RenderDeclarationsOptions.Tools fails it
func TestRenderDeclarationsMatchesPi(t *testing.T) {
	golden := loadOracle(t)
	var differences []string
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
		if got := mustDeclarations(t, rendered); got != *c.OK {
			differences = append(differences, "options "+c.Options+"\n  got  "+got+"\n  want "+*c.OK)
		}
	}
	reportDifferences(t, "renderDeclarations", len(golden.Declarations), differences)
}

func TestToCodemodeIdentifierMatchesPi(t *testing.T) {
	golden := loadOracle(t)
	var differences []string
	for _, c := range golden.Identifier {
		if got := codemode.ToCodemodeIdentifier(c.Name); got != c.OK {
			differences = append(differences, "name "+c.Name+"\n  got  "+got+"\n  want "+c.OK)
		}
	}
	reportDifferences(t, "toCodemodeIdentifier", len(golden.Identifier), differences)
}

func TestParseCodemodeSourceMatchesPi(t *testing.T) {
	golden := loadOracle(t)
	var differences []string
	for _, c := range golden.Source {
		parsed, err := codemode.ParseCodemodeSource(c.Input)
		var got string
		if err != nil {
			var sourceError *codemode.SourceError
			if !errors.As(err, &sourceError) {
				t.Fatalf("input %q: error %v is not a *SourceError", c.Input, err)
			}
			got = "CodemodeSourceError: " + sourceError.Message
			if c.Error == nil || got != *c.Error {
				want := "<ok>"
				if c.Error != nil {
					want = *c.Error
				}
				differences = append(differences, "input "+c.Input+"\n  got  "+got+"\n  want "+want)
			}
			continue
		}
		if c.Error != nil {
			differences = append(differences, "input "+c.Input+"\n  got  <ok>\n  want "+*c.Error)
			continue
		}
		options := map[string]any{}
		if parsed.Options.MaxOutputTokens != nil {
			options["maxOutputTokens"] = float64(*parsed.Options.MaxOutputTokens)
		}
		if parsed.Options.TimeoutMs != nil {
			options["timeoutMs"] = float64(*parsed.Options.TimeoutMs)
		}
		encoded, _ := json.Marshal(map[string]any{"code": parsed.Code, "options": options})
		var want map[string]any
		if err := json.Unmarshal([]byte(*c.OK), &want); err != nil {
			t.Fatal(err)
		}
		wantEncoded, _ := json.Marshal(want)
		if string(encoded) != string(wantEncoded) {
			differences = append(differences, "input "+c.Input+"\n  got  "+string(encoded)+"\n  want "+string(wantEncoded))
		}
	}
	reportDifferences(t, "parseCodemodeSource", len(golden.Source), differences)
}

func TestExportedConstantsMatchPi(t *testing.T) {
	want := loadOracle(t).Constants
	if codemode.DefaultInputSchemaMaxChars != want.DefaultInputSchemaMaxChars {
		t.Errorf("DefaultInputSchemaMaxChars = %d, Pi %d", codemode.DefaultInputSchemaMaxChars, want.DefaultInputSchemaMaxChars)
	}
	if codemode.McpTypescriptPreamble != want.McpTypescriptPreamble {
		t.Errorf("McpTypescriptPreamble differs from Pi's MCP_TYPESCRIPT_PREAMBLE:\n%q\n%q", codemode.McpTypescriptPreamble, want.McpTypescriptPreamble)
	}
	if codemode.CodemodeSourceGrammar != want.SourceGrammar {
		t.Errorf("CodemodeSourceGrammar differs from Pi's CODEMODE_SOURCE_GRAMMAR:\n%q\n%q", codemode.CodemodeSourceGrammar, want.SourceGrammar)
	}
	for name, pair := range map[string][2]int{
		"MaxStoreValueChars": {codemode.MaxStoreValueChars, want.MaxStoreValueChars},
		"MaxStoreTotalChars": {codemode.MaxStoreTotalChars, want.MaxStoreTotalChars},
		"MaxOutputChars":     {codemode.MaxOutputChars, want.MaxOutputChars},
		"MaxOutputItems":     {codemode.MaxOutputItems, want.MaxOutputItems},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %d, Pi %d", name, pair[0], pair[1])
		}
	}
}

// renderToolOutputType and mcpStructuredContentSchema (declarations.ts:166-200): the type a tool call resolves to
// (CallToolResult<T> for an MCP output schema, the schema's type otherwise, unknown without one, URIError for a malformed
// $ref) and the structuredContent schema of a CallToolResult output schema (JSON text, or undefined when the schema is not one).
func TestToolOutputTypeAndMcpStructuredContentMatchPi(t *testing.T) {
	golden := loadOracle(t)
	if len(golden.OutputType) == 0 {
		t.Fatal("the golden file has no outputType cases; regenerate it with codemode/testdata/upstream-oracle.mjs")
	}
	var differences []string
	for _, c := range golden.OutputType {
		schema := json.RawMessage(c.Schema)
		if c.Schema == "undefined" {
			schema = nil
		}
		got, err := codemode.RenderToolOutputType(schema)
		switch {
		case c.Type.Error != nil:
			if err == nil || "URIError: "+err.Error() != *c.Type.Error {
				differences = append(differences, "outputType "+c.Schema+"\n  got  "+got+" / "+fmt.Sprint(err)+"\n  want "+*c.Type.Error)
			}
		case err != nil || got != *c.Type.OK:
			differences = append(differences, "outputType "+c.Schema+"\n  got  "+got+" / "+fmt.Sprint(err)+"\n  want "+*c.Type.OK)
		}
		structured := codemode.McpStructuredContentSchema(schema)
		switch {
		case c.Structured.OK == nil && structured != nil:
			differences = append(differences, "structuredContent "+c.Schema+"\n  got  "+string(structured)+"\n  want undefined")
		case c.Structured.OK != nil && string(structured) != *c.Structured.OK:
			differences = append(differences, "structuredContent "+c.Schema+"\n  got  "+string(structured)+"\n  want "+*c.Structured.OK)
		}
	}
	reportDifferences(t, "renderToolOutputType/mcpStructuredContentSchema", len(golden.OutputType), differences)
}

// source.ts:45-50: `new CodemodeSourceError(message)` is an Error carrying the message and the name "CodemodeSourceError".
func TestNewSourceErrorMatchesPiCodemodeSourceError(t *testing.T) {
	golden := loadOracle(t)
	if len(golden.SourceError) == 0 {
		t.Fatal("the golden file has no sourceError cases; regenerate it with codemode/testdata/upstream-oracle.mjs")
	}
	for _, c := range golden.SourceError {
		var err error = codemode.NewSourceError(c.Message)
		var sourceErr *codemode.SourceError
		if isError := errors.As(err, &sourceErr); isError != c.IsError {
			t.Fatalf("NewSourceError(%q) is an error = %v, Pi %v", c.Message, isError, c.IsError)
		}
		if err.Error() != c.Message || sourceErr.Message != c.Message {
			t.Errorf("NewSourceError(%q).Error() = %q, want the message", c.Message, err.Error())
		}
		// Error.prototype.toString: the name, then ": " and the message when the message is not empty.
		text := sourceErr.Name()
		if sourceErr.Error() != "" {
			text += ": " + sourceErr.Error()
		}
		if text != c.Text {
			t.Errorf("NewSourceError(%q) string form = %q, Pi String(error) = %q", c.Message, text, c.Text)
		}
		if sourceErr.Name() != c.Name {
			t.Errorf("NewSourceError(%q).Name() = %q, want %q", c.Message, sourceErr.Name(), c.Name)
		}
	}
}
