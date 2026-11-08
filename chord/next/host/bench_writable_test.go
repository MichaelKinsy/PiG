package host

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

var benchWritableContract = mustContract(contracts.Local("bench.writable", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("v", contracts.Int()), contracts.Mem("touch", contracts.NewMethod())},
}))

// BenchmarkWritableSetInSection is Writable.Set inside a section (a method's own code), watched, one commit per Set.
//
// A Root's body is outside a section (as an `await` yields the loop), so each Set there enters one: that cost is the price
// of setting a writable from a goroutine of no section safely. BenchmarkWritableSetInNestedSection is a Set from code
// that runs in a section already (a method handler, a routine): it enters none.
func BenchmarkWritableSetInSection(b *testing.B) { benchWritableSet(b, false) }

func BenchmarkWritableSetInNestedSection(b *testing.B) { benchWritableSet(b, true) }

func benchWritableSet(b *testing.B, nested bool) {
	var writable state.Writable[any]
	host := newHost(b, HostOptions{OnError: func(HostError) {}})
	mustLoad(b)(host.Load([]HostFacet{{Name: "@bench/writable", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(benchWritableContract, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			writable = self.Writable(float64(0))
			member := self.Derive([]any{writable}, func(values []any) (delta.JsonValue, error) { return values[0], nil })
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"v": member},
				Methods: map[string]contracts.MethodFunction{
					"touch": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
				},
			}}, nil
		}, ServeOptions{})
	})}}, background))
	reference, err := host.Get(background, benchWritableContract, jsonObject(), GetOptions{})
	must(b, err)
	watch, err := reference.Member("v").Watch(background)
	must(b, err)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
	b.Cleanup(func() {
		_, _ = watch.Stop()
		reference.Release()
		must(b, host.Close(background))
	})
	b.ReportAllocs()
	b.ResetTimer()
	_, err = host.Root(func(Ctx) (any, error) {
		var failure error
		loop := func() {
			for i := 0; i < b.N && failure == nil; i++ {
				failure = writable.Set(float64(i + 1))
			}
		}
		if nested {
			host.(*chordHost).enter(loop)
		} else {
			loop()
		}
		return nil, failure
	}, background, RootOptions{})
	must(b, err)
}
