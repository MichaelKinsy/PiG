package tools

// pi: packages/durable/src/tools/index.ts

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

// packages/durable/src/tools/index.ts:21-25: CodingTools is the "coding-tools" extension with read, write, edit and
// bash in that order; createPowerShellTool is not part of it.
func TestCodingToolsAreReadWriteEditAndBashInOrder(t *testing.T) {
	if CodingTools.Name != "coding-tools" {
		t.Fatalf("extension name %q, want coding-tools", CodingTools.Name)
	}
	names := []string{}
	for _, tool := range CodingTools.Tools {
		names = append(names, tool.Name)
	}
	if want := []string{"read", "write", "edit", "bash"}; !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
}

// packages/durable/src/tools/powershell.ts: PowerShellToolInput is the bash tool's input, { command, timeout? }: it decodes the arguments a
// model sends to the powershell tool and omits an absent timeout when encoded.
func TestPowerShellToolInputIsTheBashInputShape(t *testing.T) {
	timeout := 5.0
	for _, c := range []struct {
		name string
		json string
		want PowerShellToolInput
	}{
		{"command only", `{"command":"Get-Date"}`, PowerShellToolInput{Command: "Get-Date"}},
		{"command and timeout", `{"command":"Get-ChildItem","timeout":5}`, PowerShellToolInput{Command: "Get-ChildItem", Timeout: &timeout}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var decoded PowerShellToolInput
			if err := json.Unmarshal([]byte(c.json), &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Command != c.want.Command || (decoded.Timeout == nil) != (c.want.Timeout == nil) || decoded.Timeout != nil && *decoded.Timeout != *c.want.Timeout {
				t.Fatalf("decoded %+v, want %+v", decoded, c.want)
			}
			if encoded, err := json.Marshal(c.want); err != nil || string(encoded) != c.json {
				t.Fatalf("encoded %s (err %v), want %s", encoded, err, c.json)
			}
			if reflect.TypeFor[PowerShellToolInput]() != reflect.TypeFor[BashToolInput]() {
				t.Fatal("PowerShellToolInput is not BashToolInput")
			}
		})
	}
}

// tools/write.ts schema { path, content }, tools/edit.ts:35 EditToolInput { path, edits: [{ oldText, newText }] } and :73-77 EditToolDetails
// { diff, patch, firstChangedLine? }: the JSON a model sends decodes into the Go types and encodes back to the same keys; an absent
// firstChangedLine stays absent.
func TestWriteAndEditToolInputAndDetailsJSONShapes(t *testing.T) {
	for _, c := range []struct {
		name   string
		json   string
		decode func(string) (any, error)
	}{
		{"write input", `{"path":"a.txt","content":"hi"}`, func(s string) (any, error) { var v WriteToolInput; return v, json.Unmarshal([]byte(s), &v) }},
		{"edit input", `{"path":"a.txt","edits":[{"oldText":"a","newText":"b"},{"oldText":"c","newText":"d"}]}`, func(s string) (any, error) { var v EditToolInput; return v, json.Unmarshal([]byte(s), &v) }},
		{"edit details without a line", `{"diff":"-a\n+b","patch":"@@"}`, func(s string) (any, error) { var v EditToolDetails; return v, json.Unmarshal([]byte(s), &v) }},
		{"edit details with a line", `{"diff":"d","patch":"p","firstChangedLine":12}`, func(s string) (any, error) { var v EditToolDetails; return v, json.Unmarshal([]byte(s), &v) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			value, err := c.decode(c.json)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != c.json {
				t.Fatalf("round trip = %s (err %v), want %s", encoded, err, c.json)
			}
		})
	}
	var input EditToolInput
	if err := json.Unmarshal([]byte(`{"path":"p","edits":[{"oldText":"x","newText":"y"}]}`), &input); err != nil || input.Path != "p" || len(input.Edits) != 1 || input.Edits[0].OldText != "x" || input.Edits[0].NewText != "y" {
		t.Fatalf("EditToolInput = %+v (err %v)", input, err)
	}
}
