package runtime

import (
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/src/runtime/tool-access.ts (toolsByName) and the helpers p2's tools share: a tool's declaration with its
// parameters' order kept, and the text results every tool returns.

// ToolsByName are the tools of extensions by name, with what they declare (a later tool of the same name wins, as Pi's
// Map does).
func ToolsByName(extensions *KeyedTable[*P2Extension]) map[string]P2Tool {
	byName := map[string]P2Tool{}
	if extensions == nil {
		return byName
	}
	for _, key := range extensions.Keys() {
		extension, _ := extensions.Get(key)
		for _, tool := range extension.Tools {
			byName[tool.Name] = tool
		}
	}
	return byName
}

// MustToolSchema is ToolSchemaOf for a constant declaration: a failure is a program error.
func MustToolSchema(name, description string, parameters schema.Schema) ai.ToolSchema {
	tool, err := ToolSchemaOf(name, description, parameters)
	if err != nil {
		panic(err)
	}
	return tool
}

// ToolText is a tool's text result: details (a JSON value, nil for none) and whether it is an error.
func ToolText(text string, details durable.JsonValue, isError bool) durable.ToolExecutionResult {
	result := durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}
	if details != nil {
		result.Details, result.HasDetails = details, true
	}
	if isError {
		result.IsError = &isError
	}
	return result
}
