package host

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// Ports packages/chord/test/next/bench/calls.ts: the paths slice 8 touched under every object (no registries involved): a
// local method call (its ctx), a commit through a watched derive (the batch, a derive's compute) and activation churn (an
// object with params made, live and ended per get: its attempt's declarations). Pi reports the median µs per operation
// over bursts and the heap allocated per operation; the Go form is `testing.B`: ns/op and allocs/op (`b.ReportAllocs`)
// per row. Run by hand (not in CI): go test -run '^$' -bench BenchmarkCalls ./chord/next/host/

var benchCounterDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("bench.counter", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

var benchStore = mustContract(contracts.Local("bench.store", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("doubled", contracts.Int()),
		contracts.Mem("bump", contracts.NewMethod()),
		contracts.Mem("set", contracts.NewMethod(contracts.Int())),
	},
}))

var benchItem = mustContract(contracts.Local("bench.item", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
	Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
}))

func BenchmarkCalls(b *testing.B) {
	host := newHost(b, HostOptions{OnError: func(err HostError) { b.Errorf("onError %s", err.Kind) }})
	must(b, host.Load([]HostFacet{{Name: "@bench/calls", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(benchStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			counter := self.State(benchCounterDoc).(*documents.Singleton)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"doubled": self.Derive([]any{counter}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("n").(float64) * 2, nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
					"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := counter.Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("n", args) }, ctx)
						return nil, err
					},
				},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(benchItem, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			id := params.Value("id").(float64)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"n": self.Derive([]any{self.Get(benchStore, jsonObject()).Member("doubled")}, func(values []any) (delta.JsonValue, error) {
						return values[0].(float64) + id, nil
					}),
				},
			}}, nil
		}, ServeOptions{})
	})}}, background))
	store, err := host.Get(benchStore, jsonObject(), background, GetOptions{})
	must(b, err)
	watch, err := store.Member("doubled").Watch(background)
	must(b, err)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
	b.Cleanup(func() {
		_, _ = watch.Stop()
		must(b, host.Close(background))
	})

	b.Run("local method call", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := store.Call("bump", nil, background); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("commit through a watched derive", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if _, err := store.Call("set", float64(i+1), background); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("activation churn (get, live, release)", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			item, err := host.Get(benchItem, jsonObject("id", float64(i)), background, GetOptions{})
			if err != nil {
				b.Fatal(err)
			}
			item.Release()
		}
	})
}
