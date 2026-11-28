package host

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/test/bench/events.ts: benchmark of events (decision "Events: fire-and-forget members"): the cost
// of one emit, flushed and delivered to its holders' listeners, with 0, 1, 10 and 70 holders, each a client host over
// its own memory link; with 10 holders of an older version (one mapping per emit); and through a relay (tabs on a
// server, the object on a worker behind a route link). Each step emits one paddle sample and waits until every listener
// got it; with the provider's in-memory telemetry adapter (encodes per emit: its `chord.events.encodes`) and with the
// NOOP adapter (none), and the bytes of one event message. Pi prints one markdown table; the Go form is one
// sub-benchmark per row, the time and heap columns Go's own, "encodes/emit" a metric.
// Run by hand (not in CI): go test -run '^$' -bench BenchmarkEvents -benchtime=2000x ./chord/next/host/
//
// Two of the script's printed values do not depend on the machine, and Pi records them in docs/benchmarks.md ("Events
// (2026-11-11)"): the bytes of one event message (67) and the encodes per emit of each row. Those are checked by
// TestBenchEventsMessageBytesAndEncodesPerEmit on every run.

var benchEventsPaddle = contracts.Obj(
	contracts.Prop("peer", contracts.Str()), contracts.Prop("t", contracts.Num()), contracts.Prop("y", contracts.Num()))

func benchEventsList() []contracts.RemoteEventEntry {
	return []contracts.RemoteEventEntry{contracts.EventEntry("paddle", benchEventsPaddle)}
}

var benchEventsMatch = mustContract(contracts.Remote("bench.match", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Events:      benchEventsList(),
}))

// benchEventsMatchV2: the paddle gains a velocity; version 1 consumers get it mapped away.
var benchEventsMatchV2 = mustContract(contracts.Evolve(benchEventsMatch, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.Event("paddle", contracts.Obj(
		contracts.Prop("peer", contracts.Str()), contracts.Prop("t", contracts.Num()),
		contracts.Prop("y", contracts.Num()), contracts.Prop("v", contracts.Num())))
}))

// benchEventsPlaced is the same object placed on a worker (a session), reached through the server's relay.
var benchEventsPlaced = mustContract(contracts.Remote("bench.placed", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
		return Session.Address(jsonObject("id", params.Value("id"))), nil
	},
	Events: benchEventsList(),
}))

// benchEventsKind is what a run measures: holders of the current version, of an older one, or through a relay.
type benchEventsKind string

const (
	benchEventsCurrent benchEventsKind = "current"
	benchEventsOlder   benchEventsKind = "older"
	benchEventsRelay   benchEventsKind = "relay"
)

// benchEventsProvider is the provider host serving the matches, its sources (the last one emits), and its facet.
type benchEventsProvider struct {
	host    Host
	facet   Facet
	mu      sync.Mutex
	sources []contracts.EventSource[delta.JsonValue]
}

func (p *benchEventsProvider) last() contracts.EventSource[delta.JsonValue] {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sources[len(p.sources)-1]
}

func benchEventsProviderOf(tb testing.TB, address contracts.Address, recorder *telemetry.InMemoryTelemetryContext) *benchEventsProvider {
	tb.Helper()
	provider := &benchEventsProvider{}
	provider.host = newHost(tb, HostOptions{
		Address: &address, Principal: serverPrincipal(), Telemetry: telemetryOf(recorder), OnError: func(HostError) {},
	})
	serve := func(self Self) (contracts.MembersImplementation, error) {
		paddle := self.Events()
		provider.mu.Lock()
		provider.sources = append(provider.sources, paddle)
		provider.mu.Unlock()
		return contracts.MembersImplementation{
			Events: map[string]contracts.EventMember[delta.JsonValue]{"paddle": paddle},
		}, nil
	}
	provider.facet = DefineFacet(func(h FacetHost, _ FacetSelf) error {
		mapping := func(value delta.JsonValue) (delta.JsonValue, error) {
			sample := value.(*delta.JsonObject)
			return jsonObject("peer", sample.Value("peer"), "t", sample.Value("t"), "y", sample.Value("y")), nil
		}
		if err := h.Serve(benchEventsMatchV2, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.MembersImplementation, error) {
			return serve(self)
		}, ServeOptions{Accepts: contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
			Events: []contracts.Entry[contracts.EventMapping]{{Name: "paddle", Value: contracts.EventMapping{Func: mapping}}},
		}}}}); err != nil {
			return err
		}
		return h.Serve(benchEventsPlaced, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.MembersImplementation, error) {
			return serve(self)
		}, ServeOptions{})
	})
	return provider
}

