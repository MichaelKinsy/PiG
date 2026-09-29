// Package tools provides the execution-environment-backed tools of the agent harness.
package tools

import (
	"github.com/MichaelKinsy/PiG/agent/harness"
	builtin "github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// ExecutionToolContext carries the environment and can be embedded in an application's turn context.
type ExecutionToolContext = builtin.ExecutionToolContext

// ReadToolOptions configures image processing for a read tool.
type ReadToolOptions = builtin.ReadToolOptions

// ReadImageProcessorResult carries converted bytes or an omission notice.
type ReadImageProcessorResult = builtin.ReadImageProcessorResult

// ReadToolDetails describes truncated text reads.
type ReadToolDetails = builtin.HarnessReadToolDetails

// EditToolDetails contains both display and unified diffs.
type EditToolDetails = builtin.HarnessEditToolDetails

// BashToolDetails describes a bounded shell capture and its spill file.
type BashToolDetails = builtin.HarnessBashToolDetails

// BashExecution is the mutable request supplied to a bash preparation hook.
type BashExecution = builtin.BashExecution

// BashToolOptions configures the command prefix and preparation hook.
type BashToolOptions = builtin.BashToolOptions

// CreateReadTool reads files and images through the current turn's environment.
func CreateReadTool(options *ReadToolOptions) *harness.AgentHarnessTool {
	return builtin.CreateReadTool(options)
}

// CreateWriteTool writes files through the environment's canonical mutation queue.
func CreateWriteTool() *harness.AgentHarnessTool { return builtin.CreateWriteTool() }

// CreateEditTool applies disjoint replacements to the original file contents.
func CreateEditTool() *harness.AgentHarnessTool { return builtin.CreateEditTool() }

// CreateBashTool executes prepared commands and emits bounded progress checkpoints.
func CreateBashTool(options *BashToolOptions) *harness.AgentHarnessTool {
	return builtin.CreateBashTool(options)
}
