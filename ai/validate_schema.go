package ai

import "maps"

// TypeBox accepts positional items and dependencies alongside current JSON
// Schema keywords. Translate those spellings for the draft-2020 validator,
// without changing the schema used for conversion or error traversal.
func argumentCompilerSchema(value any) any {
	original, ok := value.(map[string]any)
	if !ok {
		return value
	}
	schema := maps.Clone(original)
	for _, keyword := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
		if children, ok := schema[keyword].(map[string]any); ok {
			translated := make(map[string]any, len(children))
			for key, child := range children {
				translated[key] = argumentCompilerSchema(child)
			}
			schema[keyword] = translated
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if children, ok := schema[keyword].([]any); ok {
			translated := make([]any, len(children))
			for i, child := range children {
				translated[i] = argumentCompilerSchema(child)
			}
			schema[keyword] = translated
		}
	}
	for _, keyword := range []string{"additionalProperties", "propertyNames", "contains", "if", "then", "else", "not", "unevaluatedProperties", "unevaluatedItems"} {
		if child, ok := schema[keyword]; ok {
			schema[keyword] = argumentCompilerSchema(child)
		}
	}
	if tuple, ok := schema["items"].([]any); ok {
		translated := make([]any, len(tuple))
		for i, child := range tuple {
			translated[i] = argumentCompilerSchema(child)
		}
		tupleSchema := map[string]any{}
		// Empty positional items is permitted by TypeBox, while prefixItems must be nonempty.
		if len(translated) > 0 {
			tupleSchema["prefixItems"] = translated
		}
		if additional, exists := schema["additionalItems"]; exists {
			tupleSchema["items"] = argumentCompilerSchema(additional)
		}
		allOf, _ := schema["allOf"].([]any)
		schema["allOf"] = append(allOf, tupleSchema)
		delete(schema, "items")
		delete(schema, "additionalItems")
	} else if item, exists := schema["items"]; exists {
		schema["items"] = argumentCompilerSchema(item)
	}
	if dependencies, ok := schema["dependencies"].(map[string]any); ok {
		allOf, _ := schema["allOf"].([]any)
		for property, child := range dependencies {
			keyword := "dependentSchemas"
			if _, ok := child.([]any); ok {
				keyword = "dependentRequired"
			} else {
				child = argumentCompilerSchema(child)
			}
			allOf = append(allOf, map[string]any{keyword: map[string]any{property: child}})
		}
		if len(allOf) > 0 {
			schema["allOf"] = allOf
		}
		delete(schema, "dependencies")
	}
	return schema
}
