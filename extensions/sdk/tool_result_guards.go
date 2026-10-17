package sdk

// Type guards for a tool_result event (Pi core/extensions/types.ts:1315-1338: isBashToolResult ... isLsToolResult, each
// `e.toolName === "<tool>"`). The payload arrives as decoded JSON, so a guard reads its toolName member; a payload without a
// string toolName is the result of no built-in tool.

func toolResultNamed(data map[string]any, name string) bool {
	got, _ := data["toolName"].(string)
	return got == name
}

// IsBashToolResult reports whether a tool_result event is the result of the bash tool (types.ts:1315).
func IsBashToolResult(data map[string]any) bool { return toolResultNamed(data, "bash") }

// IsPowerShellToolResult reports whether a tool_result event is the result of the powershell tool (types.ts:1318).
func IsPowerShellToolResult(data map[string]any) bool { return toolResultNamed(data, "powershell") }

// IsReadToolResult reports whether a tool_result event is the result of the read tool (types.ts:1321).
func IsReadToolResult(data map[string]any) bool { return toolResultNamed(data, "read") }

// IsEditToolResult reports whether a tool_result event is the result of the edit tool (types.ts:1324).
func IsEditToolResult(data map[string]any) bool { return toolResultNamed(data, "edit") }

// IsWriteToolResult reports whether a tool_result event is the result of the write tool (types.ts:1327).
func IsWriteToolResult(data map[string]any) bool { return toolResultNamed(data, "write") }

// IsGrepToolResult reports whether a tool_result event is the result of the grep tool (types.ts:1330).
func IsGrepToolResult(data map[string]any) bool { return toolResultNamed(data, "grep") }

// IsFindToolResult reports whether a tool_result event is the result of the find tool (types.ts:1333).
func IsFindToolResult(data map[string]any) bool { return toolResultNamed(data, "find") }

// IsLsToolResult reports whether a tool_result event is the result of the ls tool (types.ts:1336).
func IsLsToolResult(data map[string]any) bool { return toolResultNamed(data, "ls") }
