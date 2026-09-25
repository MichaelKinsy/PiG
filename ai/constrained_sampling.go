package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// This file ports packages/ai/src/api/constrained-sampling.ts: the provider-side
// constrained tool sampling helpers (JSON-schema strict mode and Lark/regex
// grammar tools). The functions are consumed by the OpenAI completions and
// responses request builders and stream parsers.

// grammarConstrainedSampling is a resolved grammar request for one tool.
// Mirrors upstream GrammarConstrainedSampling.
type grammarConstrainedSampling struct {
	Format        string // "lark" | "regex"
	Definition    string
	InputProperty string
}

// grammarToolInputJSONBuffer accumulates a grammar tool's streamed string input
// and reconstructs it as monotonic JSON-argument deltas. Mirrors upstream
// GrammarToolInputJsonBuffer.
type grammarToolInputJSONBuffer struct {
	Input   string
	Started bool
	Closed  bool
}

// jsonStringJS encodes s the way JavaScript's JSON.stringify does: without Go's
// default HTML escaping of <, >, &: so reconstructed grammar deltas are
// byte-faithful to upstream.
func jsonStringJS(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Encode never fails for a string and appends a trailing newline.
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}

// getGrammarToolInput extracts the string input argument for a grammar tool
// call. Mirrors upstream getGrammarToolInput.
func getGrammarToolInput(toolName string, arguments map[string]any, inputProperty string) (string, error) {
	input, ok := arguments[inputProperty].(string)
	if !ok {
		return "", fmt.Errorf("Grammar tool call %q requires argument %q to be a string.", toolName, inputProperty)
	}
	return input, nil
}

// appendGrammarToolInputJSONDelta appends the next streamed grammar input to the
// buffer and returns the JSON-argument delta to emit (ok=false means "no delta",
// upstream's undefined). Mirrors upstream appendGrammarToolInputJsonDelta.
func appendGrammarToolInputJSONDelta(buffer *grammarToolInputJSONBuffer, inputProperty, nextInput string, closeBuf bool) (string, bool, error) {
	if buffer.Closed {
		if closeBuf && nextInput == buffer.Input {
			return "", false, nil
		}
		return "", false, fmt.Errorf("grammar tool input for property %q changed after it was closed", inputProperty)
	}
	if !strings.HasPrefix(nextInput, buffer.Input) {
		return "", false, fmt.Errorf("grammar tool input for property %q changed non-monotonically", inputProperty)
	}

	inputDelta := nextInput[len(buffer.Input):]
	if !closeBuf && inputDelta == "" {
		return "", false, nil
	}

	var delta strings.Builder
	if !buffer.Started {
		delta.WriteString("{")
		delta.WriteString(jsonStringJS(inputProperty))
		delta.WriteString(`:"`)
		buffer.Started = true
	}
	// JSON.stringify(inputDelta).slice(1, -1): the escaped body without quotes.
	encoded := jsonStringJS(inputDelta)
	delta.WriteString(encoded[1 : len(encoded)-1])
	buffer.Input = nextInput

	if closeBuf {
		delta.WriteString(`"}`)
		buffer.Closed = true
	}
	return delta.String(), true, nil
}

// inferGrammarInputProperty resolves the single required string property that
// carries a grammar tool's input. Mirrors upstream inferGrammarInputProperty.
func inferGrammarInputProperty(tool ToolSchema) (string, error) {
	if t, _ := tool.Parameters["type"].(string); t != "object" {
		return "", fmt.Errorf("grammar constrained sampling requires an object parameter schema")
	}
	required := toStringSlice(tool.Parameters["required"])
	if len(required) != 1 {
		return "", fmt.Errorf("grammar constrained sampling requires exactly one required string property")
	}
	inputProperty := required[0]

	properties, _ := tool.Parameters["properties"].(map[string]any)
	prop, ok := properties[inputProperty].(map[string]any)
	if !ok {
		return "", fmt.Errorf("grammar constrained sampling requires a properties entry for %s", inputProperty)
	}
	if pt, _ := prop["type"].(string); pt != "string" {
		return "", fmt.Errorf("grammar constrained sampling property %s must have type string", inputProperty)
	}
	return inputProperty, nil
}

