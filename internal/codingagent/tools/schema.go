package tools

import (
	"encoding/json"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
)

// decodedParameters holds each parameters literal decoded once, with its key order. The literals are source constants, so
// the cache holds one entry per built-in tool.
var decodedParameters sync.Map // string -> ai.ToolSchema

// toolSchemaWithParameters retains the declaration's JSON property order alongside its mutable parameter map. A Go map literal cannot carry the enumeration order of Pi's Type.Object declarations. Every call returns its own parameter map.
func toolSchemaWithParameters(definition ai.ToolSchema, parameters string) ai.ToolSchema {
	cached, ok := decodedParameters.Load(parameters)
	if !ok {
		var decoded ai.ToolSchema
		if err := json.Unmarshal([]byte(`{"parameters":`+parameters+`}`), &decoded); err != nil {
			panic(err) // Parameters are source literals, not user input.
		}
		cached, _ = decodedParameters.LoadOrStore(parameters, decoded)
	}
	ordered := cached.(ai.ToolSchema)
	ordered.Parameters = cloneSchemaObject(ordered.Parameters)
	ordered.Name = definition.Name
	ordered.Description = definition.Description
	ordered.PromptGuidelines = definition.PromptGuidelines
	ordered.ConstrainedSampling = definition.ConstrainedSampling
	return ordered
}

// cloneSchemaObject deep-copies a decoded JSON object, whose values are maps, slices and immutable scalars.
func cloneSchemaObject(object map[string]any) map[string]any {
	if object == nil {
		return nil
	}
	out := make(map[string]any, len(object))
	for key, value := range object {
		out[key] = cloneSchemaValue(value)
	}
	return out
}

func cloneSchemaValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneSchemaObject(value)
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = cloneSchemaValue(item)
		}
		return out
	}
	return value
}
