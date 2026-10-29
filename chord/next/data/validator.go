package data

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"runtime"
	"strings"
	"sync"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/typebox"
	"github.com/MichaelKinsy/PiG/telemetry"
	"github.com/MichaelKinsy/PiG/third_party/jsonschema"
)

// Ports packages/chord/src/next/data/validator.ts: compiled checks (TypeBox validators of Chord-owned schemas) and the
// `invalid` error of a failed check.
//
// Go forced differences:
//   - Upstream's `Validator` (typebox/compile) is a *Validator holding a compiled jsonschema over internal/typebox's
//     schema data. TypeBox compiles a `bytes()` leaf's `~refine` check; JSON Schema has no byte type, so the
//     validator compiles a rewritten schema in which each `{type: "bytes", minLength?, maxLength?}` marker is an
//     object `{"\x00chord:bytes": <length>}` bounded by the marker's lengths, and a `[]byte` in a checked value is
//     rewritten to that object. A value object's own key that could be mistaken for the marker key is escaped
//     first, so no data forges a byte leaf.
//   - Upstream's WeakMap keyed by the owner is a cache keyed by weak pointer, evicted when the owner is collected.

// SchemaOwner is a Chord-owned record holding a schema: a snapshot, or a contract's params.
type SchemaOwner interface {
	*contracts.Snapshot | *contracts.ParamsData
}

// bytesKey is the member that stands for the length of a byte leaf in a rewritten schema and value.
const bytesKey = "\x00chord:bytes"

// bytesProblem is TypeBox's error message for a failed bytes check (the `~refine` error of schema.ts bytes()).
const bytesProblem = "expected bytes of the declared length"

// Validator is a compiled check of one schema.
type Validator struct {
	schema    any
	rewritten any
	compiled  *jsonschema.Schema
}

// Type is the schema the validator checks.
func (validator *Validator) Type() any { return validator.schema }

// Check reports whether value matches the schema.
func (validator *Validator) Check(value any) bool {
	return validator.compiled.Validate(rewriteValue(value, true)) == nil
}

var validators struct {
	sync.Mutex
	byOwner map[any]*Validator
}

// ValidatorFor is the validator of an owner's schema, compiled on first use (counted) and cached weakly by the owner:
// the owner's collection evicts it. A schema TypeBox cannot compile is an error.
func ValidatorFor[O SchemaOwner](owner O, ctx context.Context) (*Validator, error) {
	switch typed := any(owner).(type) {
	case *contracts.Snapshot:
		return validatorFor(typed, typed.Schema, ctx)
	case *contracts.ParamsData:
		return validatorFor(typed, typed.Schema, ctx)
	}
	panic("unreachable: SchemaOwner has two types")
}

func validatorFor[T any](owner *T, schema contracts.TSchema, ctx context.Context) (*Validator, error) {
	key := weak.Make(owner)
	validators.Lock()
	defer validators.Unlock()
	if validator, ok := validators.byOwner[key]; ok {
		return validator, nil
	}
	chordctx.Telemetry(ctx).Metrics().Counter("chord.data.compilations", telemetry.InstrumentOptions{}).Add(1, nil)
	validator, err := compile(schema.Node)
	if err != nil {
		return nil, err
	}
	if validators.byOwner == nil {
		validators.byOwner = map[any]*Validator{}
	}
	validators.byOwner[key] = validator
	runtime.AddCleanup(owner, func(key weak.Pointer[T]) {
		validators.Lock()
		delete(validators.byOwner, key)
		validators.Unlock()
	}, key)
	return validator, nil
}

