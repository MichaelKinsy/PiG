package sdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// decodeOrdered reads JSON into []any, *OrderedObject (keys in the order of the text), float64, string, bool and nil, the values a constructor of Type takes.
func decodeOrdered(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return readOrdered(decoder)
}

func readOrdered(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token := token.(type) {
	case json.Delim:
		if token == '[' {
			list := []any{}
			for decoder.More() {
				item, err := readOrdered(decoder)
				if err != nil {
					return nil, err
				}
				list = append(list, item)
			}
			_, err := decoder.Token()
			return list, err
		}
		object := NewOrderedObject()
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			value, err := readOrdered(decoder)
			if err != nil {
				return nil, err
			}
			object.set(keyToken.(string), value)
		}
		_, err := decoder.Token()
		return object, err
	case json.Number:
		number, err := token.Float64()
		return number, err
	}
	return token, nil
}

// predicateSpec is a {"$fn": name, ...} node of a vector case: the predicate or message function the case names, which every SDK's test builds from the same spec (the generator's
// predicateFor is the same table). Its calls are appended to log, as "check:tag" and "error:tag", where there is one.
type predicateSpec struct {
	name string
	spec *OrderedObject
	log  *[]string
}

func (p predicateSpec) number(key string) float64 { v, _ := p.spec.values[key].(float64); return v }
func (p predicateSpec) text(key string) string    { v, _ := p.spec.values[key].(string); return v }
func (p predicateSpec) record(kind string) {
	if p.log == nil {
		return
	}
	tag := p.text("tag")
	if tag == "" {
		tag = p.name
	}
	*p.log = append(*p.log, kind+":"+tag)
}

func (p predicateSpec) check() func(any) (bool, error) {
	return func(value any) (bool, error) {
		p.record("check")
		switch p.name {
		case "always":
			return true, nil
		case "never":
			return false, nil
		case "lengthAtLeast":
			text, _ := value.(string)
			return float64(len(text)) >= p.number("n"), nil
		case "equals":
			return value == p.spec.values["value"], nil
		case "even":
			number, _ := value.(float64)
			return math.Mod(number, 2) == 0, nil
		case "greaterThan":
			number, _ := value.(float64)
			return number > p.number("n"), nil
		case "throws", "throwsText":
			return false, errors.New(p.text("message"))
		}
		return false, fmt.Errorf("no predicate %s", p.name)
	}
}

func (p predicateSpec) message() func(any) (string, error) {
	return func(value any) (string, error) {
		p.record("error")
		switch p.name {
		case "text":
			return p.text("text"), nil
		case "textWithValue":
			return p.text("prefix") + jsString(value), nil
		case "errorThrows":
			return "", errors.New(p.text("message"))
		}
		return "", fmt.Errorf("no message function %s", p.name)
	}
}

// jsString is String(value) for the numbers and strings the cases use.
func jsString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if number, ok := value.(float64); ok {
		return jsNumberKey(number)
	}
	return fmt.Sprint(value)
}

