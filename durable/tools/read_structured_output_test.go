package tools

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

// Pi main 04414839 (#11015) packages/durable/src/tools/read.ts: read declares a structuredOutputSchema, a text file's text or an
// image's ImageContent, and returns it as structuredOutput, so a Code Mode script's `image(await tools.read(...))` gets the image
// block instead of "".

func TestReadDeclaresTheTextOrImageStructuredOutputSchema(t *testing.T) {
	const want = `{"anyOf":[{"type":"string"},{"type":"object","required":["type","data","mimeType"],"properties":{"type":{"const":"image","type":"string"},"data":{"type":"string"},"mimeType":{"type":"string"}}}]}`
	var schema any
	if err := json.Unmarshal([]byte(want), &schema); err != nil {
		t.Fatal(err)
	}
	if got := normalized(t, CreateReadTool(nil).StructuredOutputSchema); !reflect.DeepEqual(got, schema) {
		t.Fatalf("structuredOutputSchema = %v, want %v", got, schema)
	}
}

func TestReadReturnsAnImageBlockAsStructuredOutput(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "image.dat", must(base64.StdEncoding.DecodeString(onePixelPNG)))
	result := mustRun(t, CreateReadTool(nil), map[string]any{"path": "image.dat"}, executionEnv)
	if got, want := structuredJSON(t, result.ToolExecutionResult), `{"type":"image","data":"`+onePixelPNG+`","mimeType":"image/png"}`; got != want {
		t.Fatalf("structuredOutput = %s, want %s", got, want)
	}
}

func TestReadReturnsTheShownTextAsStructuredOutput(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "note.txt", "one\ntwo\nthree")
	writeText(t, executionEnv, "empty.txt", "")
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"whole file": {map[string]any{"path": "note.txt"}, `"one\ntwo\nthree"`},
		"a slice":    {map[string]any{"path": "note.txt", "offset": 2.0, "limit": 1.0}, `"two"`},
		"empty file": {map[string]any{"path": "empty.txt"}, `""`},
	} {
		result := mustRun(t, CreateReadTool(nil), tc.args, executionEnv)
		if got := structuredJSON(t, result.ToolExecutionResult); got != tc.want {
			t.Errorf("%s: structuredOutput = %s, want %s", name, got, tc.want)
		}
	}
}
