package host

import (
	"context"
	"math"
	"runtime"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/test/bench/shared-holds.ts (Pi 47e9d74f9186, changed again by 90ed3e3b14b5): benchmark of remote
// references sharing one wire hold (decision "References share one wire hold"): a first reference over a memory link
// got and released (a hold, its `bound`, its `release`); a reference joining a held object got and released (no
// message); a commit fanned out to ten references of one object (time, bytes on the wire, encodes); and the heap
// retained per reference held. Pi prints one markdown table (time and observed heap growth per step, medians of seven
// bursts of 2,000 steps after a warm-up of 200; the server's bytes per step); the Go form is one sub-benchmark per row,
// the time and heap columns Go's own (`b.ReportAllocs`: bytes allocated per step, where 90ed3e3b14b5 reports heap
// growth, `heapUsed` after a burst minus before it), "server-B/step" and "encodes/step" metrics.
// Run by hand (not in CI): go test -run '^$' -bench BenchmarkSharedHolds -benchtime=2000x ./chord/next/host/
//
// Three of the script's printed columns do not depend on the machine, and Pi records them in docs/benchmarks.md
// ("Shared holds (2026-11-13)", column "After"): the server's bytes per step of the first three rows (10 B, 0 B, 23 B)
// and the encodes of a commit to ten references (1). Those are checked by
// TestBenchSharedHoldsServerBytesAndEncodesPerStep on every run, over the script's own number of steps.

const (
	// benchSharedSteps is the steps the script's byte columns are divided by: its warm-up and its RUNS bursts of STEPS.
	benchSharedSteps = 200 + 7*2_000
	// benchSharedFanOut is the script's FAN_OUT.
	benchSharedFanOut = 10
	// benchSharedRetained is the script's RETAINED.
	benchSharedRetained = 1_000
)

// benchSharedCounter is a remote counter: its count, and a method that adds one.
var benchSharedCounter = mustContract(contracts.Remote("bench.shared-counter", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	State: []contracts.RemoteStateEntry{contracts.StateEntry("count", contracts.Int())},
		Methods: []contracts.RemoteMethodEntry{contracts.Meth("bump", contracts.NewMethod())},
}))

var benchSharedCount = exDefineState("count", documents.StateOptions{
	Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
})

func benchSharedFacet() Facet {
	return DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(benchSharedCounter, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.MembersImplementation, error) {
			count := stateOf(self, benchSharedCount)
			return contracts.MembersImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"count": self.Derive([]any{count.s}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("n"), nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return nil, count.Change(func(draft *delta.Object) { _ = draft.Set("n", draft.Get("n").(float64)+1) }, ctx)
					},
				},
			}, nil
		}, ServeOptions{})
	})
}

// benchSharedSent is Pi's `counted(link, sent)`: link with the bytes it sends added up. With decode it also counts the
// `state` messages it sends and the hold entries they carry (the binary codec is the wire's).
type benchSharedSent struct {
	link.Link
	decode bool

	mu              sync.Mutex
	bytes           int
	states, entries int
}

func (s *benchSharedSent) Send(message []byte) {
	s.mu.Lock()
	s.bytes += len(message)
	if s.decode {
		if decoded, err := link.BinaryCodec.Decode(message); err == nil {
			if state, ok := decoded.(*link.StateMessage); ok {
				s.states++
				s.entries += len(state.Holds)
			}
		}
	}
	s.mu.Unlock()
	s.Link.Send(message)
}

// counts is the bytes sent so far, the `state` messages among them and their hold entries.
func (s *benchSharedSent) counts() (bytes, states, entries int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytes, s.states, s.entries
}

// benchSharedRig is Pi's `linked()`: a server and a client over a memory link; sent counts the bytes the server sends;
// its adapter counts encodes.
type benchSharedRig struct {
	server, client Host
	sent           *benchSharedSent
	recorder       *telemetry.InMemoryTelemetryContext
}

func benchSharedLinked(tb testing.TB, decode bool) *benchSharedRig {
	tb.Helper()
	rig := &benchSharedRig{recorder: telemetry.NewInMemoryTelemetryContext()}
	rig.server = newHost(tb, HostOptions{
		Address: new(Server.Address(nil)), Principal: serverPrincipal(), Telemetry: rig.recorder, OnError: func(HostError) {},
	})
	mustLoad(tb)(rig.server.Load([]HostFacet{{Name: "@bench/app", Facet: benchSharedFacet()}}, background))
	rig.client = newHost(tb, HostOptions{OnError: func(HostError) {}})
	mustLoad(tb)(rig.client.Load(nil, background))
	a, b := link.MemoryLinkPair()
	rig.sent = &benchSharedSent{Link: a, decode: decode}
	must(tb, rig.server.Accept(rig.sent, LinkOptions{Principal: ownerPrincipal()}, background))
	must(tb, rig.client.Connect(b, LinkOptions{Principal: serverPrincipal()}, background))
	return rig
}

