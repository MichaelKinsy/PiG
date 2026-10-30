package host

import (
	"context"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// Ports packages/chord/test/next/bench/derives.ts: a retirement through derives (slice 6a rule 21): a singleton under a
// diamond of 40 derives (13 layers of two derives joined by a third, 2^13 paths from the top to the bottom), its bottom
// watched; each step retires the document and creates it again in one transaction, so every step carries the `retired`
// flag through the diamond. The flag reaches each derive once per retirement, not once per path. Pi reports the median
// and p95 per step over seven bursts of 1,000 steps; the Go form is one step per `b.N` iteration (ns/op, allocs/op).
// Run by hand (not in CI): go test -run '^$' -bench BenchmarkDerives ./chord/next/host/

const benchDiamondLayers = 13

var benchListDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("bench.list", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("items", contracts.Arr(contracts.Str()))),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("items", []any{}) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

var benchDiamond = mustContract(contracts.Local("bench.diamond", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("bottom", contracts.Int()), contracts.Mem("touch", contracts.NewMethod())},
}))

// benchDiamondOver is the diamond over list: its bottom derive.
func benchDiamondOver(self Self, list *documents.Singleton) chord.Watchable[delta.JsonValue] {
	var previous chord.Watchable[delta.JsonValue] = self.Derive([]any{list}, func(values []any) (delta.JsonValue, error) {
		return float64(len(values[0].(*delta.JsonObject).Value("items").([]any))), nil
	})
	for range benchDiamondLayers {
		left := self.Derive([]any{previous}, func(values []any) (delta.JsonValue, error) { return values[0].(float64) + 1, nil })
		right := self.Derive([]any{previous}, func(values []any) (delta.JsonValue, error) { return values[0].(float64) + 2, nil })
		previous = self.Derive([]any{left, right}, func(values []any) (delta.JsonValue, error) {
			return float64(int(values[0].(float64)+values[1].(float64)) % 1_000), nil
		})
	}
	return previous
}

func BenchmarkDerives(b *testing.B) {
	var (
		list *documents.Singleton
		self Self
	)
	host := newHost(b, HostOptions{OnError: func(HostError) {}})
	must(b, host.Load([]HostFacet{{Name: "@bench/derives", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(benchDiamond, func(_ *delta.JsonObject, declared Self) (contracts.Implementation, error) {
			self = declared
			list = declared.State(benchListDoc).(*documents.Singleton)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"bottom": benchDiamondOver(declared, list)},
				Methods: map[string]contracts.MethodFunction{
					"touch": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
				},
			}}, nil
		}, ServeOptions{})
	})}}, background))
	reference, err := host.Get(benchDiamond, jsonObject(), background, GetOptions{})
	must(b, err)
	watch, err := reference.Member("bottom").Watch(background)
	must(b, err)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
	b.Cleanup(func() {
		_, _ = watch.Stop()
		reference.Release()
		must(b, host.Close(background))
	})
	b.Run("retire + recreate under a "+strconv.Itoa(1+3*benchDiamondLayers)+"-derive diamond", func(b *testing.B) {
		b.ReportAllocs()
		for step := 0; b.Loop(); step++ {
			_, err := self.Tx(func(tx *documents.Transaction) (any, error) {
				if err := tx.Retire(list); err != nil {
					return nil, err
				}
				draft, err := tx.Draft(list)
				if err != nil {
					return nil, err
				}
				_, err = draft.Array("items").Push(strconv.Itoa(step))
				return nil, err
			}, background)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
