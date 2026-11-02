package ai

// Ports packages/ai/src/utils/string-enum.ts.

// StringEnumOptions are the optional description and default of StringEnum.
type StringEnumOptions struct {
	Description string
	Default     string
}

// StringEnum creates a string enum schema compatible with Google's API and other providers that don't support anyOf/const patterns:
// {"type":"string","enum":[...]}, plus the description and the default when they are non-empty (string-enum.ts:14-24).
//
//	operation := StringEnum([]string{"add", "subtract", "multiply", "divide"}, &StringEnumOptions{Description: "The operation to perform"})
func StringEnum(values []string, options *StringEnumOptions) map[string]any {
	schema := map[string]any{"type": "string", "enum": append([]string{}, values...)}
	if options != nil {
		if options.Description != "" {
			schema["description"] = options.Description
		}
		if options.Default != "" {
			schema["default"] = options.Default
		}
	}
	return schema
}
