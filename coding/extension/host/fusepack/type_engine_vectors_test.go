package fusepack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/typebox/engine"
)

// The Go SDK's Type members that the host's type engine serves, run through the SDK exactly as an extension calls them, against the vectors Pi's own Type (TypeBox 1.3.27) produced
// (test/parity/sdk-vectors/pi-ai/Type.json). The SDK module cannot import the host's engine, so its own tests skip these cases; this test, in the module that links both, runs them
// through the fused path (the SDK calls the engine in process, as a fused extension does). The isolated path (a host call over the extension socket) is the conformance row
// TestConformance_TypeEngineMembersAgreeAcrossSDKs.

func decodeOrderedValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token := token.(type) {
	case json.Delim:
		if token == '[' {
			list := []any{}
			for decoder.More() {
				item, err := decodeOrderedValue(decoder)
				if err != nil {
					return nil, err
				}
				list = append(list, item)
			}
			_, err := decoder.Token()
			return list, err
		}
		var pairs []any
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			value, err := decodeOrderedValue(decoder)
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, key.(string), value)
		}
		_, err := decoder.Token()
		return sdk.NewOrderedObject(pairs...), err
	case json.Number:
		return token.Float64()
	}
	return token, nil
}

// resolveVectorNode evaluates {"$call": member, "args": [...]} nodes through the SDK's Type, and keeps every other JSON value.
func resolveVectorNode(node any) (any, error) {
	switch node := node.(type) {
	case []any:
		out := make([]any, len(node))
		for i, item := range node {
			value, err := resolveVectorNode(item)
			if err != nil {
				return nil, err
			}
			out[i] = value
		}
		return out, nil
	case *sdk.OrderedObject:
		if tag, _ := node.Get("$"); tag == "bigint" {
			digits, _ := node.Get("value")
			value, ok := new(big.Int).SetString(fmt.Sprint(digits), 10)
			if !ok {
				return nil, fmt.Errorf("bad bigint %v", digits)
			}
			return sdk.NewBigInt(value), nil
		}
		if _, isFn := node.Get("$fn"); isFn {
			return nil, errSkipVector
		}
		if name, isCall := node.Get("$call"); isCall {
			rawArgs, _ := node.Get("args")
			args, err := resolveVectorNode(rawArgs)
			if err != nil {
				return nil, err
			}
			return callTypeMember(name.(string), args.([]any))
		}
		var pairs []any
		for _, key := range node.Keys() {
			value, _ := node.Get(key)
			resolved, err := resolveVectorNode(value)
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, key, resolved)
		}
		return sdk.NewOrderedObject(pairs...), nil
	}
	return node, nil
}

var errSkipVector = fmt.Errorf("the case needs something this test does not build")

// callTypeMember calls the method of sdk.Type named member, converting each argument to the parameter's type; a panic is the error Pi throws.
func callTypeMember(member string, args []any) (result any, err error) {
	method := reflect.ValueOf(sdk.Type).MethodByName(member)
	if !method.IsValid() {
		return nil, errSkipVector
	}
	signature := method.Type()
	fixed := signature.NumIn()
	if signature.IsVariadic() {
		fixed--
	}
	if len(args) < fixed {
		return nil, errSkipVector // JavaScript's undefined for a required argument has no Go value
	}
	in := make([]reflect.Value, len(args))
	for i, arg := range args {
		var want reflect.Type
		if i < fixed {
			want = signature.In(i)
		} else {
			want = signature.In(fixed).Elem()
		}
		if arg == nil {
			in[i] = reflect.Zero(want)
		} else {
			in[i] = reflect.ValueOf(arg)
			if !in[i].Type().AssignableTo(want) {
				return nil, errSkipVector
			}
		}
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%v", recovered)
		}
	}()
	out := method.Call(in)
	return out[0].Interface(), nil
}

