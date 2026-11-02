package tools

import (
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
)

// toolSchema builds the pi-ai tool declaration of a tool. parameters is the
// JSON Schema pi-schema's obj() builder emits for the tool, member order
// included; decoding it keeps that order for provider requests.
func toolSchema(name, description, parameters string) ai.ToolSchema {
	encoded, err := json.Marshal(struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}{name, description, json.RawMessage(parameters)})
	if err != nil {
		panic(fmt.Sprintf("tool %s: invalid parameter schema: %v", name, err))
	}
	var schema ai.ToolSchema
	if err := json.Unmarshal(encoded, &schema); err != nil {
		panic(fmt.Sprintf("tool %s: invalid tool declaration: %v", name, err))
	}
	return schema
}
