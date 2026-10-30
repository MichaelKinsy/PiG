package host

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/test/next/bench/jobs.ts as Go benchmarks, run by hand (not in CI):
//
//	go test -run '^$' -bench BenchmarkJobs -benchmem ./chord/next/host
//
// Persisted jobs (slice 6b §4, §5): a run's start (its record and state created, resolved after the commit) and a state
// change inside `run` (immediate, and deferred), for a persisted job on a SQLite file against the same job in memory. A
// disk commit is not held to the in-memory number: the persisted rows show what a run's records cost on the store.
//
// Go forms. Pi times OPERATIONS operations per burst by hand and prints the median and the p95 of RUNS bursts; the Go
// benchmark times b.N operations per host and reports ns/op (the harness repeats it, `-count` gives the spread, and
// benchstat the median and the p95). The warm-up burst is the harness's own first runs of b.N = 1.

var (
	benchInMemoryProgress = mustDefineState(documents.DefineState("bench.progress", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("done", contracts.Int())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("done", float64(0)) },
	}))
	benchPersistedProgress = mustDefineState(documents.DefineState("bench.progress", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("done", contracts.Int())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("done", float64(0)) },
		Persist: true,
		CheckpointWhen: func(_ *documents.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 64
		},
	}))
	benchInMemoryImport = mustDefineJob(DefineJob("bench.import", JobOptions{
		Version: 1, Args: contracts.Obj(contracts.Prop("path", contracts.Str())),
		Result: contracts.Obj(contracts.Prop("count", contracts.Int())), State: benchInMemoryProgress,
	}))
	benchPersistedImport = mustDefineJob(DefineJob("bench.import", JobOptions{
		Version: 1, Args: contracts.Obj(contracts.Prop("path", contracts.Str())),
		Result: contracts.Obj(contracts.Prop("count", contracts.Int())), State: benchPersistedProgress,
		Persist: &JobPersistence{Resume: ResumeRerun},
	}))
	benchWorkerType = mustContract(contracts.Local("bench.worker", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{},
	}))
)

// parkedRun is where a run's `run` parks: its state, and the gate that ends it.
type parkedRun struct {
	state RunState
	end   func()
}

// benchWorker is a host serving the worker type with the job (persisted on a fresh SQLite file, or in memory), its runs
// parked.
type benchWorker struct {
	jobs   JobHandle
	parked chan parkedRun
	close  func()
}

func servedWorker(b *testing.B, persisted bool) *benchWorker {
	b.Helper()
	var st *store.ChordStore
	if persisted {
		database, err := node.OpenNodeSqliteDatabase(filepath.Join(b.TempDir(), "store.sqlite"), node.NodeSqliteDatabaseOptions{})
		must(b, err)
		opened, err := store.OpenStore(store.SqliteBackend(database), background, store.StoreOptions{})
		must(b, err)
		st = opened.(*store.ChordStore)
	}
	worker := &benchWorker{parked: make(chan parkedRun)}
	options := HostOptions{OnError: func(HostError) {}}
	if st != nil {
		options.Store = st
	}
	host := newJobHost(b, options)
	definition := benchInMemoryImport
	if persisted {
		definition = benchPersistedImport
	}
	must(b, host.Load([]HostFacet{{Name: "@bench/jobs", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(benchWorkerType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			worker.jobs = self.Job(definition, JobImplementation{Run: func(_ any, ctx JobCtx) (any, error) {
				ended := make(chan struct{})
				worker.parked <- parkedRun{state: ctx.State(), end: func() { close(ended) }}
				<-ended
				value, err := ctx.State().Value()
				if err != nil {
					return nil, err
				}
				return jsonObject("count", value.Value("done")), nil
			}})
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	})}}, background))
	reference, err := host.Get(benchWorkerType, jsonObject(), background, GetOptions{})
	must(b, err)
	worker.close = func() {
		reference.Release()
		must(b, host.Close(background))
		if st != nil {
			must(b, st.Close(background))
		}
	}
	return worker
}

// BenchmarkJobsStart is a start: each run's start resolved (record and state committed), its `run` parked, all ended
// after the timed loop.
func BenchmarkJobsStart(b *testing.B) {
	for name, persisted := range map[string]bool{"in memory": false, "persisted": true} {
		b.Run(name, func(b *testing.B) {
			worker := servedWorker(b, persisted)
			var parked []parkedRun
			for index := 0; b.Loop(); index++ {
				if _, err := worker.jobs.Start(jsonObject("path", "p"), background, StartOptions{}); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				parked = append(parked, <-worker.parked)
				b.StartTimer()
			}
			b.StopTimer()
			for _, run := range parked {
				run.end()
			}
			worker.close()
		})
	}
}

// BenchmarkJobsStateChange is a change of one run's state inside `run`: in memory, persisted immediate, and persisted
// deferred (stored by the outcome).
func BenchmarkJobsStateChange(b *testing.B) {
	cases := []struct {
		name      string
		persisted bool
		options   ChangeOptions
	}{
		{"in memory", false, ChangeOptions{}},
		{"persisted, immediate", true, ChangeOptions{}},
		{"persisted, deferred", true, deferredWithin(math.Inf(1))},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			worker := servedWorker(b, c.persisted)
			if _, err := worker.jobs.Start(jsonObject("path", "p"), background, StartOptions{}); err != nil {
				b.Fatal(err)
			}
			run := <-worker.parked
			b.ResetTimer()
			for index := 1; b.Loop(); index++ {
				done := float64(index)
				if _, err := run.state.Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("done", done) }, background, c.options); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			run.end()
			worker.close()
		})
	}
}
