package ai

// Ports packages/ai/src/utils/validation.ts:validateToolCall, validateToolArguments.

import (
	"encoding/json"
	"fmt"
)

// ValidateToolCall finds the tool the call names and validates the call's arguments against its schema. It returns an error when no tool has the name or validation fails.
func ValidateToolCall(tools []ToolSchema, call ToolCall) (map[string]any, error) {
	for _, tool := range tools {
		if tool.Name == call.Name {
			return ValidateToolArguments(tool, call)
		}
	}
	// validation.ts:305 interpolates the name unescaped (`Tool "${toolCall.name}" not found`); %q would escape it.
	return nil, fmt.Errorf("Tool \"%s\" not found", call.Name)
}

// ValidateToolArguments clones, prepares optional nulls, coerces and validates the call's arguments without changing the provider call.
func ValidateToolArguments(tool ToolSchema, call ToolCall) (map[string]any, error) {
	arguments, err := json.Marshal(call.Arguments)
	if err != nil {
		return nil, err
	}
	validated, err := validateToolArgs(tool.Name, tool.Parameters, arguments)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(validated, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ValidateToolArgumentsJSON validates arguments against a JSON schema and returns the validated arguments with the member order of args, which is what an agent passes to hooks and execution. An empty schema accepts any object.
func ValidateToolArgumentsJSON(toolName string, schema, args json.RawMessage) (json.RawMessage, error) {
	return validateToolArgsSchema(toolName, schema, args)
}

// ValidateToolArgumentsSchema is ValidateToolArgumentsJSON for a schema held as a map; a nil schema accepts any object.
func ValidateToolArgumentsSchema(toolName string, schema map[string]any, args json.RawMessage) (json.RawMessage, error) {
	return validateToolArgs(toolName, schema, args)
}
