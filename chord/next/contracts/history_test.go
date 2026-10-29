package contracts_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/test/next/contracts/history.test.ts. Upstream's history is a type-level structure
// (Increment, CurrentStep, OlderVersions, StepAt, ChangesAfter, tested with expectTypeOf only); Go does not type it,
// so the cases are runtime tests (ChangesAfter: TestChangesAfter in derived_runtime_test.go). At
// runtime a contract holds its versions as numbers and the changes of each version (contracts.VersionData), so the
// cases below test what the types computed: counting past 9 and 99, the current and older versions, and the changes
// after a version, through contracts.Evolve; plus the runtime forms of MemberNames, VariantsWith and MethodAt.

func TestRuntimeHistory(t *testing.T) {
	empty := func(version float64) *contracts.Contract {
		return local(t, "h", version, noParams(), contracts.Mem("s", contracts.Str()))
	}
	edit := func(contract *contracts.Contract, changes func(e *contracts.LocalEditor) *contracts.LocalEditor) *contracts.Contract {
		return must(contracts.Evolve(contract, changes))
	}
	same := func(e *contracts.LocalEditor) *contracts.LocalEditor { return e }

	t.Run("increments decimal versions", func(t *testing.T) {
		for _, tc := range [][2]float64{{1, 2}, {9, 10}, {19, 20}, {99, 100}, {1099, 1100}} {
			if got := edit(empty(tc[0]), same).Version; float64(got) != tc[1] {
				t.Errorf("%v + 1 = %d", tc[0], got)
			}
		}
	})

	t.Run("finds the current step, older versions and the changes after a version", func(t *testing.T) {
		v9 := empty(9)
		v10 := edit(v9, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("s", contracts.Int()) })
		v11 := edit(v10, func(e *contracts.LocalEditor) *contracts.LocalEditor {
			return e.AddMethod("m", contracts.Str(), contracts.Nul())
		})
		versions := dataOf(t, v11).Versions
		var numbers []int
		for _, version := range versions {
			numbers = append(numbers, version.Version)
		}
		if !reflect.DeepEqual(numbers, []int{9, 10, 11}) {
			t.Fatalf("versions = %v", numbers)
		}
		if got := versions[0].Changes; len(got) != 0 {
			t.Fatalf("a base has no changes: %v", got)
		}
		if got := versions[1].Changes; !reflect.DeepEqual(got, []contracts.Change{{Variant: "members", Member: "s", Side: contracts.SideState}}) {
			t.Fatalf("v10 changes = %v", got)
		}
		if got := versions[2].Changes; !reflect.DeepEqual(got, []contracts.Change{
			{Variant: "members", Member: "m", Side: contracts.SideArgs},
			{Variant: "members", Member: "m", Side: contracts.SideResult},
		}) {
			t.Fatalf("v11 changes = %v", got)
		}
	})
}

func TestMemberHelpers(t *testing.T) {
	contract := remote(t, "n", 1, noParams(),
		variant("editor", state(contracts.Prop("notes", contracts.Str()), contracts.Prop("count", contracts.Int())),
			contracts.Meth("add", contracts.NewMethod(contracts.Str(), contracts.Str())),
			contracts.Meth("clear", contracts.NewMethod())),
		variant("reader", state(contracts.Prop("notes", contracts.Str())),
			contracts.Meth("add", contracts.NewMethod(contracts.Str(), contracts.Str()))))
	variants := currentVariants(t, contract)

	if got := contracts.MemberNames(variants, contracts.MemberSideState); !reflect.DeepEqual(got, []string{"notes", "count"}) {
		t.Errorf("state names = %v", got)
	}
	if got := contracts.MemberNames(variants, contracts.MemberSideMethods); !reflect.DeepEqual(got, []string{"add", "clear"}) {
		t.Errorf("method names = %v", got)
	}
	if got := contracts.VariantsWith(variants, "add"); !reflect.DeepEqual(got, []string{"editor", "reader"}) {
		t.Errorf("variants with add = %v", got)
	}
	if got := contracts.VariantsWith(variants, "clear"); !reflect.DeepEqual(got, []string{"editor"}) {
		t.Errorf("variants with clear = %v", got)
	}
	if got := contracts.VariantsWith(variants, "missing"); len(got) != 0 {
		t.Errorf("variants with missing = %v", got)
	}
	method, ok := contracts.MethodAt(variants, "add")
	if !ok || stringify(t, method.Args.Normal) != `{"type":"string"}` {
		t.Errorf("MethodAt(add) = %+v, %v", method, ok)
	}
	if _, ok := contracts.MethodAt(variants, "notes"); ok {
		t.Error("a state member is no method")
	}
}
