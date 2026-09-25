package agent

import (
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// validateToolArgs validates tool call arguments against the tool's JSON schema.
// Returns nil if validation passes, or a human-readable error message suitable
// for returning to the LLM.
//
// Mirrors upstream validateToolArguments (packages/ai/src/utils/validation.ts:292).
func validateToolArgs(toolName string, schema map[string]any, args json.RawMessage) error {
	if len(schema) == 0 {
		return nil // No schema → skip validation.
	}
	if len(args) == 0 || string(args) == "null" {
		args = []byte("{}")
	}

	// Compile the JSON schema.
	compiled, err := compileSchema(schema)
	if err != nil {
		// Schema compilation failure is a programming error (bad tool definition),
		// not an LLM error. Skip validation rather than blocking execution.
		return nil
	}

	// Unmarshal args into any for validation.
	var instance any
	if err := json.Unmarshal(args, &instance); err != nil {
		return fmt.Errorf("Validation failed for tool %q:\n  - invalid JSON: %w\n\nReceived arguments:\n%s",
			toolName, err, string(args))
	}

	// Validate.
	err = compiled.Validate(instance)
	if err == nil {
		return nil
	}

	return fmt.Errorf("Validation failed for tool %q:\n%s\n\nReceived arguments:\n%s",
		toolName, err.Error(), string(args))
}

// compileSchema compiles a JSON schema from a map[string]any.
func compileSchema(schema map[string]any) (*jsonschema.Schema, error) {
	// Marshal to JSON then unmarshal to get the correct any representation
	// for the jsonschema compiler (it expects parsed JSON, not Go maps
	// with typed values that came from struct fields).
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}

	var schemaDoc any
	if err := json.Unmarshal(schemaBytes, &schemaDoc); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}

	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema.json", schemaDoc); err != nil {
		return nil, fmt.Errorf("add resource: %w", err)
	}
	compiled, err := c.Compile("schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}
	return compiled, nil
}
