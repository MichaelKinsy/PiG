package contracts

import (
	"math"
	"reflect"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/contracts/snapshot.ts (schema snapshots: one schema-aware walk checks an app schema
// against Chord's vocabulary and produces a Chord-owned copy and its normal form, the fingerprint input).
//
// Designed out: the refusal of TypeBox refinements, codecs and unsafe types, of symbol keys and of accessors (a Go
// schema is JSON data and cannot carry them), and withBytesCheck (the bytes marker is checked by CheckBytes).

// Snapshot is a Chord-owned copy of an app schema, its normal form, and whether it has a `bytes()` leaf (values
// crossing a link then carry raw byte segments); immutable by convention. Contract data holds snapshots by pointer.
type Snapshot struct {
	Schema TSchema
	Normal delta.JsonValue
	Bytes  bool
}

// SnapshotSchema is the snapshot of a state, result or params schema. source is a TSchema, an ordered
// *delta.JsonObject, a map[string]any or a bool; the error names path for anything outside the vocabulary.
func SnapshotSchema(source any, path string) (*Snapshot, error) {
	return snapshotOf(source, path, objectsOpen)
}

// SnapshotArguments is the snapshot of a method's arguments: every object schema inside must also be closed (records
// allowed).
func SnapshotArguments(source any, path string) (*Snapshot, error) {
	return snapshotOf(source, path, objectsClosed)
}

func snapshotOf(source any, path string, objects objectsPolicy) (*Snapshot, error) {
	if schema, ok := source.(TSchema); ok {
		source = schema.Node
	}
	walked, err := walk(source, path, objects, map[uintptr]struct{}{})
	if err != nil {
		return nil, err
	}
	return &Snapshot{Schema: TSchema{Node: walked.schema}, Normal: walked.normal, Bytes: walked.bytes}, nil
}

// objectsPolicy says whether object schemas must set `additionalProperties: false`.
type objectsPolicy int

const (
	objectsOpen objectsPolicy = iota
	objectsClosed
)

// copied is a copied keyword value, its normal form, and whether a schema inside it is a bytes leaf.
type copied struct {
	schema any
	normal delta.JsonValue
	bytes  bool
}

type keywordGroup int

const (
	groupSchema keywordGroup = iota + 1
	groupSchemas
	groupItems
	groupSchemaMap
	groupType
	groupRequired
	groupEnum
	groupData
	groupAnnotation
)

// keywordGroups is every supported keyword, grouped by how its value is walked.
var keywordGroups = map[keywordGroup]string{
	groupSchema:    "additionalProperties additionalItems",
	groupSchemas:   "anyOf allOf prefixItems",
	groupItems:     "items",
	groupSchemaMap: "properties patternProperties $defs",
	groupType:      "type",
	groupRequired:  "required",
	groupEnum:      "enum",
	groupData: "const default $ref $id pattern format minLength maxLength minimum maximum exclusiveMinimum exclusiveMaximum " +
		"multipleOf minItems maxItems uniqueItems minProperties maxProperties readOnly writeOnly",
	groupAnnotation: "title description examples $comment deprecated",
}

var keywords = func() map[string]keywordGroup {
	out := map[string]keywordGroup{}
	for group, names := range keywordGroups {
		for name := range strings.FieldsSeq(names) {
			out[name] = group
		}
	}
	return out
}()

var schemaTypes = []string{"null", "boolean", "number", "integer", "string", "array", "object", "bytes"}

var bytesKeywords = []string{"type", "minLength", "maxLength"}

// schemaObject reads a plain object schema: an ordered object or a string-keyed map.
func schemaObject(source any) (entries []Entry[any], identity uintptr, ok bool) {
	switch typed := source.(type) {
	case *delta.JsonObject:
		if typed == nil {
			return nil, 0, false
		}
		for key, value := range typed.All() {
			entries = append(entries, Entry[any]{key, value})
		}
		return entries, reflect.ValueOf(typed).Pointer(), true
	case map[string]any:
		if typed == nil {
			return nil, 0, false
		}
		for key, value := range typed {
			entries = append(entries, Entry[any]{key, value})
		}
		return entries, reflect.ValueOf(typed).Pointer(), true
	}
	return nil, 0, false
}

// sortedEntries is `ownEntries`: the data entries sorted by key in UTF-16 order.
func sortedEntries(entries []Entry[any]) []Entry[any] {
	out := slices.Clone(entries)
	slices.SortStableFunc(out, func(a, b Entry[any]) int { return compare16(a.Name, b.Name) })
	return out
}

func walk(source any, path string, objects objectsPolicy, ancestors map[uintptr]struct{}) (copied, error) {
	if flag, ok := source.(bool); ok {
		return copied{schema: flag, normal: flag}, nil
	}
	entries, identity, ok := schemaObject(source)
	if !ok {
		return copied{}, fail("%s: expected a schema", path)
	}
	if _, cyclic := ancestors[identity]; cyclic {
		return copied{}, fail("%s: cyclic schema; use $defs and $ref for recursion", path)
	}
	typeName, _ := lookup(entries, "type")
	isBytes := typeName == "bytes"
	ancestors[identity] = struct{}{}
	defer delete(ancestors, identity)
	node := delta.NewJsonObject(len(entries))
	normal := delta.NewJsonObject(len(entries))
	hasBytes := isBytes
	for _, entry := range sortedEntries(entries) {
		group, known := keywords[entry.Name]
		if !known || isBytes && group != groupAnnotation && !slices.Contains(bytesKeywords, entry.Name) {
			return copied{}, fail("%s: unsupported keyword %s", path, quote(entry.Name))
		}
		item, err := walkKeyword(group, entry.Value, path+"."+entry.Name, objects, ancestors)
		if err != nil {
			return copied{}, err
		}
		hasBytes = hasBytes || item.bytes
		node.Set(entry.Name, item.schema)
		// Annotations are dropped on schema nodes only; inside data they are ordinary keys.
		if group != groupAnnotation {
			normal.Set(entry.Name, item.normal)
		}
	}
	if objects == objectsClosed && isOpenObject(node) {
		return copied{}, fail("%s: argument objects must be closed (additionalProperties: false)", path)
	}
	if isBytes {
		if err := checkBytesBounds(node, path); err != nil {
			return copied{}, err
		}
	}
	return copied{schema: node, normal: normal, bytes: hasBytes}, nil
}

func lookup(entries []Entry[any], name string) (any, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry.Value, true
		}
	}
	return nil, false
}

