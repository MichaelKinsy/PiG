package contracts_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Pi packages/chord/src/next/contracts/schema.ts:181-192: `keyed(schema)` is `new KeyedMember(schema)`, a keyed state member that holds the
// schema it was given as `.schema` and is a member of a local contract. Go refuses an opt(...) schema where Pi's type rules it out.
func TestKeyedMemberHoldsTheSchemaItWasDeclaredWithAndIsALocalMember(t *testing.T) {
	schema := contracts.Obj(contracts.Prop("text", contracts.Str()))
	var declared contracts.LocalMember = contracts.Keyed(schema)
	member, ok := declared.(*contracts.KeyedMember)
	if !ok {
		t.Fatalf("Keyed returned %T, want *KeyedMember", declared)
	}
	if !reflect.DeepEqual(member.Schema, schema) {
		t.Fatalf("KeyedMember.Schema = %#v, want the declared schema %#v", member.Schema, schema)
	}
	other := contracts.Keyed(contracts.Int())
	if reflect.DeepEqual(other.Schema, member.Schema) {
		t.Fatal("two KeyedMembers of different schemas hold the same schema")
	}
}

func TestKeyedMemberRefusesAnOptionalSchema(t *testing.T) {
	defer func() {
		recovered := recover()
		message, _ := recovered.(string)
		if !strings.Contains(message, "Keyed") || !strings.Contains(message, "opt(...)") {
			t.Fatalf("Keyed(Opt(...)) panicked with %v, want the opt(...) refusal", recovered)
		}
	}()
	contracts.Keyed(contracts.Opt(contracts.Str()))
	t.Fatal("Keyed accepted an opt(...) schema")
}
