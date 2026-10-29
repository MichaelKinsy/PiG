package contracts_test

import (
	"reflect"
	"testing"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/contracts/contract.test.ts.
//
// The "derived types" cases that assert Implementation<C> and ParamsOf with expectTypeOf are not designed out: the Go
// forms (VariantImplementation, MethodFunction, *delta.JsonObject params) carry no per-contract type, so they are
// ported as runtime tests over the contract data in derived_runtime_test.go (TestDerivedShapes).

func notesContract(t testing.TB) *contracts.Contract {
	t.Helper()
	return remote(t, "notes", 1, props(contracts.Prop("session", contracts.Str()), contracts.Prop("page", contracts.Int())),
		variant("editor", state(contracts.Prop("notes", contracts.Arr(contracts.Str()))),
			contracts.Meth("add", contracts.NewMethod(contracts.Obj(contracts.Prop("text", contracts.Str())), contracts.Str())),
			contracts.Meth("clear", contracts.NewMethod())),
		variant("reader", state(contracts.Prop("notes", contracts.Arr(contracts.Str())))))
}

func TestContractHandles(t *testing.T) {
	notes := notesContract(t)

	t.Run("carry kind, name, version and fingerprint only", func(t *testing.T) {
		var fields []string
		kind := reflect.TypeOf(*notes)
		for field := range kind.Fields() {
			if field.IsExported() {
				fields = append(fields, field.Name)
			}
		}
		if !reflect.DeepEqual(fields, []string{"Kind", "Name", "Version", "Fingerprint"}) {
			t.Fatalf("exported fields = %v", fields)
		}
		if notes.Kind != contracts.KindRemote || notes.Name != "notes" || notes.Version != 1 {
			t.Fatalf("handle = %+v", notes)
		}
	})

	t.Run("are branded: copies and hand-built values are refused", func(t *testing.T) {
		refusal := "notes: not a declared contract (spread and hand-built copies are refused)"
		copied := *notes
		_, err := contracts.ContractDataOf(&copied)
		wantError(t, err, refusal)
		_, err = contracts.Evolve(&copied, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e })
		wantError(t, err, refusal)
		_, err = contracts.CheckAccepts(&copied, nil)
		wantError(t, err, refusal)
		_, err = data.BindProvider(&copied, nil)
		wantError(t, err, refusal)
		_, err = data.CheckParams(&copied, delta.JsonObjectOf("session", "s", "page", 1.0), chordctx.Background())
		wantError(t, err, refusal)
		built := &contracts.Contract{Kind: contracts.KindRemote, Name: "notes", Version: 1, Fingerprint: notes.Fingerprint}
		_, err = contracts.ContractDataOf(built)
		wantError(t, err, refusal)
		if _, err = contracts.ContractDataOf(notes); err != nil {
			t.Fatalf("the declared contract was refused: %v", err)
		}
	})

	t.Run("are collected when dropped, with their data", func(t *testing.T) {
		contract, contractData := func() (weak.Pointer[contracts.Contract], weak.Pointer[contracts.ContractData]) {
			dropped := local(t, "gc", 1, noParams(), contracts.Mem("s", contracts.Str()))
			return weak.Make(dropped), weak.Make(dataOf(t, dropped))
		}()
		nexttest.ExpectCollected(t, contract)
		nexttest.ExpectCollected(t, contractData)
	})
}

func TestContractTakesNoArgs(t *testing.T) {
	t.Run("takesNoArgs: exactly the methods whose argument schema is null", func(t *testing.T) {
		counts := local(t, "counts", 1, noParams(),
			contracts.Mem("reset", contracts.NewMethod()),
			contracts.Mem("total", contracts.NewMethod(contracts.TSchema{}, contracts.Int())),
			contracts.Mem("set", contracts.NewMethod(contracts.Int())),
			contracts.Mem("maybe", contracts.NewMethod(contracts.Union([]contracts.TSchema{contracts.Nul(), contracts.Str()}))))
		methods := currentVariants(t, counts).Value(contracts.LocalVariant).Methods
		type result struct {
			name string
			no   bool
		}
		var got []result
		for name, method := range methods.All() {
			got = append(got, result{name, contracts.TakesNoArgs(method)})
		}
		want := []result{{"reset", true}, {"total", true}, {"set", false}, {"maybe", false}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("takesNoArgs = %v, want %v", got, want)
		}
	})
}

func TestContractVariantNames(t *testing.T) {
	notes := notesContract(t)
	if got := must(contracts.VariantOrder(notes)); !reflect.DeepEqual(got, []string{"editor", "reader"}) {
		t.Fatalf("VariantOrder = %v", got)
	}
	editor := currentVariants(t, notes).Value("editor")
	if got := contracts.VariantNamesOf(editor); !reflect.DeepEqual(got, contracts.VariantNames{State: []string{"notes"}, Methods: []string{"add", "clear"}}) {
		t.Fatalf("VariantNamesOf = %+v", got)
	}
	// Names sort, whatever the declaration order.
	reversed := remote(t, "r", 1, noParams(),
		variant("z", state(contracts.Prop("b", contracts.Str()), contracts.Prop("a", contracts.Str())),
			contracts.Meth("n", contracts.NewMethod()), contracts.Meth("m", contracts.NewMethod())),
		variant("a", nil))
	if got := must(contracts.VariantOrder(reversed)); !reflect.DeepEqual(got, []string{"a", "z"}) {
		t.Fatalf("VariantOrder = %v", got)
	}
	if got := contracts.VariantNamesOf(currentVariants(t, reversed).Value("z")); !reflect.DeepEqual(got, contracts.VariantNames{State: []string{"a", "b"}, Methods: []string{"m", "n"}}) {
		t.Fatalf("VariantNamesOf = %+v", got)
	}
}
