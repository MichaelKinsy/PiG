package tools

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

// ArgumentSchema retains the TypeBox metadata and property order used by Pi's
// built-in argument validator. Schema remains plain JSON for provider requests.
func (t *ReadTool) ArgumentSchema() json.RawMessage {
	return nativeToolArgumentSchema(t.Schema(), "path", "offset", "limit")
}
func (t *BashTool) ArgumentSchema() json.RawMessage {
	return nativeToolArgumentSchema(t.Schema(), "command", "timeout")
}
func (t *PowerShellTool) ArgumentSchema() json.RawMessage {
	return nativeToolArgumentSchema(t.Schema(), "command", "timeout")
}
func (t *EditTool) ArgumentSchema() json.RawMessage {
	return nativeToolArgumentSchema(t.Schema(), "path", "edits")
}
func (t *WriteTool) ArgumentSchema() json.RawMessage {
	return nativeToolArgumentSchema(t.Schema(), "path", "content")
}
func (t *GrepTool) ArgumentSchema() json.RawMessage {
	return nativeToolArgumentSchema(t.Schema(), "pattern", "path", "glob", "ignoreCase", "literal", "context", "limit")
}
func (t *FindTool) ArgumentSchema() json.RawMessage {
	return nativeToolArgumentSchema(t.Schema(), "pattern", "path", "limit")
}
func (t *LsTool) ArgumentSchema() json.RawMessage {
	return nativeToolArgumentSchema(t.Schema(), "path", "limit")
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
