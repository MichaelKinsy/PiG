package configschema

// Ports the runtime side of packages/coding-agent/src/core/model-config.ts: the document check that ModelConfig.load runs against the schema
// trees of this package, with typebox's localized errors formatted by formatValidationPath. The schema trees are the only definition of models.json:
// the same value renders the JSON Schema artifact and validates the file at run time.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
)

type jsonKind uint8

const (
	jsonNull jsonKind = iota
	jsonBool
	jsonNumber
	jsonString
	jsonArray
	jsonObject
)

// jsonNode is a parsed JSON value that keeps the order JavaScript enumerates an object's keys in: integer-like keys
// ascending, then the rest in first-insertion order.
type jsonNode struct {
	kind   jsonKind
	text   string
	number float64
	items  []jsonNode
	keys   []string
	fields map[string]jsonNode
}

func parseJSONNode(data []byte) (jsonNode, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return parseJSONValue(decoder)
}

func parseJSONValue(decoder *json.Decoder) (jsonNode, error) {
	token, err := decoder.Token()
	if err != nil {
		return jsonNode{}, err
	}
	switch value := token.(type) {
	case json.Delim:
		if value == '[' {
			node := jsonNode{kind: jsonArray}
			for decoder.More() {
				item, err := parseJSONValue(decoder)
				if err != nil {
					return jsonNode{}, err
				}
				node.items = append(node.items, item)
			}
			_, err := decoder.Token()
			return node, err
		}
		node := jsonNode{kind: jsonObject, fields: map[string]jsonNode{}}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return jsonNode{}, err
			}
			key := keyToken.(string)
			item, err := parseJSONValue(decoder)
			if err != nil {
				return jsonNode{}, err
			}
			if _, seen := node.fields[key]; !seen {
				node.keys = append(node.keys, key)
			}
			node.fields[key] = item
		}
		if _, err := decoder.Token(); err != nil {
			return jsonNode{}, err
		}
		node.keys = jsKeyOrder(node.keys)
		return node, nil
	case string:
		return jsonNode{kind: jsonString, text: value}, nil
	case json.Number:
		number, err := strconv.ParseFloat(string(value), 64)
		if err != nil && !math.IsInf(number, 0) {
			return jsonNode{}, err
		}
		return jsonNode{kind: jsonNumber, number: number}, nil
	case bool:
		return jsonNode{kind: jsonBool, number: map[bool]float64{true: 1}[value]}, nil
	}
	return jsonNode{kind: jsonNull}, nil
}

func arrayIndexKey(key string) (uint64, bool) {
	if key == "" || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	index, err := strconv.ParseUint(key, 10, 64)
	return index, err == nil && index < 1<<32-1
}

func jsKeyOrder(keys []string) []string {
	var indexes, rest []string
	for _, key := range keys {
		if _, ok := arrayIndexKey(key); ok {
			indexes = append(indexes, key)
		} else {
			rest = append(rest, key)
		}
	}
	slices.SortStableFunc(indexes, func(a, b string) int {
		x, _ := arrayIndexKey(a)
		y, _ := arrayIndexKey(b)
		return int(max(-1, min(1, int64(x)-int64(y))))
	})
	return append(indexes, rest...)
}

// maxSchemaErrors is typebox's maxErrors: collection stops after this many errors.
// upstream: node_modules/typebox/build/system/settings/settings.mjs:maxErrors
const maxSchemaErrors = 8

type schemaError struct{ path, message string }

// formatSchemaPath is formatValidationPath: typebox does not escape "/" in an instance path, so a key containing one reads as nested.
func formatSchemaPath(instancePath []string, required string) string {
	segments := instancePath
	if required != "" {
		segments = append(slices.Clone(segments), required)
	}
	path := strings.ReplaceAll(strings.Join(segments, "/"), "/", ".")
	if path == "" {
		return "root"
	}
	return path
}

// Errors is validate.Errors(parsed) of the compiled schema as ModelConfig.load lists them: "  - path: message" per error, at most eight, in
// typebox's order. The error is the JSON syntax error of content; a document that matches the schema returns no lines.
func (s *Schema) Errors(content []byte) ([]string, error) {
	node, err := parseJSONNode(content)
	if err != nil {
		return nil, err
	}
	var errs []schemaError
	if s.check(node, nil, &errs) {
		return nil, nil
	}
	errs = errs[:min(len(errs), maxSchemaErrors)]
	lines := make([]string, len(errs))
	for i, e := range errs {
		lines[i] = "  - " + e.path + ": " + e.message
	}
	return lines, nil
}