// benchEventsRig is what a run holds: the emitting source, the listeners' count, and the provider's adapter.
type benchEventsRig struct {
	server   *benchEventsProvider
	source   func() contracts.EventSource[delta.JsonValue]
	holders  int
	recorder *telemetry.InMemoryTelemetryContext

	mu       sync.Mutex
	arrived  *sync.Cond
	received int
}

// benchEventsSetUp is holders client hosts each holding the match over its own link, every listener counting; the
// provider with the in-memory adapter (encodes counted), or with none (the NOOP adapter) when noop.
func benchEventsSetUp(tb testing.TB, kind benchEventsKind, holders int, noop bool) *benchEventsRig {
	tb.Helper()
	rig := &benchEventsRig{holders: holders}
	rig.arrived = sync.NewCond(&rig.mu)
	// The NOOP adapter (none) records nothing: its rows show what an emit costs without telemetry.
	if !noop {
		rig.recorder = telemetry.NewInMemoryTelemetryContext()
	}
	rig.server = benchEventsProviderOf(tb, Server.Address(nil), rig.recorder)
	rig.source = rig.server.last
	if kind == benchEventsRelay {
		worker := benchEventsProviderOf(tb, Session.Address(jsonObject("id", "m")), telemetry.NewInMemoryTelemetryContext())
		mustRoute(tb)(rig.server.host.Route(Session, RouteOptions{
			Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
			Resolve: func(contracts.Address, context.Context) (ResolvedLink, error) {
				a, b := link.MemoryLinkPair()
				if err := worker.host.Connect(b, LinkOptions{Principal: serverPrincipal(), Trusted: true}, background); err != nil {
					return ResolvedLink{}, err
				}
				return ResolvedLink{Link: a, Principal: workerPrincipal("m"), Trusted: true}, nil
			},
		}, background))
		mustLoad(tb)(worker.host.Load([]HostFacet{{Name: "@bench/worker", Facet: worker.facet}}, background))
		rig.source = worker.last
	}
	mustLoad(tb)(rig.server.host.Load([]HostFacet{{Name: "@bench/server", Facet: rig.server.facet}}, background))
	typ := benchEventsMatchV2
	switch kind {
	case benchEventsOlder:
		typ = benchEventsMatch
	case benchEventsRelay:
		typ = benchEventsPlaced
	}
	for range holders {
		client := newHost(tb, HostOptions{OnError: func(HostError) {}})
		mustLoad(tb)(client.Load(nil, background))
		a, b := link.MemoryLinkPair()
		must(tb, rig.server.host.Accept(a, LinkOptions{Principal: ownerPrincipal()}, background))
		must(tb, client.Connect(b, LinkOptions{Principal: serverPrincipal()}, background))
		ref, err := client.Get(background, typ, jsonObject("id", "m"), GetOptions{})
		must(tb, err)
		on(tb, ref, "paddle", func(contracts.EventDelivery[delta.JsonValue]) {
			rig.mu.Lock()
			rig.received++
			rig.mu.Unlock()
			rig.arrived.Broadcast()
		}, background)
	}
	if holders == 0 {
		// The object lives while referenced: the server's own reference keeps it, without listeners.
		_, err := rig.server.host.Get(background, benchEventsMatchV2, jsonObject("id", "m"), GetOptions{})
		must(tb, err)
	}
	return rig
}

// step is one step: an emit, then turns until every holder's listener got it.
func (r *benchEventsRig) step(tb testing.TB, index int) {
	r.mu.Lock()
	target := r.received + r.holders
	r.mu.Unlock()
	must(tb, r.source().Emit(jsonObject("peer", "p", "t", float64(index), "y", float64(index)/7, "v", 1.0)))
	flushed(r.server.host)
	r.mu.Lock()
	for r.received < target {
		r.arrived.Wait()
	}
	r.mu.Unlock()
}

