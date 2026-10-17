package ai

// Ports the strict check durable/src/harness/tool.ts:structuredOutputError runs with TypeBox: Compile(schema).Check(value)
// without conversion or coercion, and the first of validator.Errors(value).

import (
	"encoding/json"
	"strings"
)

// CheckSchemaValue checks value, a JSON text, against the JSON schema as TypeBox's validator does, with no conversion and no
// coercion. A value that matches returns ok. Otherwise it returns the instance path of the first error, dotted with the leading
// slash removed and "root" for the whole value, and its message.
func CheckSchemaValue(schema map[string]any, value json.RawMessage) (path, message string, ok bool, err error) {
	if schema == nil {
		schema = map[string]any{}
	}
	//portlint:allow mapkeyorder tool.ts:567-571 Compile(schema).Errors(value) walks properties in the schema's order, but the caller's durable ToolRegistration.StructuredOutputSchema is a Go map with no recorded member order (as at describe.go:65); its in-repo sources are bash.ts's shell schema, whose output PiG produces, and models.ts's Type.Unknown() {}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return "", "", false, err
	}
	entry := toolArgumentSchemas.entry(encoded)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	parsed, err := entry.parsed()
	if err != nil {
		return "", "", false, err
	}
	instance, err := decodeSchemaInstance(value)
	if err != nil {
		return "", "", false, err
	}
	compiled, err := parsed.validator()
	if err != nil {
		return "", "", false, err
	}
	if compiled.Validate(instance) == nil {
		return "", "", true, nil
	}
	sourceOrder, _ := readSchemaObjectOrder(value)
	failures := parsed.argumentErrorsAt(sourceOrder, instance, "", "")
	if len(failures) == 0 {
		return "root", "invalid", false, nil
	}
	location, message, _ := strings.Cut(strings.TrimPrefix(failures[0], "  - "), ": ")
	// A missing required member is reported at its object's instance path, not at the member.
	if names, found := strings.CutPrefix(message, "must have required properties "); found {
		first, _, _ := strings.Cut(names, ", ")
		if location == first {
			location = "root"
		} else if parent, found := strings.CutSuffix(location, "."+first); found {
			location = parent
		}
	}
	return location, message, false, nil
}
