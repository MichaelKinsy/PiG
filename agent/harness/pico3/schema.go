package pico3

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

var compiledSchemas sync.Map

func compileToolSchema(declaration *ToolDeclaration) (*jsonschema.Schema, error) {
	if cached, ok := compiledSchemas.Load(declaration); ok {
		return cached.(*jsonschema.Schema), nil
	}
	encoded, err := json.Marshal(declaration.Parameters)
	if err != nil {
		return nil, err
	}
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(string(encoded)))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("tool.json", document); err != nil {
		return nil, err
	}
	schema, err := compiler.Compile("tool.json")
	if err != nil {
		return nil, err
	}
	compiledSchemas.Store(declaration, schema)
	return schema, nil
}

// invalidArguments validates arguments with the tool's schema and returns the
// errors as "path: message" joined by "; ", or "" when valid.
func invalidArguments(declaration *ToolDeclaration, arguments JsonValue) string {
	if len(declaration.Parameters) == 0 {
		return ""
	}
	schema, err := compileToolSchema(declaration)
	if err != nil {
		return err.Error()
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return err.Error()
	}
	instance, err := jsonschema.UnmarshalJSON(strings.NewReader(string(encoded)))
	if err != nil {
		return err.Error()
	}
	err = schema.Validate(instance)
	if err == nil {
		return ""
	}
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) {
		return err.Error()
	}
	var messages []string
	collectLeaves(validation, &messages)
	return strings.Join(messages, "; ")
}

var englishPrinter = message.NewPrinter(language.English)

func collectLeaves(validation *jsonschema.ValidationError, messages *[]string) {
	if len(validation.Causes) > 0 {
		for _, cause := range validation.Causes {
			collectLeaves(cause, messages)
		}
		return
	}
	path := "/" + strings.Join(validation.InstanceLocation, "/")
	*messages = append(*messages, fmt.Sprintf("%s: %s", path, schemaMessage(validation.ErrorKind)))
}

// schemaMessage renders a validation failure in TypeBox's wording.
func schemaMessage(errorKind jsonschema.ErrorKind) string {
	switch typed := errorKind.(type) {
	case *kind.Type:
		return "must be " + strings.Join(typed.Want, " or ")
	case *kind.Required:
		return "must have required properties " + strings.Join(typed.Missing, ", ")
	case *kind.AdditionalProperties:
		return "must not have additional properties"
	default:
		return errorKind.LocalizedString(englishPrinter)
	}
}
