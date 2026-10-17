package tools

import (
	"encoding/json"
	"reflect"
	"testing"
)

// upstream: packages/durable/src/tools/read.ts (readSchema) and :90-93 (createReadTool): the read tool is declared to the model as name "read", the description
// "Read the contents of a file. Supports text files and images (${formats}). Images are sent as attachments. For text files, output is truncated to
// ${DEFAULT_MAX_LINES} lines or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with
// offset until complete." where formats is "jpg, png, gif, webp" without an image processor (truncate.ts: 2000 lines, 50 * 1024 bytes), and an object schema whose
// required `path` is a string and whose optional `offset` and `limit` are numbers, each with Pi's description.
// mutation-checked: each word of the description, a property description, `required`, the offset type and a moved schema member each fail it.
func TestReadToolDeclarationMatchesUpstream(t *testing.T) {
	tool := CreateReadTool(nil)
	if tool.Name != "read" {
		t.Fatalf("name = %q, want read", tool.Name)
	}
	const description = "Read the contents of a file. Supports text files and images (jpg, png, gif, webp). Images are sent as attachments. For text files, output is truncated to 2000 lines or 50KB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete."
	if tool.Description != description {
		t.Fatalf("description = %q\nwant %q", tool.Description, description)
	}
	const parameters = `{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {"type": "string", "description": "Path to the file to read (relative or absolute)"},
			"offset": {"type": "number", "description": "Line number to start reading from (1-indexed)"},
			"limit": {"type": "number", "description": "Maximum number of lines to read"}
		}
	}`
	var want map[string]any
	if err := json.Unmarshal([]byte(parameters), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tool.Parameters, want) {
		got, _ := json.Marshal(tool.Parameters)
		t.Fatalf("parameters = %s\nwant %v", got, want)
	}
	declaredParametersText(t, tool, parameters)
}
