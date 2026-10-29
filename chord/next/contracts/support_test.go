package contracts_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Shared helpers of the contracts tests. The TypeBox constructors the upstream tests call (Type.Object, Type.Tuple, ...)
// have no Go counterpart in the package; the tests build the schema data TypeBox 1.3.27 emits for them.

// must returns value, panicking on err: the test binary then fails with the error and its stack.
func must[T any](value T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("unexpected error: %v", err))
	}
	return value
}

// wantError fails unless err is non-nil and its message contains text.
func wantError(t testing.TB, err error, text string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got none", text)
	}
	if !strings.Contains(err.Error(), text) {
		t.Fatalf("error %q does not contain %q", err.Error(), text)
	}
}

// wantNoError fails if err is non-nil.
func wantNoError(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// js decodes JSON text into strict JSON data with ordered objects.
func js(t testing.TB, text string) delta.JsonValue {
	t.Helper()
	return must(delta.DecodeJson([]byte(text)))
}

// raw is a schema built from JSON text, as a JavaScript caller's plain object schema.
func raw(t testing.TB, text string) contracts.TSchema {
	t.Helper()
	return contracts.TSchema{Node: js(t, text)}
}

// stringify is JSON.stringify.
func stringify(t testing.TB, value any) string {
	t.Helper()
	return string(must(jsstring.MarshalJSON(value)))
}

// canon is JSON text with object keys sorted, so two values compare equal whatever their key order.
func canon(t testing.TB, value any) string {
	t.Helper()
	return stringify(t, sorted(value))
}

func sorted(value any) any {
	switch typed := value.(type) {
	case *delta.JsonObject:
		keys := typed.Keys()
		copied := delta.NewJsonObject(len(keys))
		for _, key := range sortedKeys(keys) {
			member, _ := typed.Get(key)
			copied.Set(key, sorted(member))
		}
		return copied
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sorted(item)
		}
		return out
	case contracts.TSchema:
		return sorted(typed.Node)
	}
	return value
}

func sortedKeys(keys []string) []string {
	out := append([]string(nil), keys...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// sameJSON fails unless got equals the JSON text want, ignoring object key order.
func sameJSON(t testing.TB, got any, want string) {
	t.Helper()
	if have, expected := canon(t, got), canon(t, js(t, want)); have != expected {
		t.Fatalf("got %s, want %s", have, expected)
	}
}

// tbObject is Type.Object(properties): TypeBox's open object, `required` left out when empty.
func tbObject(properties ...contracts.Property) contracts.TSchema {
	props := delta.NewJsonObject(len(properties))
	var required []any
	for _, property := range properties {
		props.Set(property.Name, property.Value.Node)
		if !property.Value.Optional {
			required = append(required, property.Name)
		}
	}
	node := delta.JsonObjectOf("type", "object")
	if len(required) > 0 {
		node.Set("required", required)
	}
	node.Set("properties", props)
	return contracts.TSchema{Node: node}
}

// tbClosedObject is Type.Object(properties, { additionalProperties: false }), which obj() makes.
func tbClosedObject(properties ...contracts.Property) contracts.TSchema {
	schema := tbObject(properties...)
	schema.Node.(*delta.JsonObject).Set("additionalProperties", false)
	return schema
}

// server is the host kind of the upstream tests' `Server`.
var server = mustKind("server")

// mustKind declares a host kind, panicking on a declaration error (package-level test fixtures).
func mustKind(name string, params ...contracts.Property) *contracts.HostKind {
	kind, err := contracts.NewHostKind(name, params...)
	if err != nil {
		panic(err)
	}
	return kind
}

// serverAddress is `() => Server()`.
func serverAddress(*delta.JsonObject) (contracts.Address, error) {
	return server.Address(nil), nil
}

// noParams is `params: {}`.
func noParams() contracts.Params { return contracts.Params{} }

// props builds a params record.
func props(properties ...contracts.Property) contracts.Params {
	return contracts.Params(append([]contracts.Property{}, properties...))
}

// remote declares a remote contract with the server host address; variants are `name: declaration` pairs.
func remote(t testing.TB, name string, version float64, params contracts.Params, variants ...contracts.VariantEntry) *contracts.Contract {
	t.Helper()
	return must(declareRemote(name, version, params, variants...))
}

func declareRemote(name string, version float64, params contracts.Params, variants ...contracts.VariantEntry) (*contracts.Contract, error) {
	return contracts.Remote(name, contracts.RemoteDeclaration{
		Version:     version,
		Params:      params,
		HostAddress: serverAddress,
		Variants:    variants,
	})
}

// local declares a local contract.
func local(t testing.TB, name string, version float64, params contracts.Params, members ...contracts.LocalEntry) *contracts.Contract {
	t.Helper()
	return must(contracts.Local(name, contracts.LocalDeclaration{Version: version, Params: params, Members: append([]contracts.LocalEntry{}, members...)}))
}

// variant is `{ state, methods }` of one remote variant.
func variant(name string, state []contracts.Property, methods ...contracts.MethodEntry) contracts.VariantEntry {
	return contracts.Var(name, contracts.VariantDeclaration{State: state, Methods: methods})
}

func state(properties ...contracts.Property) []contracts.Property { return properties }

// dataOf is the Chord-owned data of a contract.
func dataOf(t testing.TB, contract *contracts.Contract) *contracts.ContractData {
	t.Helper()
	return must(contracts.ContractDataOf(contract))
}

// currentVariants is `currentVariants(contract)`.
func currentVariants(t testing.TB, contract *contracts.Contract) *contracts.NamedMap[*contracts.VariantData] {
	t.Helper()
	return must(contracts.CurrentVariants(contract))
}

// keys lists the names of a map in order.
func keys[V any](m *contracts.NamedMap[V]) []string { return append([]string{}, m.Keys()...) }

// mapping functions, which the accepts tests never call.
func fn(value delta.JsonValue) (delta.JsonValue, error) { return value, nil }

func call(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) { return args, nil }

func stateFn() contracts.StateMapping { return contracts.StateMapping{Func: fn} }

// sameAs fails unless got and want are the same JSON, ignoring object key order.
func sameAs(t testing.TB, got, want any) {
	t.Helper()
	if have, expected := canon(t, got), canon(t, want); have != expected {
		t.Fatalf("got %s, want %s", have, expected)
	}
}