func walkKeyword(group keywordGroup, value any, path string, objects objectsPolicy, ancestors map[uintptr]struct{}) (copied, error) {
	switch group {
	case groupSchema:
		return walk(value, path, objects, ancestors)
	case groupSchemas:
		return walkList(value, path, objects, ancestors)
	case groupItems:
		if _, isList := value.([]any); isList {
			return walkList(value, path, objects, ancestors)
		}
		return walk(value, path, objects, ancestors)
	case groupSchemaMap:
		entries, _, ok := schemaObject(value)
		if !ok {
			return copied{}, fail("%s: expected an object of schemas", path)
		}
		schemas, normals := delta.NewJsonObject(len(entries)), delta.NewJsonObject(len(entries))
		hasBytes := false
		for _, entry := range sortedEntries(entries) {
			item, err := walk(entry.Value, path+"."+entry.Name, objects, ancestors)
			if err != nil {
				return copied{}, err
			}
			schemas.Set(entry.Name, item.schema)
			normals.Set(entry.Name, item.normal)
			hasBytes = hasBytes || item.bytes
		}
		return copied{schema: schemas, normal: normals, bytes: hasBytes}, nil
	case groupType:
		name, ok := value.(string)
		if !ok || !slices.Contains(schemaTypes, name) {
			return copied{}, fail("%s: unsupported type %s", path, stringifyForError(value))
		}
		return copied{schema: name, normal: name}, nil
	case groupRequired:
		list, ok := value.([]any)
		names := make([]string, 0, len(list))
		for _, item := range list {
			name, isString := item.(string)
			ok = ok && isString
			names = append(names, name)
		}
		if !ok {
			return copied{}, fail("%s: expected an array of strings", path)
		}
		sortedNames := sortStrings(names)
		out := make([]any, len(sortedNames))
		for i, name := range sortedNames {
			out[i] = name
		}
		return copied{schema: out, normal: slices.Clone(out)}, nil
	case groupEnum:
		datum, err := data(value, path)
		if err != nil {
			return copied{}, err
		}
		values, ok := datum.normal.([]any)
		if !ok {
			return copied{}, fail("%s: expected an array", path)
		}
		sortedValues := slices.Clone(values)
		keysOf := make(map[int]string, len(values))
		order := make([]int, len(values))
		for i, item := range values {
			order[i] = i
			keysOf[i] = stringifyForError(item)
		}
		slices.SortStableFunc(order, func(a, b int) int { return compare16(keysOf[a], keysOf[b]) })
		for i, from := range order {
			sortedValues[i] = values[from]
		}
		return copied{schema: sortedValues, normal: slices.Clone(sortedValues)}, nil
	default:
		return data(value, path)
	}
}

