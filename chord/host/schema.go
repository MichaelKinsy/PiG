package host

// Ports packages/chord/src/schema.ts.

import (
	"math"
	"reflect"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/typebox"
)

// Schema is JSON Schema as data, as TypeBox builds it (schema.ts Schema). Nested schemas are map[string]any or Schema.
type Schema = map[string]any

func asSchema(schema any) Schema {
	switch typed := schema.(type) {
	case map[string]any:
		return typed
	case bool:
		if !typed {
			return Schema{"not": Schema{}}
		}
	}
	return Schema{}
}

// compareUTF16 orders strings as JavaScript's default sort does: by UTF-16 code units.
func compareUTF16(left, right string) int {
	a, b := utf16.Encode([]rune(left)), utf16.Encode([]rune(right))
	return slices.Compare(a, b)
}

// objectEntries reads a JSON object of either Go kind with its keys in JavaScript enumeration order; ok is false for a
// non-object.
func objectEntries(value any) (keys []string, get func(string) any, ok bool) {
	switch typed := value.(type) {
	case *chordjson.Object:
		if typed == nil {
			return nil, nil, false
		}
		return typed.Keys(), typed.Value, true
	case map[string]any:
		keys = make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		chordjson.SortOwnKeys(keys)
		return keys, func(key string) any { return typed[key] }, true
	}
	return nil, nil, false
}

func quoteJSON(text string) string { return string(jsstring.AppendString(nil, text)) }

// absent marks a member JavaScript would hold as undefined; canonicalJson drops it.
type absent struct{}

// CanonicalJSON is schema.ts canonicalJson: sorted object keys, undefined members dropped.
func CanonicalJSON(value any) string {
	var out strings.Builder
	writeCanonical(&out, value)
	return out.String()
}

func writeCanonical(out *strings.Builder, value any) {
	switch typed := value.(type) {
	case nil, absent:
		out.WriteString("null")
	case bool:
		if typed {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case string:
		out.WriteString(quoteJSON(typed))
	case []any:
		out.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				out.WriteByte(',')
			}
			writeCanonical(out, item)
		}
		out.WriteByte(']')
	case jsonMarshaler:
		writeCanonical(out, typed.jsonValue())
	default:
		if number, ok := toFloat(value); ok {
			if math.IsNaN(number) || math.IsInf(number, 0) {
				out.WriteString("null")
				return
			}
			out.WriteString(string(jsnumber.JSON(number)))
			return
		}
		if list := reflect.ValueOf(value); list.Kind() == reflect.Slice {
			items := make([]any, list.Len())
			for index := range items {
				items[index] = list.Index(index).Interface()
			}
			writeCanonical(out, items)
			return
		}
		keys, get, ok := objectEntries(value)
		if !ok {
			out.WriteString("null")
			return
		}
		var present []string
		for _, key := range keys {
			if _, missing := get(key).(absent); !missing {
				present = append(present, key)
			}
		}
		slices.SortFunc(present, compareUTF16)
		out.WriteByte('{')
		for index, key := range present {
			if index > 0 {
				out.WriteByte(',')
			}
			out.WriteString(quoteJSON(key))
			out.WriteByte(':')
			writeCanonical(out, get(key))
		}
		out.WriteByte('}')
	}
}

// jsonMarshaler is a Chord value with a JSON form (a contract description) that canonicalJson writes.
type jsonMarshaler interface{ jsonValue() any }

func toFloat(value any) (float64, bool) {
	number, ok := typebox.Plain(value).(float64)
	return number, ok
}

// Hash64 is schema.ts hash64: a non-cryptographic 64-bit hash (two FNV-1a lanes) over UTF-16 code units, as 16 hex
// digits.
func Hash64(text string) string {
	a := uint32(0x811c9dc5)
	b := uint32(0x01000193 ^ 0x5bd1e995)
	for _, code := range utf16.Encode([]rune(text)) {
		a = (a ^ uint32(code)) * 0x01000193
		b = ((b ^ uint32(code)) * 0x5bd1e995) ^ (b >> 13)
	}
	const digits = "0123456789abcdef"
	var out [16]byte
	for index := range 8 {
		out[index] = digits[(a>>(28-4*index))&0xf]
		out[8+index] = digits[(b>>(28-4*index))&0xf]
	}
	return string(out[:])
}

// DeepEqual is schema.ts deepEqual over canonical JSON.
func DeepEqual(a, b any) bool { return CanonicalJSON(a) == CanonicalJSON(b) }

