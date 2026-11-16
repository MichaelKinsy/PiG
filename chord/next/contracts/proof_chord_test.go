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

// Proof of queries(item, args) of a local contract (schema.ts QueriesMember): it holds the item schema and the argument schemas in order, and refuses an optional item.

func TestQueriesMemberHoldsItsItemAndArgumentsAndRefusesAnOptionalItem(t *testing.T) {
	item := Str()
	args := []Property{Prop("all", Obj()), Prop("one", Obj(Prop("id", Int())))}
	var member *QueriesMember = Queries(item, args) //nolint:staticcheck // ST1023: the declared type is the pin: the test asserts the symbol has it
	if member.Item != item || len(member.Args) != 2 || member.Args[0].Name != "all" || member.Args[1].Name != "one" {
		t.Fatalf("Queries did not keep its item and arguments: %+v", member)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Queries accepted opt(...), which marks an object property and is no schema")
		}
	}()
	Queries(Opt(Str()), nil)
}

// Proof of the section of a queries member in a remote contract's paths (declare.ts SECTIONS.queries: "state"). Go cannot declare one in a
// remote variant, so the section is reached only through the path function.
func TestAQueriesMemberPathIsInTheStateSection(t *testing.T) {
	if got := memberPath("n", KindRemote, "editor", "notes", MemberKindQueries); got != "n.editor.state.notes" {
		t.Fatalf("path = %s", got)
	}
	if got := memberPath("n", KindLocal, "members", "notes", MemberKindQueries); got != "n.members.notes" {
		t.Fatalf("path = %s", got)
	}
}