// typeMembers is Type's members by the names Pi gives them, for the vector calls.
var typeMembers = map[string]func(args []any) any{
	"Any":       func(a []any) any { return Type.Any(typeOptions(a, 0)) },
	"Unknown":   func(a []any) any { return Type.Unknown(typeOptions(a, 0)) },
	"String":    func(a []any) any { return Type.String(typeOptions(a, 0)) },
	"Number":    func(a []any) any { return Type.Number(typeOptions(a, 0)) },
	"Integer":   func(a []any) any { return Type.Integer(typeOptions(a, 0)) },
	"Boolean":   func(a []any) any { return Type.Boolean(typeOptions(a, 0)) },
	"Null":      func(a []any) any { return Type.Null(typeOptions(a, 0)) },
	"Never":     func(a []any) any { return Type.Never(typeOptions(a, 0)) },
	"Undefined": func(a []any) any { return Type.Undefined(typeOptions(a, 0)) },
	"Void":      func(a []any) any { return Type.Void(typeOptions(a, 0)) },
	"Symbol":    func(a []any) any { return Type.Symbol(typeOptions(a, 0)) },
	"BigInt":    func(a []any) any { return Type.BigInt(typeOptions(a, 0)) },
	"Ref":       func(a []any) any { return Type.Ref(a[0].(string), typeOptions(a, 1)) },
	"Array":     func(a []any) any { return Type.Array(a[0], typeOptions(a, 1)) },
	"Union":     func(a []any) any { return Type.Union(a[0].([]any), typeOptions(a, 1)) },
	"Intersect": func(a []any) any { return Type.Intersect(a[0].([]any), typeOptions(a, 1)) },
	"Literal":   func(a []any) any { return Type.Literal(a[0], typeOptions(a, 1)) },
	"Enum":      func(a []any) any { return Type.Enum(a[0], typeOptions(a, 1)) },
	"Object":    func(a []any) any { return Type.Object(a[0].(*OrderedObject), typeOptions(a, 1)) },
	"Refine": func(a []any) any {
		check, _ := a[1].(predicateSpec)
		if len(a) > 2 {
			message, _ := a[2].(predicateSpec)
			return Type.Refine(a[0], check.check(), message.message())
		}
		return Type.Refine(a[0], check.check())
	},
	"IsRefine":   func(a []any) any { return Type.IsRefine(a[0]) },
	"Optional":   func(a []any) any { return Type.Optional(a[0]) },
	"Readonly":   func(a []any) any { return Type.Readonly(a[0]) },
	"Immutable":  func(a []any) any { return Type.Immutable(a[0]) },
	"Unsafe":     func(a []any) any { return Type.Unsafe(a[0]) },
	"With":       func(a []any) any { return Type.With(a[0], typeOptions(a, 1)) },
	"Record":     func(a []any) any { return Type.Record(a[0], a[1], typeOptions(a, 2)) },
	"IsKind":     func(a []any) any { return Type.IsKind(a[0], a[1].(string)) },
	"IsSchema":   func(a []any) any { return Type.IsSchema(a[0]) },
	"IsOptional": func(a []any) any { return Type.IsOptional(a[0]) }, "IsReadonly": func(a []any) any { return Type.IsReadonly(a[0]) },
	"IsImmutable": func(a []any) any { return Type.IsImmutable(a[0]) }, "IsUnsafe": func(a []any) any { return Type.IsUnsafe(a[0]) },
	"IsEnumValue": func(a []any) any { return Type.IsEnumValue(a[0]) },
	"IsAny":       func(a []any) any { return Type.IsAny(a[0]) }, "IsArray": func(a []any) any { return Type.IsArray(a[0]) },
	"IsBigInt": func(a []any) any { return Type.IsBigInt(a[0]) }, "IsBoolean": func(a []any) any { return Type.IsBoolean(a[0]) },
	"IsEnum": func(a []any) any { return Type.IsEnum(a[0]) }, "IsInteger": func(a []any) any { return Type.IsInteger(a[0]) },
	"IsIntersect": func(a []any) any { return Type.IsIntersect(a[0]) }, "IsLiteral": func(a []any) any { return Type.IsLiteral(a[0]) },
	"IsNever": func(a []any) any { return Type.IsNever(a[0]) }, "IsNull": func(a []any) any { return Type.IsNull(a[0]) },
	"IsNumber": func(a []any) any { return Type.IsNumber(a[0]) }, "IsObject": func(a []any) any { return Type.IsObject(a[0]) },
	"IsRecord": func(a []any) any { return Type.IsRecord(a[0]) }, "IsRef": func(a []any) any { return Type.IsRef(a[0]) },
	"IsString": func(a []any) any { return Type.IsString(a[0]) }, "IsSymbol": func(a []any) any { return Type.IsSymbol(a[0]) },
	"IsUndefined": func(a []any) any { return Type.IsUndefined(a[0]) }, "IsUnion": func(a []any) any { return Type.IsUnion(a[0]) },
	"IsUnknown": func(a []any) any { return Type.IsUnknown(a[0]) }, "IsVoid": func(a []any) any { return Type.IsVoid(a[0]) },
}

func typeOptions(args []any, index int) TypeOptions {
	if index >= len(args) {
		return nil
	}
	options, _ := args[index].(*OrderedObject)
	return options
}

