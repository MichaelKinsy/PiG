package contracts

import "testing"

// Proof of keyed(schema) of a local contract (schema.ts KeyedMember): it holds the schema, and refuses an optional one.

func TestKeyedMemberHoldsItsSchemaAndRefusesAnOptionalOne(t *testing.T) {
	schema := Str()
	var member *KeyedMember = Keyed(schema) //nolint:staticcheck // ST1023: the declared type is the pin: the test asserts the symbol has it
	if member.Schema != schema {
		t.Fatal("Keyed did not keep its schema")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Keyed accepted opt(...), which marks an object property and is no schema")
		}
	}()
	Keyed(Opt(Str()))
}
