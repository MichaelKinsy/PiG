package coding

// Ports packages/coding-agent/src/core/tools/{edit,find,grep,ls,powershell,read,write}.ts create<Tool>ToolDefinition for the tools whose definition has no extra shape; createBashToolDefinition is tools.CreateBashToolDefinition.

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// builtinToolDefinition is the ToolDefinition of a built-in tool: the one [toolDefinition] builds for the session registry. A built-in tool's
// schema is a fixed document, so a failure to encode it is a programming error.
func builtinToolDefinition(tool agent.AgentTool) extension.ToolDefinition {
	definition, err := toolDefinition(tool)
	if err != nil {
		panic(fmt.Sprintf("coding: the %s tool schema does not encode: %v", tool.Name(), err))
	}
	// A built-in definition carries no execution mode (the agent supplies the parallel default) and the edit tool draws its own shell.
	definition.ExecutionMode = ""
	if _, ok := tool.(*tools.EditTool); ok {
		definition.RenderShell = extension.ToolRenderShellSelf // upstream: core/tools/edit.ts:157
	}
	return definition
}

// CreateEditToolDefinition is upstream's createEditToolDefinition (edit.ts:143).
func CreateEditToolDefinition(cwd string, options *tools.EditToolOptions) extension.ToolDefinition {
	return builtinToolDefinition(tools.CreateEditTool(cwd, options))
}

// CreateFindToolDefinition is upstream's createFindToolDefinition (find.ts:70).
func CreateFindToolDefinition(cwd string, options *tools.FindToolOptions) extension.ToolDefinition {
	return builtinToolDefinition(tools.CreateFindTool(cwd, options))
}

// CreateGrepToolDefinition is upstream's createGrepToolDefinition (grep.ts:70).
func CreateGrepToolDefinition(cwd string, options *tools.GrepToolOptions) extension.ToolDefinition {
	return builtinToolDefinition(tools.CreateGrepTool(cwd, options))
}

// CreateLsToolDefinition is upstream's createLsToolDefinition (ls.ts:54).
func CreateLsToolDefinition(cwd string, options *tools.LsToolOptions) extension.ToolDefinition {
	return builtinToolDefinition(tools.CreateLsTool(cwd, options))
}

// CreatePowerShellToolDefinition is upstream's createPowerShellToolDefinition (powershell.ts:49).
func CreatePowerShellToolDefinition(cwd string, options *tools.PowerShellToolOptions) extension.ToolDefinition {
	return builtinToolDefinition(tools.CreatePowerShellTool(cwd, options))
}

// CreateReadToolDefinition is upstream's createReadToolDefinition (read.ts:86).
func CreateReadToolDefinition(cwd string, options *tools.ReadToolOptions) extension.ToolDefinition {
	return builtinToolDefinition(tools.CreateReadTool(cwd, options))
}

// CreateWriteToolDefinition is upstream's createWriteToolDefinition (write.ts:44).
func CreateWriteToolDefinition(cwd string, options *tools.WriteToolOptions) extension.ToolDefinition {
	return builtinToolDefinition(tools.CreateWriteTool(cwd, options))
}
