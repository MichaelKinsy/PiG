// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package tools

// Ports packages/coding-agent/src/core/tools/tool-definition-wrapper.ts (createToolDefinitionFromAgentTool) and
// packages/coding-agent/src/core/tools/bash.ts (createBashToolDefinition).

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// ToolDefinitionFromAgentTool is upstream's createToolDefinitionFromAgentTool: the ToolDefinition that runs tool. It carries the
// tool's name, label, description, parameters, structured-output schema, constrained sampling, argument preparation and execution
// mode, and runs the tool's own Execute. A tool without a label is labelled with its name, as the tool registry lists it.
func ToolDefinitionFromAgentTool(tool agent.AgentTool) (extension.ToolDefinition, error) {
	schema := tool.Schema()
	parameters, err := json.Marshal(schema.Parameters)
	if err != nil {
		return extension.ToolDefinition{}, err
	}
	var sampling json.RawMessage
	if schema.ConstrainedSampling != nil {
		sampling, err = json.Marshal(schema.ConstrainedSampling)
		if err != nil {
			return extension.ToolDefinition{}, err
		}
	}
	label := tool.Label()
	if label == "" {
		label = tool.Name()
	}
	definition := extension.ToolDefinition{
		Name: tool.Name(), Label: label, Description: schema.Description, Parameters: parameters, ConstrainedSampling: sampling,
		PromptGuidelines: schema.PromptGuidelines, ExecutionMode: extension.ToolExecutionMode(tool.ExecutionMode()),
		Execute: func(ctx context.Context, id string, args json.RawMessage, onUpdate extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			return tool.Execute(ctx, id, args, onUpdate)
		},
	}
	if preparer, ok := tool.(agent.ArgumentPreparer); ok {
		definition.PrepareArguments = preparer.PrepareArguments
	}
	if provider, ok := tool.(agent.OutputSchemaProvider); ok {
		definition.OutputSchema = provider.OutputSchema()
	}
	return definition, nil
}

// CreateBashToolDefinition is upstream's createBashToolDefinition (bash.ts:427): the bash tool as a ToolDefinition, with the
// prompt snippet the system prompt lists it under.
func CreateBashToolDefinition(cwd string, options *BashToolOptions) (extension.ToolDefinition, error) {
	tool := CreateBashTool(cwd, options)
	definition, err := ToolDefinitionFromAgentTool(tool)
	if err != nil {
		return extension.ToolDefinition{}, err
	}
	definition.PromptSnippet = prompts.DefaultToolSnippets()[tool.Name()]
	definition.ExecutionMode = "" // upstream: core/tools/bash.ts createBashToolDefinition sets no executionMode; the agent supplies the parallel default
	return definition, nil
}
