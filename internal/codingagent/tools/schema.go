package tools

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
)

// toolSchemaWithParameters retains the declaration's JSON property order alongside its mutable parameter map. A Go map literal cannot carry the enumeration order of Pi's Type.Object declarations.
func toolSchemaWithParameters(definition ai.ToolSchema, parameters string) ai.ToolSchema {
	var ordered ai.ToolSchema
	if err := json.Unmarshal([]byte(`{"parameters":`+parameters+`}`), &ordered); err != nil {
		panic(err) // Parameters are source literals, not user input.
	}
	ordered.Name = definition.Name
	ordered.Description = definition.Description
	ordered.PromptGuidelines = definition.PromptGuidelines
	ordered.ConstrainedSampling = definition.ConstrainedSampling
	return ordered
}
