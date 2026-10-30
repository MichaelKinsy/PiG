package host

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/test/next/bench/registries.ts: slice 8's registries and writables: a `writable.set` delivered to
// one watch, a set of the same value (one comparison), and one contribution of N (10, 100) changed while the registry is
// watched (one table build and one compose per set, over N entries). Pi reports the median µs per operation over bursts,
// heap allocated per operation and the registry's compose count per set (the cost guard: one); the Go form is
// `testing.B`: ns/op and allocs/op, and the compose count per set as the metric `composes/set`.
// Run by hand (not in CI): go test -run '^$' -bench BenchmarkRegistries ./chord/next/host/

var benchSum = func() registries.Registry[any, any] {
	registry, err := registries.DefineRegistry[any, any]("bench.sum", registries.DefineRegistryOptions{Version: 1})
	if err != nil {
		panic(err)
	}
	return registry
}()

// benchRegistry is a host with contributors host-lifetime objects each contributing a writable number to `Sum`, watched.
type benchRegistry struct {
	host      Host
	writables []state.Writable[any]
	watch     chord.NativeWatchHandle[any]
	telemetry *telemetry.InMemoryTelemetryContext
}

// composes is the registry composes counted so far.
func (r *benchRegistry) composes() float64 {
	total := 0.0
	for _, measurement := range r.telemetry.GetMetrics() {
		if measurement.Name == "chord.registry.composes" {
			total += measurement.Value
		}
	}
	return total
}

func benchRegistryOf(b *testing.B, contributors int) *benchRegistry {
	b.Helper()
	recorder := telemetry.NewInMemoryTelemetryContext()
	result := &benchRegistry{telemetry: recorder}
	var mu sync.Mutex
	types := make([]*contracts.Contract, contributors)
	for i := range types {
		types[i] = mustContract(contracts.Local("bench.contributor-"+strconv.Itoa(i), contracts.LocalDeclaration{
			Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
		}))
	}
	result.host = newHost(b, HostOptions{Telemetry: recorder, OnError: func(err HostError) { b.Errorf("onError %s", err.Kind) }})
	must(b, result.host.Load([]HostFacet{{Name: "@bench/registries", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		sum := registries.RegistryProvider[any, any]{Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
			total := 0.0
			for _, entry := range table {
				total += entry.Value.(float64)
			}
			return total, nil
		}}
		if err := h.Serve(benchSum, sum, ServeOptions{}); err != nil {
			return err
		}
		for _, typ := range types {
			if err := h.Serve(typ, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				value := self.Writable(0.0)
				mu.Lock()
				result.writables = append(result.writables, value)
				mu.Unlock()
				self.(StoreSelf).Contribute(benchSum, value)
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{
						"n": self.Derive([]any{value}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
					},
				}}, nil
			}, ServeOptions{}); err != nil {
				return err
			}
		}
		return nil
	})}}, background))
	value, err := result.host.Registry(benchSum, background, GetOptions{})
	must(b, err)
	result.watch, err = value.Watch(background)
	must(b, err)
	result.watch.Start(func(context.Context, chord.NativeWatchCall[any]) error { return nil })
	return result
}

func BenchmarkRegistries(b *testing.B) {
	b.Run("writable.set, one watch", func(b *testing.B) {
		one := benchRegistryOf(b, 1)
		writable := one.writables[0]
		watch, err := writable.Watch(background)
		must(b, err)
		watch.Start(func(context.Context, chord.NativeWatchCall[any]) error { return nil })
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if err := writable.Set(float64(i + 1)); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		_, _ = watch.Stop()
		_, _ = one.watch.Stop()
		must(b, one.host.Close(background))
	})
	b.Run("writable.set of the same value", func(b *testing.B) {
		one := benchRegistryOf(b, 1)
		writable := one.writables[0]
		// The watches' backlogs dropped first: what follows measures the set alone.
		_, _ = one.watch.Stop()
		must(b, writable.Set(-1.0))
		b.ReportAllocs()
		for b.Loop() {
			if err := writable.Set(-1.0); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		must(b, one.host.Close(background))
	})
	b.Run("an empty operation (the loop's own cost)", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
		}
	})
	for _, contributors := range []int{10, 100} {
		b.Run("one of "+strconv.Itoa(contributors)+" contributions set, registry watched", func(b *testing.B) {
			registry := benchRegistryOf(b, contributors)
			target := registry.writables[contributors/2]
			before := registry.composes()
			b.ReportAllocs()
			sets := 0
			for i := 0; b.Loop(); i++ {
				if err := target.Set(float64(i + 1)); err != nil {
					b.Fatal(err)
				}
				sets++
			}
			b.StopTimer()
			b.ReportMetric((registry.composes()-before)/float64(max(sets, 1)), "composes/set")
			_, _ = registry.watch.Stop()
			must(b, registry.host.Close(background))
		})
	}
}
