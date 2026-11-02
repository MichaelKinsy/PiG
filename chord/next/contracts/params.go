package contracts

import (
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/next/contracts/params.ts (the policy for params fields and the closed object schema checked
// on every get). The type-level ParamSchema, Params and RequiredFields types are designed out: Params is a slice of
// properties (see schema.go) and the policy is the runtime check below.

// ParamsData is the Chord-owned data of declared params, held by pointer.
type ParamsData struct {
	// Fields are the field names in identity order.
	Fields []string
	// Schema is the closed object schema of all fields.
	Schema TSchema
	// Normal is the `[field, normal form]` pairs in identity order.
	Normal delta.JsonValue
}

// ReadParams reads declared params fields: each a required Str(), Int(), Bool() or OneOf(...) of strings, constraints
// allowed. Fields are read in the order JavaScript enumerates an object's own keys.
func ReadParams(declared []Property, path string) (*ParamsData, error) {
	ordered := orderedEntries(declared)
	snapshots := make([]*Snapshot, len(ordered))
	for i, field := range ordered {
		fieldPath := path + "." + field.Name
		snapshot, err := SnapshotSchema(field.Value, fieldPath)
		if err != nil {
			return nil, err
		}
		if field.Value.Optional || !isParamSchema(snapshot.Normal) {
			return nil, fail("%s: a param is a required str(), int(), bool() or oneOf(...) of strings", fieldPath)
		}
		snapshots[i] = snapshot
	}
	properties := delta.NewJsonObject(len(ordered))
	required := make([]any, len(ordered))
	fields := make([]string, len(ordered))
	normal := make([]any, len(ordered))
	for i, field := range ordered {
		properties.Set(field.Name, snapshots[i].Schema.Node)
		required[i], fields[i] = field.Name, field.Name
		normal[i] = []any{field.Name, snapshots[i].Normal}
	}
	schema := delta.JsonObjectOf("type", "object", "properties", properties, "required", required, "additionalProperties", false)
	return &ParamsData{Fields: fields, Schema: TSchema{Node: schema}, Normal: normal}, nil
}

// JoinKey joins key parts with `|`, each escaped (`\` and `|`), so two different part lists never join to one key.
func JoinKey(parts []string) string {
	escaped := make([]string, len(parts))
	for i, part := range parts {
		escaped[i] = strings.ReplaceAll(strings.ReplaceAll(part, `\`, `\\`), "|", `\|`)
	}
	return strings.Join(escaped, "|")
}

func isParamSchema(normal delta.JsonValue) bool {
	node, ok := normal.(*delta.JsonObject)
	if !ok {
		return false
	}
	if branches, present := node.Get("anyOf"); present {
		list, isList := branches.([]any)
		return node.Len() == 1 && isList && !slices.ContainsFunc(list, func(branch any) bool { return !isStringLiteral(branch) })
	}
	typeName, _ := node.Value("type").(string)
	allowed, known := paramKeywords[typeName]
	if !known {
		return false
	}
	return !slices.ContainsFunc(node.Keys(), func(key string) bool { return !slices.Contains(allowed, key) })
}

// paramKeywords are the keywords a params field may use, by type.
var paramKeywords = map[string][]string{
	"string":  {"type", "minLength", "maxLength", "pattern", "format"},
	"integer": {"type", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"},
	"boolean": {"type"},
}

func isStringLiteral(branch any) bool {
	node, ok := branch.(*delta.JsonObject)
	if !ok {
		return false
	}
	_, isString := node.Value("const").(string)
	return node.Value("type") == "string" && isString && node.Len() == 2
}
