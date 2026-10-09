package durable

import "testing"

// Pi packages/durable/src/types.ts:69-79 CommonDocDefinition.initial(): T is the singleton document's first value; DocFamilyDefinition (types.ts:84-89) is
// `Omit<CommonDocDefinition<T>, "initial">` with `initial(seed: I): T`, run only for the seed of an absent member.
// mutation-checked: DefineDoc ignoring CommonDocDefinition.Initial, or the family erasure dropping the seed, fails this test.
func TestCommonDocDefinitionInitialIsTheSingletonsFirstValue(t *testing.T) {
	type state struct {
		N int `json:"n"`
	}
	singleton := DefineDoc(DocDefinition[state]{
		CommonDocDefinition: CommonDocDefinition[state]{Kind: "t.single", Version: 1, Initial: func() state { return state{N: 7} }},
		DocumentSemantics:   DocumentSemantics{Scope: "session"},
	})
	got, err := singleton.AnyDefinition().Initial(nil)
	if err != nil || got.Value("n") != float64(7) {
		t.Fatalf("singleton initial = %v, %v, want n=7", got, err)
	}
	family := DefineDocFamily(DocFamilyDefinition[state, int]{
		Kind: "t.family", Version: 1, Family: true, DocumentSemantics: DocumentSemantics{Scope: "session"},
		Initial: func(seed int) state { return state{N: seed * 2} },
	})
	got, err = family.AnyDefinition().Initial(JsonValue(float64(21)))
	if err != nil || got.Value("n") != float64(42) {
		t.Fatalf("family initial(21) = %v, %v, want n=42", got, err)
	}
}
