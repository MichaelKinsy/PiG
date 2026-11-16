package contracts_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// upstream: packages/chord/test/contracts/declare.test.ts, the @ts-expect-error variants "a keyed member under a remote variant's state" and "a method under state / a schema under methods". Upstream's type check refuses them at compile time; Go's compiler does too, and this pins the types that make it refuse: a remote variant's state holds Schemas and its methods hold *Methods, so a *KeyedMember or a *Method can be put under neither.
func TestARemoteVariantsMembersAreTypedSoAKeyedMemberOrMethodCannotBeDeclaredInItsState(t *testing.T) {
	variant := reflect.TypeFor[contracts.VariantDeclaration]()
	stateField, _ := variant.FieldByName("State")
	methodsField, _ := variant.FieldByName("Methods")
	stateValue, _ := stateField.Type.Elem().FieldByName("Value")
	methodsValue, _ := methodsField.Type.Elem().FieldByName("Value")
	if stateValue.Type != reflect.TypeFor[contracts.Schema]() {
		t.Fatalf("a remote variant's state holds %v, want contracts.Schema", stateValue.Type)
	}
	if methodsValue.Type != reflect.TypeFor[*contracts.Method]() {
		t.Fatalf("a remote variant's methods hold %v, want *contracts.Method", methodsValue.Type)
	}
	for _, other := range []reflect.Type{reflect.TypeFor[*contracts.KeyedMember](), reflect.TypeFor[*contracts.Method]()} {
		if other.AssignableTo(stateValue.Type) {
			t.Errorf("%v is assignable to a remote variant's state", other)
		}
	}
	if reflect.TypeFor[contracts.Schema]().AssignableTo(methodsValue.Type) {
		t.Error("a Schema is assignable to a remote variant's methods")
	}
}

// upstream: packages/chord/test/contracts/declare.test.ts "queries members (slice 14 §3.1)": the case "refuse a queries member in a remote contract" (the refusal `queries members are only in local contracts` is reached only through `untyped.remote`) and "type the implementation as one named query per listed name" (`expectTypeOf`). Designed out as the keyed member's are: a remote variant's state holds Schemas, and *QueriesMember is no Schema (a compile error), so there is nothing to refuse at runtime; the implementation's named queries are one NamedQuery per name by the type of VariantImplementation.Queries.
func TestAQueriesMemberIsNoRemoteStateAndAVariantImplementationHoldsOneNamedQueryPerName(t *testing.T) {
	variant := reflect.TypeFor[contracts.VariantDeclaration]()
	stateField, _ := variant.FieldByName("State")
	stateValue, _ := stateField.Type.Elem().FieldByName("Value")
	if reflect.TypeFor[*contracts.QueriesMember]().AssignableTo(stateValue.Type) {
		t.Error("*QueriesMember is assignable to a remote variant's state")
	}
	queries, _ := reflect.TypeFor[contracts.VariantImplementation]().FieldByName("Queries")
	if want := reflect.TypeFor[map[string]map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]](); queries.Type != want {
		t.Fatalf("VariantImplementation.Queries is %v, want %v", queries.Type, want)
	}
}