func walkList(value any, path string, objects objectsPolicy, ancestors map[uintptr]struct{}) (copied, error) {
	list, ok := value.([]any)
	if !ok {
		return copied{}, fail("%s: expected an array of schemas", path)
	}
	schemas, normals := make([]any, len(list)), make([]any, len(list))
	hasBytes := false
	for i, item := range list {
		walked, err := walk(item, path+"["+itoa(i)+"]", objects, ancestors)
		if err != nil {
			return copied{}, err
		}
		schemas[i], normals[i] = walked.schema, walked.normal
		hasBytes = hasBytes || walked.bytes
	}
	return copied{schema: schemas, normal: normals, bytes: hasBytes}, nil
}

// data is a JSON value as a copy with sorted keys; the same copy serves as snapshot and normal form.
func data(value any, path string) (copied, error) {
	if !chordjson.IsValue(value) {
		return copied{}, fail("%s: expected JSON data (finite numbers, plain objects and arrays)", path)
	}
	datum, err := sortedCopy(value)
	if err != nil {
		return copied{}, fail("%s: expected JSON data (finite numbers, plain objects and arrays)", path)
	}
	return copied{schema: datum, normal: datum}, nil
}

// sortedCopy is a detached copy of strict JSON with every object's keys sorted (UTF-16 order).
func sortedCopy(value any) (any, error) {
	plain, err := chordjson.Copy(value)
	if err != nil {
		return nil, err
	}
	return sortKeys(plain), nil
}

func sortKeys(value any) any {
	switch typed := value.(type) {
	case []any:
		for i, item := range typed {
			typed[i] = sortKeys(item)
		}
		return typed
	case *delta.JsonObject:
		keys := sortStrings(typed.Keys())
		out := delta.NewJsonObject(len(keys))
		for _, key := range keys {
			out.Set(key, sortKeys(typed.Value(key)))
		}
		return out
	}
	return value
}

// stringifyForError is JSON.stringify of a value for a message or a sort key.
func stringifyForError(value any) string {
	text, err := jsstring.MarshalJSON(value)
	if err != nil {
		return "undefined"
	}
	return string(text)
}

// isOpenObject is an object schema that admits unknown fields. A record (pattern properties only) is allowed.
func isOpenObject(node *delta.JsonObject) bool {
	isRecord := node.Has("patternProperties") && !node.Has("properties")
	isObject := node.Value("type") == "object" || node.Has("properties")
	closed := node.Value("additionalProperties") == false
	return isObject && !isRecord && !closed
}

func checkBytesBounds(node *delta.JsonObject, path string) error {
	for _, name := range []string{"minLength", "maxLength"} {
		bound, present := node.Get(name)
		if !present {
			continue
		}
		number, ok := bound.(float64)
		if !ok || number != math.Trunc(number) || math.Abs(number) > 1<<53-1 || number < 0 {
			return fail("%s: bytes bounds must be non-negative integers", path)
		}
	}
	return nil
}