// engineServedMembers are the members of Type the host's engine serves; the cases whose outermost call is one of them are the ones this test must run.
var engineServedMembers = map[string]bool{
	"Call": true, "Capitalize": true, "Conditional": true, "Constructor": true, "ConstructorParameters": true, "Cyclic": true, "Dependent": true, "Evaluate": true, "Exclude": true,
	"Extends": true, "Extract": true, "Function": true, "Generic": true, "Identifier": true, "Index": true, "Infer": true, "InstanceType": true, "Instantiate": true, "Interface": true,
	"KeyOf": true, "Lowercase": true, "Mapped": true, "Module": true, "NonNullable": true, "Omit": true, "Parameter": true, "Parameters": true, "Partial": true, "Pick": true,
	"ReadonlyObject": true, "ReadonlyType": true, "RecordKey": true, "RecordPattern": true, "RecordValue": true, "Required": true, "Rest": true, "ReturnType": true, "Script": true,
	"TemplateLiteral": true, "This": true, "Tuple": true, "Uncapitalize": true, "Uppercase": true,
}

func TestTypeEngineMembersMatchPi(t *testing.T) {
	sdk.SetHostHelpers(&sdk.HostHelpers{TypeEngine: engine.Serve})
	t.Cleanup(func() { sdk.SetHostHelpers(nil) })
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "test", "parity", "sdk-vectors", "pi-ai", "Type.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name   string
			Args   []json.RawMessage
			Result json.RawMessage
			Throws *string
		}
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	ran, engineCases := 0, 0
	for _, c := range file.Cases {
		node, err := decodeOrderedValue(json.NewDecoder(bytes.NewReader(c.Args[0])))
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		top, _ := node.(*sdk.OrderedObject)
		name, _ := top.Get("$call")
		member, _ := name.(string)
		if !engineServedMembers[member] {
			continue
		}
		engineCases++
		got, err := resolveVectorNode(node)
		if errors.Is(err, errSkipVector) {
			t.Errorf("%s: the SDK has no call for this case", c.Name)
			continue
		}
		ran++
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
			JSON            *string        `json:"json"`
			StringifyThrows *string        `json:"stringifyThrows"`
			Hidden          map[string]any `json:"hidden"`
		}
		if schema, isSchema := got.(*sdk.TSchema); isSchema {
			if err := json.Unmarshal(c.Result, &want); err != nil || (want.JSON == nil && want.StringifyThrows == nil) {
				t.Errorf("%s: the SDK answers a schema, Pi %s", c.Name, c.Result)
				continue
			}
			text, err := schema.MarshalJSON()
			if want.StringifyThrows != nil {
				// The schema holds a bigint, which only writing it as JSON refuses, with Pi's message.
				if err == nil || !strings.Contains(err.Error(), *want.StringifyThrows) {
					t.Errorf("%s: MarshalJSON gave %q, %v; Pi's JSON.stringify throws %q", c.Name, text, err, *want.StringifyThrows)
				}
			} else if err != nil || string(text) != *want.JSON {
				t.Errorf("%s:\n got  %s (%v)\n Pi   %s", c.Name, text, err, *want.JSON)
			}
			for _, key := range []string{"~kind", "~optional", "~readonly", "~immutable", "~unsafe", "~refine"} {
				have, present := schema.Hidden(key)
				wantValue, wanted := want.Hidden[key]
				if present != wanted || (present && !reflect.DeepEqual(have, wantValue)) {
					t.Errorf("%s: hidden %s = %v (%v), Pi %v (%v)", c.Name, key, have, present, wantValue, wanted)
				}
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
	if ran != engineCases || ran < 350 {
		t.Fatalf("%d of %d engine cases ran", ran, engineCases)
	}
}

// BenchmarkTypeBuildThroughEngine is a schema build that reaches the engine, the way an extension builds one: Type.Pick of a four-property object, with the engine in process (the
// fused path: the SDK's encoder and decoder and the engine, with no socket).
func BenchmarkTypeBuildThroughEngine(b *testing.B) {
	sdk.SetHostHelpers(&sdk.HostHelpers{TypeEngine: engine.Serve})
	b.Cleanup(func() { sdk.SetHostHelpers(nil) })
	base := sdk.Type.Object(sdk.NewOrderedObject("id", sdk.Type.String(), "name", sdk.Type.Optional(sdk.Type.String()), "n", sdk.Type.Integer(), "tags", sdk.Type.Array(sdk.Type.String())))
	b.ReportAllocs()
	for range b.N {
		_ = sdk.Type.Pick(base, []string{"id", "name"})
	}
}
