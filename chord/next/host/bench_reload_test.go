package host

import (
	"context"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// Ports packages/chord/test/bench/reload.ts: `host.reload` (slice 7b §11): one plugin of 20 reloaded on a host with
// 1,000 live objects, as a pure swap and as a native restart. Per case: the reload's time until it returns (staging, commit,
// every fate started and every swap checked: the metric `resolve-ns/reload`), the time until every object of the plugin is
// live again (ns/op), and the heap retained after the reloads against after the first (`heap-bytes-above-first`: old
// attempts and facets collected). Pi reports medians over the reloads after a warm-up of two; the Go form is `testing.B`:
// each iteration is one reload, and the metrics are means over the iterations.
// Run by hand (not in CI): go test -run '^$' -bench BenchmarkReload ./chord/next/host/

const (
	benchReloadPlugins = 20
	benchReloadObjects = 1_000
)

var benchReloadCounter = func() *documents.StateDefinition {
	definition, err := documents.DefineState("bench.counter", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("n", float64(0)) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// benchReloadTypes are the plugins' types: one per plugin, objects with params (each lives while referenced).
var benchReloadTypes = func() []*contracts.Contract {
	types := make([]*contracts.Contract, benchReloadPlugins)
	for i := range types {
		types[i] = mustContract(contracts.Local("bench.item"+strconv.Itoa(i), contracts.LocalDeclaration{
			Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
			Members: []contracts.LocalEntry{
				contracts.Mem("n", contracts.Int()), contracts.Mem("label", contracts.Int()),
				contracts.Mem("bump", contracts.NewMethod(contracts.Schema{}, contracts.Int())),
			},
		}))
	}
	return types
}()

// benchReloadFacet is plugin index's facet at code version code; with native, each object opens a native.
func benchReloadFacet(index, code int, native bool) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(benchReloadTypes[index], func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			counter := self.State(benchReloadCounter).(*documents.Singleton)
			if native {
				self.Open(func(context.Context) (any, error) { return code, nil },
					func(any, NativeCloseReason, context.Context) error { return nil })
			}
			implementation := impl("n", self.Derive([]any{counter}, func(values []any) (delta.JsonValue, error) {
				return values[0].(*delta.JsonObject).Value("n"), nil
			}), "label", constant(float64(code)))
			implementation.Members.Methods = map[string]contracts.MethodFunction{
				"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return counter.Change(ctx, func(draft *delta.Object) (any, error) { return nil, draft.Set("n", 1.0) })
				},
			}
			return implementation, nil
		}, ServeOptions{})
	})
}

// benchReloadHeap is the whole-heap use after full collections.
func benchReloadHeap() uint64 {
	for range 5 {
		time.Sleep(time.Millisecond)
		runtime.GC()
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// benchReload is one case: counts[i] objects per plugin (1,000 in all), plugin 0 reloaded b.N times; native makes plugin 0 a
// native object (restarts) instead of a pure one (swaps).
func benchReload(b *testing.B, counts []int, native bool) {
	host := newHost(b, HostOptions{OnError: func(err HostError) { b.Errorf("onError %s", err.Kind) }})
	defer closeHosts(b, host)
	facets := make([]Facet, benchReloadPlugins)
	set := func() []HostFacet {
		loaded := make([]HostFacet, benchReloadPlugins)
		for i, facet := range facets {
			loaded[i] = HostFacet{Name: "@bench/p" + strconv.Itoa(i), Facet: facet}
		}
		return loaded
	}
	for i := range facets {
		facets[i] = benchReloadFacet(i, 0, native && i == 0)
	}
	mustLoad(b)(host.Load(set(), background))
	var refs []*Reference
	for index, count := range counts {
		for id := range count {
			refs = append(refs, mustGet(b, host, benchReloadTypes[index], jsonObject("id", float64(id))))
		}
	}
	reloaded := refs[:counts[0]]
	var resolving time.Duration
	var afterFirst uint64
	b.ResetTimer()
	for round := 1; round <= b.N; round++ {
		facets[0] = benchReloadFacet(0, round, native)
		started := time.Now()
		_, err := host.Reload(set(), background, ReloadOptions{})
		must(b, err)
		resolving += time.Since(started)
		for {
			ready := true
			for _, ref := range reloaded {
				if refStateOf(ref).Kind != RefStateLive || memberOf(b, ref, "label") != float64(round) {
					ready = false
					break
				}
			}
			if ready {
				break
			}
			turn(host)
		}
		if round == 1 {
			b.StopTimer()
			afterFirst = benchReloadHeap()
			b.StartTimer()
		}
	}
	b.StopTimer()
	afterAll := benchReloadHeap()
	b.ReportMetric(float64(resolving.Nanoseconds())/float64(b.N), "resolve-ns/reload")
	b.ReportMetric(float64(int64(afterAll)-int64(afterFirst)), "heap-bytes-above-first")
}

func BenchmarkReload(b *testing.B) {
	spread := make([]int, benchReloadPlugins)
	for i := range spread {
		spread[i] = benchReloadObjects / benchReloadPlugins
	}
	concentrated := make([]int, benchReloadPlugins)
	concentrated[0] = benchReloadObjects
	b.Run("swap, 50 of 1,000 objects in the reloaded plugin", func(b *testing.B) { benchReload(b, spread, false) })
	b.Run("swap, all 1,000 objects in the reloaded plugin", func(b *testing.B) { benchReload(b, concentrated, false) })
	b.Run("restart, 50 of 1,000 objects in the reloaded plugin", func(b *testing.B) { benchReload(b, spread, true) })
	b.Run("restart, all 1,000 objects in the reloaded plugin", func(b *testing.B) { benchReload(b, concentrated, true) })
}
