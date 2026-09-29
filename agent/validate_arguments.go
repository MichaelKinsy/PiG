package agent

// Ports packages/ai/src/utils/validation.ts:validateToolArguments.

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
)

// ValidateToolArguments clones, prepares optional nulls, coerces and validates the call's arguments without changing the provider call.
func ValidateToolArguments(tool ai.ToolSchema, call ai.ToolCall) (map[string]any, error) {
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