// resolveType evaluates {"$call": member, "args": [...]} nodes and keeps every other JSON value.
func resolveType(node any) (any, error) {
	switch node := node.(type) {
	case []any:
		out := make([]any, len(node))
		for i, item := range node {
			value, err := resolveType(item)
			if err != nil {
				return nil, err
			}
			out[i] = value
		}
		return out, nil
	case *OrderedObject:
		if name, isFn := node.values["$fn"].(string); isFn {
			return predicateSpec{name: name, spec: node}, nil
		}
		if name, isCall := node.values["$call"].(string); isCall {
			rawArgs, _ := node.values["args"].([]any)
			args, err := resolveType(rawArgs)
			if err != nil {
				return nil, err
			}
			member, ok := typeMembers[name]
			if !ok {
				return nil, fmt.Errorf("the SDK's Type has no %s", name)
			}
			return callType(member, args.([]any))
		}
		out := NewOrderedObject()
		for _, key := range node.keys {
			value, err := resolveType(node.values[key])
			if err != nil {
				return nil, err
			}
			out.set(key, value)
		}
		return out, nil
	}
	return node, nil
}

// callType runs a member, turning its panic into the error message Pi throws.
func callType(member func([]any) any, args []any) (result any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%v", recovered)
		}
	}()
	return member(args), nil
}

type typeVector struct {
	Name   string
	Args   []json.RawMessage
	Result json.RawMessage
	Throws *string
}

// TestTypeMatchesPi runs every case of test/parity/sdk-vectors/pi-ai/Type.json: Pi's own `Type` (TypeBox 1.3.27) returned the JSON text, the key order and the hidden members.
func TestTypeMatchesPi(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "test", "parity", "sdk-vectors", "pi-ai", "Type.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Cases []typeVector }
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("Type.json has no cases")
	}
	for _, c := range file.Cases {
		node, err := decodeOrdered(c.Args[0])
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		got, err := resolveType(node)
		if c.Throws != nil {
			if err == nil || err.Error() != *c.Throws {
				t.Errorf("%s: error %v, Pi throws %q", c.Name, err, *c.Throws)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v, Pi answers %s", c.Name, err, c.Result)
			continue
		}
		var want struct {
			JSON   *string        `json:"json"`
			Hidden map[string]any `json:"hidden"`
		}
		if schema, isSchema := got.(*TSchema); isSchema {
			if err := json.Unmarshal(c.Result, &want); err != nil || want.JSON == nil {
				t.Errorf("%s: the SDK answers a schema, Pi %s", c.Name, c.Result)
				continue
			}
			text, err := schema.MarshalJSON()
			if err != nil {
				t.Errorf("%s: %v", c.Name, err)
				continue
			}
			if string(text) != *want.JSON {
				t.Errorf("%s:\n got  %s\n Pi   %s", c.Name, text, *want.JSON)
			}
			have := maps.Clone(schema.hidden)
			if list, isList := have["~refine"].([]Refinement); isList {
				have["~refine"] = map[string]any{"count": float64(len(list))} // functions have no JSON form: the vector records how many there are
			}
			if !reflect.DeepEqual(have, want.Hidden) {
				t.Errorf("%s: hidden members %v, Pi %v", c.Name, have, want.Hidden)
			}
			continue
		}
		var wantValue any
		if err := json.Unmarshal(c.Result, &wantValue); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, wantValue) {
			t.Errorf("%s: %v, Pi %v", c.Name, got, wantValue)
		}
	}
}

// TestTypeOmitsTheCodecFamily is the evidence of divergence D198: the Codec family of Pi's Type is not served, so none of its members exists.
func TestTypeOmitsTheCodecFamily(t *testing.T) {
	methods := reflect.TypeOf(Type)
	for _, name := range []string{"Codec", "Decode", "Encode", "DecodeBuilder", "EncodeBuilder", "IsCodec"} {
		if _, exists := methods.MethodByName(name); exists {
			t.Errorf("Type.%s exists; D198 records that the Codec family is not served", name)
		}
	}
}

// TestTypeSchemaRootKeepsItsDeclaredOrderOnTheWire: a tool registered with TSchema.Schema() writes the root in the order Type wrote it, without the member that records it, where a plain
// map writes its keys alphabetically.
func TestTypeSchemaRootKeepsItsDeclaredOrderOnTheWire(t *testing.T) {
	schema := Type.Object(NewOrderedObject("b", Type.String(), "a", Type.Optional(Type.Number())), NewOrderedObject("description", "d", "additionalProperties", false))
	want, err := schema.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := schemaRoot(schema.Schema()).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("root written as %s, want %s", got, want)
	}
	plain, err := schemaRoot(Schema{"type": "object", "properties": map[string]any{}}).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != `{"properties":{},"type":"object"}` {
		t.Errorf("a plain map is written as %s", plain)
	}
}