func (s *Schema) number(key string) (float64, bool) {
	switch v, _ := s.get(key); n := v.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func (s *Schema) strings(key string) []string {
	list, _ := s.get(key)
	items, _ := list.([]any)
	out := make([]string, len(items))
	for i, item := range items {
		out[i], _ = item.(string)
	}
	return out
}

func (s *Schema) alternatives(key string) []*Schema {
	list, _ := s.get(key)
	items, _ := list.([]any)
	out := make([]*Schema, len(items))
	for i, item := range items {
		out[i], _ = item.(*Schema)
	}
	return out
}

func (s *Schema) child(key string) *Schema {
	v, _ := s.get(key)
	child, _ := v.(*Schema)
	return child
}

// modelledKeyword reports the schema keywords check reads or deliberately ignores (annotations). A tree that uses another keyword, or a type
// that check does not handle, panics, so a schema edit cannot silently weaken the validation of models.json. A switch, unlike a map, costs
// package initialization nothing.
func modelledKeyword(keyword string) bool {
	switch keyword {
	case "type", "const", "enum", "anyOf", "items", "properties", "required", "patternProperties",
		"minLength", "minimum", "maximum", "exclusiveMinimum", "maxItems", "additionalProperties",
		"description", "default", "deprecated", "title":
		return true
	}
	return false
}

func (s *Schema) check(node jsonNode, path []string, errs *[]schemaError) bool {
	for _, o := range s.kv {
		if !modelledKeyword(o.K) {
			panic("configschema: the validator does not model the keyword " + o.K)
		}
	}
	if kind, has := s.get("type"); has {
		switch kind {
		case "string", "number", "integer", "boolean", "null", "array", "object":
		default:
			panic(fmt.Sprintf("configschema: the validator does not model the type %v", kind))
		}
	}
	fail := func(message string) bool {
		*errs = append(*errs, schemaError{formatSchemaPath(path, ""), message})
		return false
	}
	if _, ok := s.get("anyOf"); ok {
		var branchErrs []schemaError
		for _, alt := range s.alternatives("anyOf") {
			var altErrs []schemaError
			if alt.check(node, path, &altErrs) {
				return true
			}
			branchErrs = append(branchErrs, altErrs...)
		}
		*errs = append(*errs, branchErrs...)
		return fail("must match a schema in anyOf")
	}
	if _, ok := s.get("enum"); ok {
		if node.kind != jsonString || !slices.Contains(s.strings("enum"), node.text) {
			return fail("must be equal to one of the allowed values")
		}
		return true
	}
	kind, _ := s.get("type")
	isNumber := node.kind == jsonNumber && !math.IsInf(node.number, 0) && !math.IsNaN(node.number)
	switch kind {
	case "string":
		if literal, isLiteral := s.get("const"); isLiteral {
			// TypeBox reports a string literal's type failure and then its constant failure.
			ok := true
			if node.kind != jsonString {
				ok = fail("must be string")
			}
			if text, _ := literal.(string); node.kind != jsonString || node.text != text {
				ok = fail("must be equal to constant")
			}
			return ok
		}
		if node.kind != jsonString {
			return fail("must be string")
		}
		if minLength, ok := s.number("minLength"); ok && minLength > 0 && float64(len(utf16.Encode([]rune(node.text)))) < minLength {
			return fail("must not have fewer than " + strconv.FormatFloat(minLength, 'f', -1, 64) + " characters")
		}
	case "number", "integer":
		if !isNumber || (kind == "integer" && node.number != math.Trunc(node.number)) {
			return fail("must be " + kind.(string))
		}
		ok := true
		if minimum, has := s.number("minimum"); has && node.number < minimum {
			ok = fail("must be >= " + strconv.FormatFloat(minimum, 'f', -1, 64))
		}
		if maximum, has := s.number("maximum"); has && node.number > maximum {
			ok = fail("must be <= " + strconv.FormatFloat(maximum, 'f', -1, 64))
		}
		if exclusive, has := s.number("exclusiveMinimum"); has && node.number <= exclusive {
			ok = fail("must be > " + strconv.FormatFloat(exclusive, 'f', -1, 64))
		}
		return ok
	case "boolean":
		if node.kind != jsonBool {
			return fail("must be boolean")
		}
	case "null":
		if node.kind != jsonNull {
			return fail("must be null")
		}
	case "array":
		if node.kind != jsonArray {
			return fail("must be array")
		}
		ok := true
		items := s.child("items")
		for i, item := range node.items {
			if !items.check(item, append(slices.Clone(path), strconv.Itoa(i)), errs) {
				ok = false
			}
		}
		if maxItems, has := s.number("maxItems"); has && maxItems > 0 && float64(len(node.items)) > maxItems {
			ok = fail("must not have more than " + strconv.FormatFloat(maxItems, 'f', -1, 64) + " items")
		}
		return ok
	case "object":
		if node.kind != jsonObject {
			return fail("must be object")
		}
		ok := true
		var missing []string
		for _, name := range s.strings("required") {
			if _, present := node.fields[name]; !present {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			*errs = append(*errs, schemaError{formatSchemaPath(path, missing[0]), "must have required properties " + strings.Join(missing, ", ")})
			ok = false
		}
		if properties := s.child("properties"); properties != nil {
			for _, prop := range properties.kv {
				if value, present := node.fields[prop.K]; present && !prop.V.(*Schema).check(value, append(slices.Clone(path), prop.K), errs) {
					ok = false
				}
			}
		}
		if pattern := s.child("patternProperties"); pattern != nil {
			elem := pattern.kv[0].V.(*Schema)
			for _, key := range node.keys {
				if !elem.check(node.fields[key], append(slices.Clone(path), key), errs) {
					ok = false
				}
			}
		}
		return ok
	}
	return true
}
