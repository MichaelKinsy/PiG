package coding

import "github.com/MichaelKinsy/PiG/coding/extension"

// DefineTool returns the tool definition it is given, as Pi's `defineTool` (packages/coding-agent/src/core/extensions/types.ts:676) does.
// Pi uses it to preserve TypeScript's contextual inference of a tool's parameters; Go's definition is one concrete type, so the function adds only the call shape a ported extension writes.
//
// Ports packages/coding-agent/src/core/extensions/types.ts defineTool.
func DefineTool(tool extension.ToolDefinition) extension.ToolDefinition { return tool }
