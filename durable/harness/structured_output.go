// Ports packages/durable/src/harness/tool.ts (structuredValidators, structuredOutputError).

package harness

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"weak"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
	"github.com/MichaelKinsy/PiG/schema/formats"
)

// structuredValidators holds the compiled structuredOutputSchema of each tool (upstream's WeakMap keyed by the schema
// object). A Go map is not a weak key, so the key is the registration, and the entry names the schema it compiled, so a
// registration given another schema compiles again; the entry goes with the registration.
var structuredValidators = struct {
	sync.Mutex
	byTool map[weak.Pointer[durable.ToolRegistration]]compiledStructuredSchema
}{byTool: map[weak.Pointer[durable.ToolRegistration]]compiledStructuredSchema{}}

type compiledStructuredSchema struct {
	source    uintptr
	validator *schema.Validator
}

// structuredValidator is the validator of the tool's structuredOutputSchema: pi-schema with the standard formats, where a
// reference that names nothing rejects the value it applies to.
func structuredValidator(tool *durable.ToolRegistration) (*schema.Validator, error) {
	source := reflect.ValueOf(tool.StructuredOutputSchema).Pointer()
	key := weak.Make(tool)
	structuredValidators.Lock()
	defer structuredValidators.Unlock()
	if compiled, found := structuredValidators.byTool[key]; found && compiled.source == source {
		return compiled.validator, nil
	}
	validator, err := schema.Compile(tool.StructuredOutputSchema, schema.CompileOptions{Formats: formats.Formats(), UnresolvedRefs: schema.UnresolvedRefsReject})
	if err != nil {
		return nil, err
	}
	if _, found := structuredValidators.byTool[key]; !found {
		runtime.AddCleanup(tool, func(key weak.Pointer[durable.ToolRegistration]) {
			structuredValidators.Lock()
			defer structuredValidators.Unlock()
			delete(structuredValidators.byTool, key)
		}, key)
	}
	structuredValidators.byTool[key] = compiledStructuredSchema{source: source, validator: validator}
	return validator, nil
}

// StructuredOutputError is why a result's structuredOutput breaks the tool's contract, or "" when it keeps it
// (tool.ts structuredOutputError): pi-schema's check of the schema, without conversion or coercion. The path is the first
// error's JSON Pointer with its escapes undone and its segments joined by dots, "root" for the value itself.
func StructuredOutputError(tool *durable.ToolRegistration, result durable.ToolExecutionResult) string {
	if tool.StructuredOutputSchema == nil {
		if !result.HasStructuredOutput {
			return ""
		}
		return fmt.Sprintf("Tool %s returned structuredOutput but declares no structuredOutputSchema", tool.Name)
	}
	if !result.HasStructuredOutput {
		if result.IsError != nil && *result.IsError {
			return ""
		}
		return fmt.Sprintf("Tool %s returned no structuredOutput", tool.Name)
	}
	mismatch := func(path, message string) string {
		return fmt.Sprintf("Tool %s returned structuredOutput that does not match its schema: %s: %s", tool.Name, path, message)
	}
	// The instance is the value as a JSON tree that keeps the members' order, so the first error is the one upstream reports.
	value, err := chordjson.Copy(result.StructuredOutput)
	if err != nil {
		return mismatch("root", err.Error())
	}
	validator, err := structuredValidator(tool)
	if err != nil {
		return mismatch("root", err.Error())
	}
	valid, err := validator.Check(value)
	if err != nil {
		return mismatch("root", err.Error())
	}
	if valid {
		return ""
	}
	failures, err := validator.Errors(value)
	if err != nil {
		return mismatch("root", err.Error())
	}
	if len(failures) == 0 {
		return mismatch("root", "invalid")
	}
	return mismatch(structuredPath(failures[0].Path), failures[0].Message)
}

// structuredPath is a JSON Pointer as the dotted path structuredOutputError names: "" and a pointer of one empty segment are "root".
func structuredPath(pointer string) string {
	if pointer == "" {
		return "root"
	}
	segments := strings.Split(pointer[1:], "/")
	for i, segment := range segments {
		segments[i] = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
	}
	if path := strings.Join(segments, "."); path != "" {
		return path
	}
	return "root"
}
