package tools

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports packages/durable/src/tools/write.ts.

const writeParameters = `{"type":"object","required":["path","content"],"properties":{"path":{"type":"string","description":"Path to the file to write (relative or absolute)"},"content":{"type":"string","description":"Content to write to the file"}}}`

// WriteToolInput is the arguments of the write tool.
type WriteToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// CreateWriteTool creates the write tool: it writes a file, creating it and its
// parent directories.
func CreateWriteTool() *durable.ToolRegistration {
	return &durable.ToolRegistration{
		ToolSchema: toolSchema("write", "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.", writeParameters),
		Execute:    executeWrite,
	}
}

func executeWrite(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	input, err := durable.FromJsonValue[WriteToolInput](args)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	executionEnv, err := requireEnv(api)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	absolutePath, err := resolveToolPath(ctx, executionEnv, input.Path)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	return withFileMutationQueue(ctx, executionEnv, absolutePath, func() (durable.ToolExecutionResult, error) {
		if ctx.Err() != nil {
			return durable.ToolExecutionResult{}, errors.New("Operation aborted")
		}
		if err := executionEnv.WriteFile(ctx, absolutePath, input.Content); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if ctx.Err() != nil {
			return durable.ToolExecutionResult{}, errors.New("Operation aborted")
		}
		return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Successfully wrote to " + input.Path}}}, nil
	})
}