// compile is TypeBox's Compile over internal/typebox's schema data.
func compile(schema any) (*Validator, error) {
	rewritten, _ := rewriteSchema(schema)
	text, err := jsstring.MarshalJSON(typebox.Plain(rewritten))
	if err != nil {
		return nil, err
	}
	var document any
	//portlint:allow jsonclone chord validator.ts Compile(schema): the text gives the compiler strict JSON data (numbers as float64, members decoded), once per owner
	if err := json.Unmarshal(text, &document); err != nil { //perf:ok compile runs once per schema owner
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	typebox.RegisterFormats(compiler)
	if err := compiler.AddResource("schema.json", typebox.CompilerSchema(document)); err != nil {
		return nil, err
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		return nil, err
	}
	return &Validator{schema: schema, rewritten: rewritten, compiled: compiled}, nil
}

// rewriteSchema replaces every bytes marker inside schema with the object schema that stands for it, walking the
// schema keywords that hold schemas. It returns schema itself when it holds no marker.
func rewriteSchema(schema any) (any, bool) {
	switch typed := schema.(type) {
	case *chordjson.Object:
		if typed == nil {
			return schema, false
		}
		if name, _ := typed.Get("type"); name == "bytes" {
			return bytesMarker(func(key string) (any, bool) { return typed.Get(key) }), true
		}
		var changes []schemaChange
		for key, child := range typed.All() {
			if next, changed := rewriteKeyword(key, child); changed {
				changes = append(changes, schemaChange{key, next})
			}
		}
		if len(changes) == 0 {
			return schema, false
		}
		out := chordjson.NewObject(typed.Len())
		for key, child := range typed.All() {
			out.Set(key, child)
		}
		for _, change := range changes {
			out.Set(change.key, change.value)
		}
		return out, true
	case map[string]any:
		if name := typed["type"]; name == "bytes" {
			return bytesMarker(func(key string) (any, bool) { value, ok := typed[key]; return value, ok }), true
		}
		var out map[string]any
		for key, child := range typed {
			if next, changed := rewriteKeyword(key, child); changed {
				if out == nil {
					out = make(map[string]any, len(typed))
					maps.Copy(out, typed)
				}
				out[key] = next
			}
		}
		if out == nil {
			return schema, false
		}
		return out, true
	}
	return schema, false
}

type schemaChange struct {
	key   string
	value any
}

// rewriteKeyword rewrites the schemas a keyword's value holds.
func rewriteKeyword(keyword string, value any) (any, bool) {
	switch keyword {
	case "additionalProperties", "additionalItems", "not", "contains", "propertyNames", "if", "then", "else",
		"unevaluatedProperties", "unevaluatedItems":
		return rewriteSchema(value)
	case "anyOf", "allOf", "oneOf", "prefixItems":
		return rewriteList(value)
	case "items":
		if _, isList := value.([]any); isList {
			return rewriteList(value)
		}
		return rewriteSchema(value)
	case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
		return rewriteMap(value)
	}
	return value, false
}

func rewriteList(value any) (any, bool) {
	items, ok := value.([]any)
	if !ok {
		return value, false
	}
	var out []any
	for index, item := range items {
		if next, changed := rewriteSchema(item); changed {
			if out == nil {
				out = append([]any(nil), items...)
			}
			out[index] = next
		}
	}
	if out == nil {
		return value, false
	}
	return out, true
}

func rewriteMap(value any) (any, bool) {
	switch typed := value.(type) {
	case *chordjson.Object:
		if typed == nil {
			return value, false
		}
		var out *chordjson.Object
		for key, child := range typed.All() {
			if next, changed := rewriteSchema(child); changed {
				if out == nil {
					out = chordjson.NewObject(typed.Len())
					for k, v := range typed.All() {
						out.Set(k, v)
					}
				}
				out.Set(key, next)
			}
		}
		if out == nil {
			return value, false
		}
		return out, true
	case map[string]any:
		var out map[string]any
		for key, child := range typed {
			if next, changed := rewriteSchema(child); changed {
				if out == nil {
					out = make(map[string]any, len(typed))
					maps.Copy(out, typed)
				}
				out[key] = next
			}
		}
		if out == nil {
			return value, false
		}
		return out, true
	}
	return value, false
}

// bytesMarker is the object schema standing for a bytes marker: a `type` that is a list (no Chord schema has one),
// so a failure of the type is recognised, and the length bounded as the marker bounds it.
func bytesMarker(get func(string) (any, bool)) any {
	length := chordjson.ObjectOf("type", "integer")
	if min, ok := get("minLength"); ok {
		length.Set("minimum", min)
	}
	if max, ok := get("maxLength"); ok {
		length.Set("maximum", max)
	}
	return chordjson.ObjectOf(
		"type", []any{"object"},
		"required", []any{bytesKey},
		"properties", chordjson.ObjectOf(bytesKey, length),
	)
}

// rewriteValue prepares a checked value: a []byte becomes its length object and an object key that could stand for
// the marker key is escaped. plain gives the form the validator reads (maps, float64); otherwise containers keep
// their kind and order for the error walk.
func rewriteValue(value any, plain bool) any {
	switch typed := value.(type) {
	case []byte:
		if plain {
			return map[string]any{bytesKey: float64(len(typed))}
		}
		return chordjson.ObjectOf(bytesKey, float64(len(typed)))
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = rewriteValue(item, plain)
		}
		return out
	case *chordjson.Object:
		if typed == nil {
			return nil
		}
		if plain {
			out := make(map[string]any, typed.Len())
			for key, item := range typed.All() {
				out[escapeKey(key)] = rewriteValue(item, plain)
			}
			return out
		}
		out := chordjson.NewObject(typed.Len())
		for key, item := range typed.All() {
			out.Set(escapeKey(key), rewriteValue(item, plain))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[escapeKey(key)] = rewriteValue(item, plain)
		}
		return out
	}
	return typebox.Plain(value)
}

