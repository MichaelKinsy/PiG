package ai

// Ports packages/ai/src/utils/string-enum.ts.

import (
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// StringEnumOptions are the optional description and default of StringEnum.
type StringEnumOptions struct {
	Description string
	Default     string
}

// StringEnum creates a string enum schema compatible with Google's API and other providers that don't support anyOf/const patterns:
// {"type":"string","enum":[...]}, plus the description and the default when they are non-empty (string-enum.ts:14-24).
// The schema is an ordered object, so it serializes in the member order Pi writes, and its enum is the caller's slice, as Pi's is the caller's array.
//
//	operation := StringEnum([]string{"add", "subtract", "multiply", "divide"}, &StringEnumOptions{Description: "The operation to perform"})
func StringEnum(values []string, options *StringEnumOptions) schema.Schema {
	if values == nil {
		values = []string{}
	}
	result := chordjson.ObjectOf("type", "string", "enum", values)
	if options != nil {
		if options.Description != "" {
			result.Set("description", options.Description)
		}
		if options.Default != "" {
			result.Set("default", options.Default)
		}
	}
	return result
}
