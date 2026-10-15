package tools

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
)

// ArgumentSchema retains the TypeBox metadata and property order used by Pi's
// built-in argument validator. Schema remains plain JSON for provider requests.
// Each tool's parameters are source constants (the bash and PowerShell variants
// differ only in guidelines), so the bytes are encoded once per tool.
func (t *ReadTool) ArgumentSchema() json.RawMessage       { return readArgumentSchema() }
func (t *BashTool) ArgumentSchema() json.RawMessage       { return bashArgumentSchema() }
func (t *PowerShellTool) ArgumentSchema() json.RawMessage { return powerShellArgumentSchema() }
func (t *EditTool) ArgumentSchema() json.RawMessage       { return editArgumentSchema() }
func (t *WriteTool) ArgumentSchema() json.RawMessage      { return writeArgumentSchema() }
func (t *GrepTool) ArgumentSchema() json.RawMessage       { return grepArgumentSchema() }
func (t *FindTool) ArgumentSchema() json.RawMessage       { return findArgumentSchema() }
func (t *LsTool) ArgumentSchema() json.RawMessage         { return lsArgumentSchema() }

var (
	readArgumentSchema       = argumentSchemaOnce(func() ai.ToolSchema { return (&ReadTool{}).Schema() }, "path", "offset", "limit")
	bashArgumentSchema       = argumentSchemaOnce(func() ai.ToolSchema { return (&BashTool{}).Schema() }, "command", "timeout")
	powerShellArgumentSchema = argumentSchemaOnce(func() ai.ToolSchema { return (&PowerShellTool{}).Schema() }, "command", "timeout")
	editArgumentSchema       = argumentSchemaOnce(func() ai.ToolSchema { return (&EditTool{}).Schema() }, "path", "edits")
	writeArgumentSchema      = argumentSchemaOnce(func() ai.ToolSchema { return (&WriteTool{}).Schema() }, "path", "content")
	grepArgumentSchema       = argumentSchemaOnce(func() ai.ToolSchema { return (&GrepTool{}).Schema() }, "pattern", "path", "glob", "ignoreCase", "literal", "context", "limit")
	findArgumentSchema       = argumentSchemaOnce(func() ai.ToolSchema { return (&FindTool{}).Schema() }, "pattern", "path", "limit")
	lsArgumentSchema         = argumentSchemaOnce(func() ai.ToolSchema { return (&LsTool{}).Schema() }, "path", "limit")
)

// argumentSchemaOnce encodes a tool's argument schema once; each call returns its own copy of those bytes, so a caller may
// keep or edit them, at the cost of one copy instead of a schema clone and encode.
func argumentSchemaOnce(schema func() ai.ToolSchema, order ...string) func() json.RawMessage {
	encoded := sync.OnceValue(func() json.RawMessage { return nativeToolArgumentSchema(schema(), order...) })
	return func() json.RawMessage { return slices.Clone(encoded()) }
}

func nativeToolArgumentSchema(schema ai.ToolSchema, order ...string) json.RawMessage {
	return nativeSchemaJSON(schema.Parameters, order)
}

func nativeSchemaJSON(schema map[string]any, order []string) json.RawMessage {
	document := maps.Clone(schema)
	if typ, ok := document["type"].(string); ok && typ != "" {
		document["~kind"] = strings.ToUpper(typ[:1]) + typ[1:]
	}
	if properties, ok := document["properties"].(map[string]any); ok {
		if len(order) == 0 {
			if required, ok := document["required"].([]string); ok {
				order = slices.Clone(required)
			}
		}
		for _, key := range slices.Sorted(maps.Keys(properties)) {
			if !slices.Contains(order, key) {
				order = append(order, key)
			}
		}
		var out bytes.Buffer
		out.WriteByte('{')
		first := true
		for _, key := range order {
			child, exists := properties[key]
			if !exists {
				continue
			}
			if !first {
				out.WriteByte(',')
			}
			first = false
			encodedKey, _ := json.Marshal(key)
			out.Write(encodedKey)
			out.WriteByte(':')
			out.Write(nativeSchemaJSON(child.(map[string]any), nil))
		}
		out.WriteByte('}')
		document["properties"] = json.RawMessage(out.Bytes())
	}
	if items, ok := document["items"].(map[string]any); ok {
		document["items"] = nativeSchemaJSON(items, nil)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	return encoded
}