// escapeKey makes a data key that is the marker key, or the marker key and NULs, longer by one NUL, so no data key is
// the marker key.
func escapeKey(key string) string {
	if rest, ok := strings.CutPrefix(key, bytesKey); ok && strings.Trim(rest, "\x00") == "" {
		return key + "\x00"
	}
	return key
}

// Check checks value (counted); a failure returns `invalid/<reason>` with the first error's JSON Pointer path, as
// upstream throws it. reason is ReasonArgs, ReasonParams or ReasonHostAddress.
func Check(validator *Validator, value any, reason ChordErrorReason, ctx context.Context) error {
	chordctx.Telemetry(ctx).Metrics().Counter("chord.data.validations", telemetry.InstrumentOptions{}).Add(1, nil)
	if !validator.Check(value) {
		return failedCheck(validator, value, reason, ctx)
	}
	return nil
}

// failedCheck is the error of a failed check: TypeBox's first error from its diagnostic walk (counted; it builds the
// whole list). TypeBox does not escape `~` and `/` in its instance paths, so keys containing them make the path
// ambiguous.
func failedCheck(validator *Validator, value any, reason ChordErrorReason, ctx context.Context) *ChordError {
	chordctx.Telemetry(ctx).Metrics().Counter("chord.data.walks", telemetry.InstrumentOptions{}).Add(1, nil)
	path, problem := "/", "does not match the schema"
	if errs := typebox.Errors(validator.rewritten, rewriteValue(value, false)); len(errs) > 0 {
		path, problem = describe(errs[0])
	}
	suffix := path
	if path == "/" {
		suffix = ""
	}
	return Invalid(reason, fmt.Sprintf("%s%s: %s", reason, suffix, problem), delta.JsonObjectOf("path", path))
}

// describe is the path and problem of a TypeBox error: TypeBox reports a missing field at its parent, and an unknown
// one as a false schema (`additionalProperties: false`) at its own path. A failure inside a rewritten bytes leaf is the
// bytes check's. Anything else keeps TypeBox's message.
func describe(err typebox.ValueError) (path, problem string) {
	params := err.ParamsObject()
	switch err.Keyword {
	case "type":
		if names, _ := params.Value("type").([]any); len(names) == 1 && names[0] == "object" {
			return pathOf(err.InstancePath), bytesProblem
		}
	case "required":
		names, _ := params.Value("requiredProperties").([]any)
		if len(names) == 1 && names[0] == bytesKey {
			return pathOf(err.InstancePath), bytesProblem
		}
		if len(names) > 0 {
			first, _ := names[0].(string)
			return err.InstancePath + "/" + first, "missing"
		}
	case "boolean":
		if strings.HasSuffix(err.SchemaPath, "/additionalProperties") {
			return err.InstancePath, "unknown field"
		}
	}
	if parent, ok := strings.CutSuffix(err.InstancePath, "/"+bytesKey); ok {
		return pathOf(parent), bytesProblem
	}
	return pathOf(err.InstancePath), err.Message
}

func pathOf(instancePath string) string {
	if instancePath == "" {
		return "/"
	}
	return instancePath
}