// encodes is the provider's `chord.events.encodes` so far (0 with the NOOP adapter).
func (r *benchEventsRig) encodes() float64 {
	if r.recorder == nil {
		return 0
	}
	total := 0.0
	for _, measurement := range r.recorder.GetMetrics() {
		if measurement.Name == "chord.events.encodes" {
			total += measurement.Value
		}
	}
	return total
}

// benchEventsMessageBytes is the bytes of one event message of one paddle sample with the binary codec.
func benchEventsMessageBytes(tb testing.TB) int {
	tb.Helper()
	value := mustJSONPart(tb, jsonObject("peer", "p", "t", 1_762_819_200_123.5, "y", 0.42857142857142855))
	encoded, err := link.BinaryCodec.Encode(&link.EventMessage{Events: []link.WireEvent{{Hold: 0, Member: 0, At: 1_762_819_200_123, Value: value}}})
	must(tb, err)
	return len(encoded)
}

// benchEventsRows are the script's rows, in its order: holders of the current version, of an older one, through a
// relay, then the current version again on the NOOP adapter. encodes is Pi's "Encodes per emit" column
// (docs/benchmarks.md; "0 at the relay": the relay's adapter is the one read).
var benchEventsRows = []struct {
	label   string
	kind    benchEventsKind
	holders int
	noop    bool
	encodes float64
}{
	{"current, in-memory, 0 holders", benchEventsCurrent, 0, false, 0},
	{"current, in-memory, 1 holder", benchEventsCurrent, 1, false, 1},
	{"current, in-memory, 10 holders", benchEventsCurrent, 10, false, 1},
	{"current, in-memory, 70 holders", benchEventsCurrent, 70, false, 1},
	{"older, in-memory, 10 holders", benchEventsOlder, 10, false, 1},
	{"relay, in-memory, 10 holders", benchEventsRelay, 10, false, 0},
	{"current, noop, 0 holders", benchEventsCurrent, 0, true, 0},
	{"current, noop, 1 holder", benchEventsCurrent, 1, true, 0},
	{"current, noop, 10 holders", benchEventsCurrent, 10, true, 0},
}

func BenchmarkEvents(b *testing.B) {
	b.Run("one event message of a paddle sample (binary codec)", func(b *testing.B) {
		bytes := 0
		for b.Loop() {
			bytes = benchEventsMessageBytes(b)
		}
		b.ReportMetric(float64(bytes), "B/message")
	})
	for _, row := range benchEventsRows {
		b.Run(row.label, func(b *testing.B) {
			rig := benchEventsSetUp(b, row.kind, row.holders, row.noop)
			for index := range 200 {
				rig.step(b, index)
			}
			before := rig.encodes()
			steps := 0
			b.ReportAllocs()
			for b.Loop() {
				rig.step(b, steps)
				steps++
			}
			if !row.noop {
				b.ReportMetric((rig.encodes()-before)/float64(steps), "encodes/emit")
			}
		})
	}
}

// The machine-independent values the script prints, as Pi records them (packages/chord/docs/benchmarks.md, "Events
// (2026-11-11)": "One event message of one sample is 67 B" and the table's "Encodes per emit" column): the value is
// encoded once per representation whatever the number of holds and links, a relay encodes nothing.
func TestBenchEventsMessageBytesAndEncodesPerEmit(t *testing.T) {
	if got := benchEventsMessageBytes(t); got != 67 {
		t.Fatalf("one event message of a paddle sample = %d B, want 67", got)
	}
	for _, row := range benchEventsRows {
		if row.noop {
			continue
		}
		t.Run(row.label, func(t *testing.T) {
			rig := benchEventsSetUp(t, row.kind, row.holders, false)
			for index := range 20 {
				rig.step(t, index)
			}
			before := rig.encodes()
			const steps = 50
			for index := range steps {
				rig.step(t, index)
			}
			if got := (rig.encodes() - before) / steps; got != row.encodes {
				t.Fatalf("encodes per emit = %v, want %v", got, row.encodes)
			}
		})
	}
}
