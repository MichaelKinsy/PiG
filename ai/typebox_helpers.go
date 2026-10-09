package ai

// Ports packages/ai/src/utils/typebox-helpers.ts.

// StringEnumOptions are the optional description and default of StringEnum.
type StringEnumOptions struct {
	Description string
	Default     string
}

// StringEnum creates a string enum JSON schema compatible with Google's API and other providers that do not support anyOf/const patterns: {"type":"string","enum":values} plus the description and default when they are non-empty (typebox-helpers.ts:14-24).
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
