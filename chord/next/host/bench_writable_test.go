package host

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Own benchmark (no Pi file; the shape of bench_calls_test.go): what Writable.Set costs. Pi's `set` runs on the one event
// loop and its cost guard makes setting the value it has cost one comparison (writable.ts keeps a closure out of `set` for
// that reason); the Go form enters a section of the owner's loop for a Set from outside one (state.SectionOwner), so the
// rows are: a Set inside a section (a method's handler; the case of every Set a p2 plugin makes), the same value inside a
// section, and a Set from a goroutine in no section (a native's opener or goroutine).
// Run by hand: go test -run '^$' -bench BenchmarkWritableSet -benchmem -count 10 ./chord/next/host/

var benchWritable = mustContract(contracts.Local("bench.writable", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("n", contracts.Int()),
		// sets sets the writable that many times in one call (the host's section), alternating two values.
		contracts.Mem("sets", contracts.NewMethod(contracts.Int())),
		// same sets the value it has that many times.
		contracts.Mem("same", contracts.NewMethod(contracts.Int())),
	},
}))

func BenchmarkWritableSet(b *testing.B) {
	var writable state.Writable[any]
	host := newHost(b, HostOptions{OnError: func(err HostError) { b.Errorf("onError %s", err.Kind) }})
	mustLoad(b)(host.Load([]HostFacet{{Name: "@bench/writable", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(benchWritable, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			writable = self.Writable(0.0)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"n": self.Derive([]any{writable}, func(values []any) (delta.JsonValue, error) { return values[0].(float64), nil }),
				},
				Methods: map[string]contracts.MethodFunction{
					"sets": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
						for i := range int(args.(float64)) {
							if err := writable.Set(float64(i%2 + 1)); err != nil {
								return nil, err
							}
						}
						return nil, nil
					},
					"same": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
						for range int(args.(float64)) {
							if err := writable.Set(float64(1)); err != nil {
								return nil, err
							}
						}
						return nil, nil
					},
				},
			}}, nil
		}, ServeOptions{})
	})}}, background))
	object, err := host.Get(background, benchWritable, jsonObject(), GetOptions{})
	must(b, err)
	watch, err := object.Member("n").Watch(background)
	must(b, err)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
	b.Cleanup(func() {
		_, _ = watch.Stop()
		must(b, host.Close(background))
	})

	b.Run("set in a section", func(b *testing.B) {
		b.ReportAllocs()
		if _, err := object.Call("sets", float64(b.N), background); err != nil {
			b.Fatal(err)
		}
	})
	b.Run("same value in a section", func(b *testing.B) {
		if _, err := object.Call("sets", float64(1), background); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		if _, err := object.Call("same", float64(b.N), background); err != nil {
			b.Fatal(err)
		}
	})
	b.Run("set outside a section", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if err := writable.Set(float64(i%2 + 1)); err != nil {
				b.Fatal(err)
			}
		}
	})
}