// assertValid is schema.ts assertValid: it returns invalid (with reason) and the first three schema errors when value
// does not match.
func assertValid(reason, what string, schema, value any) error {
	if typebox.Check(schema, value) {
		return nil
	}
	errors := typebox.Errors(schema, value)
	if len(errors) > 3 {
		errors = errors[:3]
	}
	parts := make([]string, len(errors))
	details := make([]any, len(errors))
	for index, failure := range errors {
		path := failure.InstancePath
		if path == "" {
			path = "/"
		}
		parts[index] = path + " " + failure.Message
		details[index] = map[string]any{"instancePath": failure.InstancePath, "message": failure.Message}
	}
	return Invalid(reason, what+": "+strings.Join(parts, "; "), details)
}

// Normalize is schema.ts normalize: it applies defaults, strips unknown fields and validates, returning a fresh value.
// A nil value is normalized as an empty object (value ?? {}).
func Normalize(schema Schema, value any, what string) (any, error) {
	if value == nil {
		value = map[string]any{}
	}
	normalized := typebox.Clean(schema, typebox.Default(schema, typebox.Clone(value)))
	if err := assertValid("input", what, schema, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

// CanonicalIdentity is schema.ts canonicalIdentity: the canonical JSON of a normalized value with fields equal to their
// schema default omitted.
func CanonicalIdentity(schema Schema, value any) string {
	return CanonicalJSON(stripDefaults(asSchema(schema), value))
}

func stripDefaults(schema Schema, value any) any {
	if items, ok := value.([]any); ok {
		out := make([]any, len(items))
		if positional, tuple := schema["items"].([]any); tuple {
			for index, item := range items {
				var itemSchema any
				if index < len(positional) {
					itemSchema = positional[index]
				}
				out[index] = stripDefaults(asSchema(itemSchema), item)
			}
			return out
		}
		for index, item := range items {
			out[index] = stripDefaults(asSchema(schema["items"]), item)
		}
		return out
	}
	keys, get, ok := objectEntries(value)
	if !ok {
		return value
	}
	properties := asSchema(schema["properties"])
	result := chordjson.NewObject(len(keys))
	for _, key := range keys {
		item := get(key)
		property := asSchema(properties[key])
		if fallback, has := property["default"]; has && DeepEqual(fallback, item) {
			continue
		}
		result.Set(key, stripDefaults(property, item))
	}
	return result
}

var constraints = []string{
	"minLength", "maxLength", "pattern", "format", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum",
	"multipleOf", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties",
}

func typesOf(schema Schema) ([]string, bool) {
	switch typed := schema["type"].(type) {
	case string:
		return []string{typed}, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out, true
	case []string:
		return typed, true
	}
	return nil, false
}

// known are the keywords fits reasons about. Any other validation keyword in the provider's schema forces exact
// equality.
var known = func() map[string]bool {
	set := map[string]bool{}
	for _, key := range append(slices.Clone(constraints), "type", "properties", "required", "additionalProperties", "patternProperties", "items", "additionalItems", "const", "enum", "anyOf", "parameters", "returnType") {
		set[key] = true
	}
	return set
}()

// annotations have no effect on validity.
var annotations = map[string]bool{
	"title": true, "description": true, "default": true, "examples": true, "$id": true, "$schema": true,
	"$comment": true, "readOnly": true, "writeOnly": true, "deprecated": true,
}

func withoutAnnotations(schema Schema) Schema {
	out := Schema{}
	for key, value := range schema {
		if !annotations[key] {
			out[key] = value
		}
	}
	return out
}

func stringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

func anyList(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []Schema:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = item
		}
		return out, true
	}
	return nil, false
}

