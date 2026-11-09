package host

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/test/store/refusal.test.ts describe("Chord's owners"): "an int out of range fails only its
// transaction; the object stays live". The classification cases of that file are in chord/next/store/refusal_test.go.
func TestRefusalAnIntOutOfRangeFailsOnlyItsTransactionAndTheObjectStaysLive(t *testing.T) {
	counts, err := store.DefineTable("counts", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("n", store.Int())}, Key: []string{"id"}})
	must(t, err)
	counter := mustContract(contracts.Local("app.counter", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("bump", contracts.NewMethod(contracts.Obj(contracts.Prop("by", contracts.Int())))),
			contracts.Mem("reset", contracts.NewMethod()),
		},
	}))
	chordStore := memoryStore(t)
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(counter, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			table := self.(StoreSelf).Table(counts)
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"bump": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					by := int64(args.(*delta.JsonObject).Value("by").(float64))
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						return nil, tx.Update(table, store.Row{"id": "a"}, store.Row{"n": store.Add(store.NewColumnRef("n"), by)})
					}, ctx)
					return nil, err
				},
				"reset": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						return nil, tx.Put(table, store.Row{"id": "a", "n": int64(9007199254740991)})
					}, ctx)
					return nil, err
				},
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Store: chordStore})
	defer closeHosts(t, host)
	ref := mustGet(t, host, counter, jsonObject())
	if _, err := ref.Call("reset", nil, background); err != nil {
		t.Fatalf("reset: %v", err)
	}
	_, err = ref.Call("bump", jsonObject("by", float64(1)), background)
	wantChordError(t, err, data.CodeInvalid, data.ReasonArgs, "")
	if state := ref.State().Value(); state.Kind != RefStateLive {
		t.Fatalf("state after the refused bump = %+v, want live", state)
	}
	if errs.Len() != 0 {
		t.Fatalf("background errors = %v, want none", errs.All())
	}
	if _, err := ref.Call("bump", jsonObject("by", float64(-1)), background); err != nil {
		t.Fatalf("bump by -1: %v", err)
	}
}
