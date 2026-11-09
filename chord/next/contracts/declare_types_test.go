package contracts_test

import (
	"reflect"
	"testing"

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