// encodes is the server's `chord.link.encodes` so far.
func (r *benchSharedRig) encodes() float64 {
	total := 0.0
	for _, measurement := range r.recorder.GetMetrics() {
		if measurement.Name == "chord.link.encodes" {
			total += measurement.Value
		}
	}
	return total
}

// get is `await client.get(Counter, { id }, context)`.
func (r *benchSharedRig) get(tb testing.TB, id string) *Reference {
	ref, err := r.client.Get(background, benchSharedCounter, jsonObject("id", id), GetOptions{})
	if err != nil {
		tb.Fatal(err)
	}
	return ref
}

// benchSharedFan is the fan-out row's rig: FAN_OUT references of one object, each watched; a step is one commit (with
// its call) seen by every one of them.
type benchSharedFan struct {
	*benchSharedRig
	refs    []*Reference
	changed chan struct{}
	target  float64
	stop    []func()
}

func benchSharedFanOutOf(tb testing.TB, decode bool) *benchSharedFan {
	tb.Helper()
	fan := &benchSharedFan{benchSharedRig: benchSharedLinked(tb, decode), changed: make(chan struct{}, 1)}
	for range benchSharedFanOut {
		ref := fan.get(tb, "a")
		fan.refs = append(fan.refs, ref)
		watch, err := ref.Member("count").Watch(background)
		must(tb, err)
		// Pi's step yields with setImmediate until every reference has the value; here every frame wakes the step.
		watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
			select {
			case fan.changed <- struct{}{}:
			default:
			}
			return nil
		})
		fan.stop = append(fan.stop, func() { _, _ = watch.Stop() })
	}
	fan.target = exValue(tb, fan.refs[0], "count").(float64)
	return fan
}

// step is one commit: `target++; await refs[0].bump(context)`, then turns until every reference has the count.
func (f *benchSharedFan) step(tb testing.TB) {
	f.target++
	if _, err := f.refs[0].Call("bump", nil, background); err != nil {
		tb.Fatal(err)
	}
	for !f.arrived(tb) {
		<-f.changed
	}
}

func (f *benchSharedFan) arrived(tb testing.TB) bool {
	for _, ref := range f.refs {
		if exValue(tb, ref, "count") != f.target {
			return false
		}
	}
	return true
}

// close is the script's end of the row: the watches stopped, the references released.
func (f *benchSharedFan) close() {
	for _, stop := range f.stop {
		stop()
	}
	for _, ref := range f.refs {
		ref.Release()
	}
}

// benchSharedRound is Math.round of a non-negative value: what the script prints for bytes.
func benchSharedRound(value float64) float64 { return math.Floor(value + 0.5) }

func BenchmarkSharedHolds(b *testing.B) {
	// A first reference, got and released: its own hold each time.
	b.Run("first reference: get and release", func(b *testing.B) {
		rig := benchSharedLinked(b, false)
		id := 0
		step := func() {
			rig.get(b, strconv.Itoa(id%64)).Release()
			id++
		}
		for range 200 {
			step()
		}
		before, _, _ := rig.sent.counts()
		steps := 0
		b.ReportAllocs()
		for b.Loop() {
			step()
			steps++
		}
		after, _, _ := rig.sent.counts()
		b.ReportMetric(float64(after-before)/float64(steps), "server-B/step")
	})

	// A reference to an object another reference holds, got and released.
	b.Run("joining reference: get and release", func(b *testing.B) {
		rig := benchSharedLinked(b, false)
		kept := rig.get(b, "a")
		defer kept.Release()
		for range 200 {
			rig.get(b, "a").Release()
		}
		before, _, _ := rig.sent.counts()
		steps := 0
		b.ReportAllocs()
		for b.Loop() {
			rig.get(b, "a").Release()
			steps++
		}
		after, _, _ := rig.sent.counts()
		b.ReportMetric(float64(after-before)/float64(steps), "server-B/step")
	})

	// One commit seen by every one of FAN_OUT references of one object (each awaits its value).
	b.Run("a commit to 10 references of one object (with its call)", func(b *testing.B) {
		fan := benchSharedFanOutOf(b, false)
		defer fan.close()
		for range 200 {
			fan.step(b)
		}
		bytes, _, _ := fan.sent.counts()
		encoded := fan.encodes()
		steps := 0
		b.ReportAllocs()
		for b.Loop() {
			fan.step(b)
			steps++
		}
		after, _, _ := fan.sent.counts()
		b.ReportMetric(float64(after-bytes)/float64(steps), "server-B/step")
		b.ReportMetric((fan.encodes()-encoded)/float64(steps), "encodes/step")
	})

	// The heap a held reference retains, RETAINED of them to one object (after the first).
	b.Run("1000 references held: heap per reference", func(b *testing.B) {
		var per float64
		trials := 0
		for b.Loop() {
			b.StopTimer()
			rig := benchSharedLinked(b, false)
			kept := rig.get(b, "a")
			before := benchSettledHeap()
			refs := make([]*Reference, 0, benchSharedRetained)
			for range benchSharedRetained {
				refs = append(refs, rig.get(b, "a"))
			}
			after := benchSettledHeap()
			per += (float64(after) - float64(before)) / benchSharedRetained
			trials++
			for _, ref := range refs {
				ref.Release()
			}
			kept.Release()
			runtime.KeepAlive(refs)
			must(b, rig.client.Close(background))
			must(b, rig.server.Close(background))
			b.StartTimer()
		}
		b.ReportMetric(per/float64(max(trials, 1)), "heap-B/reference")
	})
}