// toStringSlice returns v as a []string when it is a JSON array of strings
// ([]any of strings or []string); otherwise nil. Upstream requires
// Array.isArray && every element a string; a non-array or mixed array yields an
// empty result that fails the length check.
func toStringSlice(v any) []string {
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, e := range arr {
			s, ok := e.(string)
			if !ok {
				return nil
			}
			out = append(out, s)
		}
		return out
	default:
		return nil
	}
}

// unsupportedStrictJSONSchemaError identifies schemas that cannot be converted
// without changing their accepted language.
type unsupportedStrictJSONSchemaError struct {
	reason string
}

func (err unsupportedStrictJSONSchemaError) Error() string { return err.reason }

var unsupportedStrictSchemaKeys = []string{
	"$ref", "$defs", "definitions", "allOf", "oneOf", "patternProperties",
	"dependentSchemas", "dependencies", "unevaluatedProperties", "propertyNames",
	"contains", "prefixItems", "not", "if", "then", "else",
}

func strictSchemaError(reason string) error {
	return unsupportedStrictJSONSchemaError{reason: reason}
}

func isStructuredJSONSchema(schema any) bool {
	object, ok := schema.(map[string]any)
	if !ok {
		return false
	}
	types := toStringSlice(object["type"])
	if schemaType, ok := object["type"].(string); ok {
		types = []string{schemaType}
	}
	return slices.Contains(types, "object") || slices.Contains(types, "array") || object["properties"] != nil || object["items"] != nil
}

func jsonSchemaAllowsNull(schema any) bool {
	object, ok := schema.(map[string]any)
	if !ok {
		return false
	}
	if object["type"] == "null" || slices.Contains(toStringSlice(object["type"]), "null") || object["const"] == nil && hasJSONSchemaKey(object, "const") {
		return true
	}
	if enum, ok := object["enum"].([]any); ok && slices.Contains(enum, any(nil)) {
		return true
	}
	if variants, ok := object["anyOf"].([]any); ok {
		return slices.ContainsFunc(variants, jsonSchemaAllowsNull)
	}
	return false
}

func hasJSONSchemaKey(schema map[string]any, key string) bool {
	_, ok := schema[key]
	return ok
}

func makeJSONSchemaNodeStrict(value any) error {
	schema, ok := value.(map[string]any)
	if !ok {
		return strictSchemaError("boolean schemas are unsupported")
	}
	for _, key := range unsupportedStrictSchemaKeys {
		if hasJSONSchemaKey(schema, key) {
			return strictSchemaError(key + " schemas are unsupported")
		}
	}

	if variantsValue, exists := schema["anyOf"]; exists {
		variants, ok := variantsValue.([]any)
		if !ok || len(variants) == 0 {
			return strictSchemaError("anyOf must contain at least one schema")
		}
		for _, variant := range variants {
			if isStructuredJSONSchema(variant) {
				return strictSchemaError("object and array unions are unsupported")
			}
			if err := makeJSONSchemaNodeStrict(variant); err != nil {
				return err
			}
		}
	}

	if items, exists := schema["items"]; exists {
		if _, tuple := items.([]any); tuple {
			return strictSchemaError("tuple schemas are unsupported")
		}
		if err := makeJSONSchemaNodeStrict(items); err != nil {
			return err
		}
	}

	isObject := schema["type"] == "object"
	if _, exists := schema["properties"]; exists && !isObject {
		return strictSchemaError("properties require type object")
	}
	if !isObject {
		return nil
	}
	if additional, exists := schema["additionalProperties"]; exists && additional != false {
		return strictSchemaError("schema-valued or true additionalProperties is unsupported")
	}
	properties := map[string]any{}
	if value, exists := schema["properties"]; exists {
		var ok bool
		properties, ok = value.(map[string]any)
		if !ok {
			return strictSchemaError("object properties must be a schema map")
		}
	}
	required := toStringSlice(schema["required"])
	if _, exists := schema["required"]; exists && required == nil {
		return strictSchemaError("object required must be a string array")
	}
	if required == nil {
		required = []string{}
	}
	for _, name := range required {
		if _, exists := properties[name]; !exists {
			return strictSchemaError("required contains an unknown property")
		}
	}

	propertyNames := slices.Sorted(maps.Keys(properties))
	if propertyNames == nil {
		propertyNames = []string{}
	}
	for _, name := range propertyNames {
		property := properties[name]
		if err := makeJSONSchemaNodeStrict(property); err != nil {
			return err
		}
		if !slices.Contains(required, name) && !jsonSchemaAllowsNull(property) {
			properties[name] = map[string]any{"anyOf": []any{property, map[string]any{"type": "null"}}}
		}
	}
	schema["required"] = propertyNames
	schema["additionalProperties"] = false
	return nil
}

