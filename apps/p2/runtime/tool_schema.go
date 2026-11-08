package runtime

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/schema"
)

// ToolSchemaOf is the model-facing schema of a tool: its name, description and parameters (pi-schema's `obj(...)` of a
// `defineTool`), property order kept. Built once per worker start.
func ToolSchemaOf(name, description string, parameters schema.Schema) (ai.ToolSchema, error) {
	encoded, err := json.Marshal(struct {
		Name        string        `json:"name"`
		Description string        `json:"description"`
		Parameters  schema.Schema `json:"parameters"`
	}{name, description, parameters})
	if err != nil {
		return ai.ToolSchema{}, err
	}
	var tool ai.ToolSchema
	//perf:ok the tool schema is built once per worker start (cold)
	err = json.Unmarshal(encoded, &tool) //portlint:allow jsonclone a typed value converted from its stored JSON form, cold (see the perf:ok above)
	return tool, err
}