// The machine-independent values the script prints, as Pi records them (packages/chord/docs/benchmarks.md at
// 90ed3e3b14b5, "Shared holds (2026-11-13)", column "After": "10 B", "0 B", "23 B, 1 encode"; and under the table: "A
// joining reference sends nothing", "A commit to ten references is one hold entry and one encode in its `state`
// message instead of ten"), each over the script's steps (its warm-up of 200 and seven bursts of 2,000). The column
// "Before" (every reference its own hold: 10 B for a join, 178 B and 10 encodes for the commit) is what the host of
// the commit before measures.
func TestBenchSharedHoldsServerBytesAndEncodesPerStep(t *testing.T) {
	t.Run("first reference: get and release", func(t *testing.T) {
		rig := benchSharedLinked(t, false)
		before, _, _ := rig.sent.counts()
		for id := range benchSharedSteps {
			rig.get(t, strconv.Itoa(id%64)).Release()
		}
		// Everything the server sends for these steps is on the link once the client is told its last `bound`; the
		// release's own answer, if any, is in a later turn of the server.
		flushed(rig.server)
		after, _, _ := rig.sent.counts()
		if got := benchSharedRound(float64(after-before) / benchSharedSteps); got != 10 {
			t.Fatalf("server's bytes per step = %v (%d B over %d steps), want 10", got, after-before, benchSharedSteps)
		}
	})

	t.Run("joining reference: get and release", func(t *testing.T) {
		rig := benchSharedLinked(t, false)
		kept := rig.get(t, "a")
		defer kept.Release()
		before, _, _ := rig.sent.counts()
		for range benchSharedSteps {
			rig.get(t, "a").Release()
		}
		flushed(rig.client)
		flushed(rig.server)
		after, _, _ := rig.sent.counts()
		// Pi prints the rounded quotient, 0 B; its text says "sends nothing": not one byte over all the steps.
		if after != before {
			t.Fatalf("server's bytes for %d joins = %d, want 0", benchSharedSteps, after-before)
		}
	})

	t.Run("a commit to 10 references of one object (with its call)", func(t *testing.T) {
		fan := benchSharedFanOutOf(t, true)
		defer fan.close()
		bytes, states, entries := fan.sent.counts()
		encoded := fan.encodes()
		for range benchSharedSteps {
			fan.step(t)
		}
		flushed(fan.server)
		after, statesAfter, entriesAfter := fan.sent.counts()
		if got := benchSharedRound(float64(after-bytes) / benchSharedSteps); got != 23 {
			t.Errorf("server's bytes per commit = %v (%d B over %d steps), want 23", got, after-bytes, benchSharedSteps)
		}
		if got := (fan.encodes() - encoded) / benchSharedSteps; got != 1 {
			t.Errorf("encodes per commit = %v, want 1", got)
		}
		// One message either way; one hold entry in it, not ten.
		if statesAfter-states != benchSharedSteps || entriesAfter-entries != benchSharedSteps {
			t.Errorf("%d commits travelled as %d `state` messages with %d hold entries, want one message with one entry each",
				benchSharedSteps, statesAfter-states, entriesAfter-entries)
		}
	})
}