// makeStrictJSONSchema converts a tool schema to the strict subset accepted by
// provider constrained sampling without mutating the authored schema.
func makeStrictJSONSchema(parameters map[string]any) (map[string]any, error) {
	body, err := json.Marshal(parameters)
	if err != nil {
		return nil, err
	}
	var cloned map[string]any
	if err := json.Unmarshal(body, &cloned); err != nil {
		return nil, err
	}
	if err := makeJSONSchemaNodeStrict(cloned); err != nil {
		return nil, err
	}
	if cloned["type"] != "object" {
		return nil, strictSchemaError("root schema must have type object")
	}
	return cloned, nil
}

func getJSONSchemaToolParameters(tool ToolSchema, strict *bool) (map[string]any, error) {
	if strict != nil && *strict {
		return makeStrictJSONSchema(tool.Parameters)
	}
	return tool.Parameters, nil
}

// resolveJSONSchemaStrictSampling returns a non-nil *true when a json_schema
// constrained tool should request strict mode, nil when it should not, and an
// error when strict is required but unsupported. Mirrors upstream
// resolveJsonSchemaStrictSampling.
func resolveJSONSchemaStrictSampling(tool ToolSchema, supportsStrictMode bool) (*bool, error) {
	config := tool.ConstrainedSampling
	if config == nil || config.Type != "json_schema" {
		return nil, nil
	}
	if supportsStrictMode {
		if _, err := makeStrictJSONSchema(tool.Parameters); err != nil {
			var unsupported unsupportedStrictJSONSchemaError
			if !errors.As(err, &unsupported) {
				return nil, err
			}
			if config.Strict != "require" {
				return nil, nil
			}
			return nil, fmt.Errorf("Tool %q requires JSON-schema constrained sampling, but %s.", tool.Name, unsupported.reason)
		}
		t := true
		return &t, nil
	}
	if config.Strict == "require" {
		return nil, fmt.Errorf("Tool %q requires JSON-schema constrained sampling, but strict tools are unsupported.", tool.Name)
	}
	return nil, nil
}

// resolveGrammarConstrainedSampling resolves a grammar tool's request, or nil
// when the tool is not a grammar tool or grammar tools are unsupported. Mirrors
// upstream resolveGrammarConstrainedSampling.
func resolveGrammarConstrainedSampling(tool ToolSchema, supportsOpenAIGrammarTools bool) (*grammarConstrainedSampling, error) {
	config := tool.ConstrainedSampling
	if config == nil || config.Type != "grammar" {
		return nil, nil
	}
	if !supportsOpenAIGrammarTools {
		return nil, nil
	}

	lark := config.Variants[GrammarFormatOpenAILark]
	regex := config.Variants[GrammarFormatOpenAIRegex]
	hasLark := strings.TrimSpace(lark) != ""
	hasRegex := strings.TrimSpace(regex) != ""
	if !hasLark && !hasRegex {
		return nil, fmt.Errorf("Tool %q cannot use grammar constrained sampling: no supported grammar variant was provided.", tool.Name)
	}

	inputProperty, err := inferGrammarInputProperty(tool)
	if err != nil {
		return nil, fmt.Errorf("Tool %q cannot use grammar constrained sampling: %s.", tool.Name, err.Error())
	}
	format, definition := "regex", regex
	if hasLark {
		format, definition = "lark", lark
	}
	return &grammarConstrainedSampling{Format: format, Definition: definition, InputProperty: inputProperty}, nil
}

// createGrammarToolInputProperties maps each grammar tool name to its input
// property. Mirrors upstream createGrammarToolInputProperties.
func createGrammarToolInputProperties(tools []ToolSchema, supportsOpenAIGrammarTools bool) (map[string]string, error) {
	properties := map[string]string{}
	for _, tool := range tools {
		grammar, err := resolveGrammarConstrainedSampling(tool, supportsOpenAIGrammarTools)
		if err != nil {
			return nil, err
		}
		if grammar != nil {
			properties[tool.Name] = grammar.InputProperty
		}
	}
	return properties, nil
}
