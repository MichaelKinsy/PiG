package tui

import "slices"

// tool_renderers.go: the built-in tool renderer registry.
//
// Ports upstream packages/coding-agent/src/core/tools/renderers/index.ts.
// Upstream keys renderCall/renderResult pairs by built-in tool name
// (createAllToolRenderers) and merges them into any registered definition that
// does not supply its own (withBuiltInRenderers). Pig dispatches the same way
// by name: FormatBuiltinToolHeader is the renderCall half and
// internal/codingagent toolBodyRenderer the renderResult half.

// builtInToolRendererNames lists the keys of upstream createAllToolRenderers.
var builtInToolRendererNames = [...]string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls"}

// HasBuiltInToolRenderers reports whether upstream createAllToolRenderers has
// an entry for toolName. withBuiltInRenderers falls back to that entry for a
// registered definition (for example an extension override of a built-in
// name) that supplies no renderCall or renderResult of its own.
func HasBuiltInToolRenderers(toolName string) bool {
	return slices.Contains(builtInToolRendererNames[:], toolName)
}

// ShellToolPrompt returns the prompt upstream createAllToolRenderers assigns
// to a built-in shell tool: createShellRenderers("$") for bash and
// createShellRenderers("PS>") for powershell. ok is false for other tools.
func ShellToolPrompt(toolName string) (prompt string, ok bool) {
	switch toolName {
	case "bash":
		return "$", true
	case "powershell":
		return "PS>", true
	}
	return "", false
}

// IsShellTool reports whether toolName uses the shared shell renderers.
func IsShellTool(toolName string) bool {
	_, ok := ShellToolPrompt(toolName)
	return ok
}