// Fits is schema.ts fits: conservative structural subtyping. It is true only if every value valid under sub is valid
// under sup, where object fields missing from sub but optional in sup count as fine. A provider schema using a keyword
// this function does not model, or an additionalProperties schema, only fits an identical consumer schema.
func Fits(subSchema, supSchema any) bool {
	sub := withoutAnnotations(asSchema(subSchema))
	sup := withoutAnnotations(asSchema(supSchema))
	if len(sup) == 0 {
		return true
	}
	if DeepEqual(sub, sup) {
		return true
	}
	for key := range sup {
		if !known[key] {
			return false
		}
	}
	if additional, ok := sup["additionalProperties"]; ok {
		if _, boolean := additional.(bool); !boolean {
			return false
		}
	}
	if branches, ok := anyList(sub["anyOf"]); ok {
		for _, branch := range branches {
			if !Fits(branch, sup) {
				return false
			}
		}
		return true
	}
	if branches, ok := anyList(sup["anyOf"]); ok {
		for _, branch := range branches {
			if Fits(sub, branch) {
				return true
			}
		}
		return false
	}
	if constant, ok := sup["const"]; ok {
		subConstant, has := sub["const"]
		return has && DeepEqual(subConstant, constant)
	}
	if allowed, ok := anyList(sup["enum"]); ok {
		var values []any
		if constant, has := sub["const"]; has {
			values = []any{constant}
		} else if enum, has := anyList(sub["enum"]); has {
			values = enum
		} else {
			return false
		}
		for _, item := range values {
			if !slices.ContainsFunc(allowed, func(candidate any) bool { return DeepEqual(candidate, item) }) {
				return false
			}
		}
		return true
	}
	for _, key := range constraints {
		if wanted, ok := sup[key]; ok && !DeepEqual(valueOrAbsent(sub, key), wanted) {
			return false
		}
	}
	supTypes, supOK := typesOf(sup)
	subTypes, subOK := typesOf(sub)
	if !supOK || !subOK {
		return false
	}
	for _, typeName := range subTypes {
		if !slices.Contains(supTypes, typeName) && (typeName != "integer" || !slices.Contains(supTypes, "number")) {
			return false
		}
	}
	if _, ok := sub["const"]; ok {
		return true
	}
	if _, ok := anyList(sub["enum"]); ok {
		return true
	}
	if slices.Contains(subTypes, "object") && !objectFits(sub, sup) {
		return false
	}
	if slices.Contains(subTypes, "array") && !arrayFits(sub, sup) {
		return false
	}
	if slices.Contains(subTypes, "function") {
		return functionFits(sub, sup)
	}
	return true
}

func valueOrAbsent(schema Schema, key string) any {
	if value, ok := schema[key]; ok {
		return value
	}
	return absent{}
}

func objectFits(sub, sup Schema) bool {
	subProperties := asSchema(sub["properties"])
	supProperties := asSchema(sup["properties"])
	subRequired := stringList(sub["required"])
	for _, key := range stringList(sup["required"]) {
		if !slices.Contains(subRequired, key) {
			return false
		}
	}
	subAdditional, subAdditionalSchema := sub["additionalProperties"].(map[string]any)
	for key, property := range supProperties {
		if subProperty, ok := subProperties[key]; ok {
			if !Fits(subProperty, property) {
				return false
			}
		} else if subAdditionalSchema && !Fits(subAdditional, property) {
			// The consumer may send this name with any value its additionalProperties allows. Open consumer objects
			// are fine: unknown names are stripped with the consumer's own schema before the call.
			return false
		}
	}
	if sup["additionalProperties"] == false {
		if sub["additionalProperties"] != false {
			return false
		}
		for key := range subProperties {
			if _, ok := supProperties[key]; !ok {
				return false
			}
		}
	}
	supPatterns := asSchema(sup["patternProperties"])
	if len(supPatterns) > 0 {
		// Every value the consumer may put under a matching name must fit, and the consumer must not allow arbitrary
		// extra names (it would then allow any value under a matching one).
		subPatterns := asSchema(sub["patternProperties"])
		if sub["additionalProperties"] != false && len(subPatterns) == 0 {
			return false
		}
		for pattern, property := range supPatterns {
			matcher, err := typebox.CompilePattern(pattern)
			if err != nil {
				return false
			}
			for key, subProperty := range subProperties {
				if _, inSup := supProperties[key]; !inSup && matcher.MatchString(key) && !Fits(subProperty, property) {
					return false
				}
			}
			if len(subPatterns) > 0 {
				subPattern, ok := subPatterns[pattern]
				if !ok || !Fits(subPattern, property) {
					return false
				}
			}
		}
		for pattern := range subPatterns {
			if _, ok := supPatterns[pattern]; !ok {
				return false
			}
		}
	}
	return true
}

func arrayFits(sub, sup Schema) bool {
	if supItems, ok := anyList(sup["items"]); ok {
		subItems, tuple := anyList(sub["items"])
		if !tuple || len(subItems) != len(supItems) {
			return false
		}
		for index, item := range subItems {
			if !Fits(item, supItems[index]) {
				return false
			}
		}
		return true
	}
	if subItems, ok := anyList(sub["items"]); ok {
		for _, item := range subItems {
			if !Fits(item, sup["items"]) {
				return false
			}
		}
		return true
	}
	return Fits(sub["items"], sup["items"])
}

func functionFits(sub, sup Schema) bool {
	subParameters, _ := anyList(sub["parameters"])
	supParameters, _ := anyList(sup["parameters"])
	// A function accepting fewer parameters is fine; each shared parameter is contravariant.
	if len(subParameters) > len(supParameters) {
		return false
	}
	for index, parameter := range subParameters {
		if !Fits(supParameters[index], parameter) {
			return false
		}
	}
	return Fits(sub["returnType"], sup["returnType"])
}
