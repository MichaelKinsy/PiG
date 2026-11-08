package piglet

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/MichaelKinsy/PiG/schema"
)

// pigletSchemaJSON is the published closed Piglet JSON Schema v1. It mirrors
// the Go parser's accepted vocabulary; Go layers semantic validation (path
// safety, origin resolution, inheritance) on top of it.
//
//go:embed piglet.schema.json
var pigletSchemaJSON []byte

var (
	compiledSchema *schema.Validator
	compileOnce    sync.Once
	compileErr     error
)

// SchemaJSON returns the published Piglet JSON Schema v1 bytes.
func SchemaJSON() []byte { return pigletSchemaJSON }

func pigletSchema() (*schema.Validator, error) {
	compileOnce.Do(func() {
		var document any
		if err := json.Unmarshal(pigletSchemaJSON, &document); err != nil {
			compileErr = fmt.Errorf("decode Piglet schema: %w", err)
			return
		}
		if compiledSchema, compileErr = schema.Compile(document, schema.CompileOptions{}); compileErr != nil {
			compileErr = fmt.Errorf("compile Piglet schema: %w", compileErr)
		}
	})
	return compiledSchema, compileErr
}

// ValidateAgainstSchema checks Piglet YAML against the published JSON Schema
// closed vocabulary. It is the schema half of the schema/parser agreement; Go
// parsing (ParseBytes) adds the semantic checks the vocabulary schema cannot
// express.
func ValidateAgainstSchema(yamlBytes []byte) error {
	validator, err := pigletSchema()
	if err != nil {
		return err
	}
	value, err := schemaInstance(yamlBytes)
	if err != nil {
		return err
	}
	problems, err := validator.Errors(value)
	if err != nil || len(problems) == 0 {
		return err
	}
	messages := make([]string, len(problems))
	for i, problem := range problems {
		// The root of the document has the empty JSON pointer; "/" names it in the message.
		path := problem.Path
		if path == "" {
			path = "/"
		}
		messages[i] = path + ": " + problem.Message
	}
	return fmt.Errorf("piglet does not match its schema: %s", strings.Join(messages, "; "))
}

// schemaInstance decodes YAML and normalizes it to JSON-native types
// (map[string]any, []any, float64) so the JSON Schema validator sees the same
// value shape it was authored against.
func schemaInstance(yamlBytes []byte) (any, error) {
	var document any
	if err := yaml.Unmarshal(yamlBytes, &document); err != nil {
		return nil, fmt.Errorf("parse piglet YAML: %w", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("normalize piglet YAML: %w", err)
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return nil, fmt.Errorf("normalize piglet YAML: %w", err)
	}
	return value, nil
}
