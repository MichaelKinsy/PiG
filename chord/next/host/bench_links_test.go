package host

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports the host rows of packages/chord/test/next/bench/links.ts: remote objects over one memory link (slices 3a, 3b), per
// codec side by side: compute (hold to live, commit-to-frame end to end), memory (retained per hold), drops (3b:
// reconnects re-holding 1 and 100 references, 1,000 calls parked while the link is down) and calls. The codec rows (wire
// bytes, encode and decode time and allocations per message) are chord/next/link/bench_test.go. Payloads are modelled on
// p2: a notes list, a 500 KB conversation document, a reply streamed token by token, calls with and without bytes. Pi
// reports medians over bursts; the Go form is `testing.B` (ns/op, allocs/op) with the per-commit counts as metrics.
// Run by hand (not in CI): go test -run '^$' -bench BenchmarkLinks -benchmem ./chord/next/host/

var (
	benchLinksEntry = contracts.Obj(
		contracts.Prop("id", contracts.Int()), contracts.Prop("text", contracts.Str()),
		contracts.Prop("author", contracts.Str()), contracts.Prop("at", contracts.Int()))
	benchLinksShape = contracts.Obj(contracts.Prop("entries", contracts.Arr(benchLinksEntry)))
)

// benchLinksStore is the shared store: one document of entries, and the writes the benchmark makes.
var benchLinksStore = mustContract(contracts.Local("bench.store", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("name", contracts.Str())},
	Members: []contracts.LocalEntry{
		contracts.Mem("doc", benchLinksShape),
		contracts.Mem("append", contracts.NewMethod(benchLinksEntry)),
		contracts.Mem("edit", contracts.NewMethod(contracts.Obj(contracts.Prop("index", contracts.Int()), contracts.Prop("text", contracts.Str())))),
		contracts.Mem("remove", contracts.NewMethod(contracts.Int())),
		contracts.Mem("stream", contracts.NewMethod(contracts.Str())),
	},
}))

// benchLinksView is the remote view a client holds: the store's document as it is.
var benchLinksView = remoteOneVariantNamed("bench.view", "owner", contracts.Params{contracts.Prop("name", contracts.Str())},
	contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("doc", benchLinksShape)}})

// benchLinksFiles is a file service: a call with a small argument, and one with bytes.
var benchLinksFiles = remoteOneVariantNamed("bench.files", "owner", contracts.Params{}, contracts.VariantDeclaration{
	Methods: []contracts.MethodEntry{
		contracts.Meth("ping", contracts.NewMethod(contracts.Obj(contracts.Prop("n", contracts.Int())), contracts.Int())),
		contracts.Meth("put", contracts.NewMethod(contracts.Obj(contracts.Prop("data", contracts.Bytes())), contracts.Int())),
	},
})

// remoteOneVariantNamed is remoteOneVariant with the variant's name.
func remoteOneVariantNamed(name, variant string, params contracts.Params, declaration contracts.VariantDeclaration) *contracts.Contract {
	return mustContract(contracts.Remote(name, contracts.RemoteDeclaration{
		Version:     1,
		Params:      params,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants:    []contracts.VariantEntry{contracts.Var(variant, declaration)},
	}))
}

var benchLinksDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("doc", documents.StateOptions{
		Schema:  benchLinksShape,
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("entries", []any{}) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// benchPings is the `n` of every `ping` in the order the provider ran them.
type benchPings struct {
	mu sync.Mutex
	ns []float64
}

func (p *benchPings) add(n float64) {
	p.mu.Lock()
	p.ns = append(p.ns, n)
	p.mu.Unlock()
}

func (p *benchPings) reset() {
	p.mu.Lock()
	p.ns = nil
	p.mu.Unlock()
}

func (p *benchPings) all() []float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]float64(nil), p.ns...)
}

// benchLinksFacet serves the three types of the benchmark.
func benchLinksFacet(pings *benchPings) func(host FacetHost, _ FacetSelf) error {
	return func(h FacetHost, _ FacetSelf) error {
		change := func(doc *documents.Singleton, fn func(entries *delta.Array) error, ctx context.Context) (delta.JsonValue, error) {
			_, err := doc.Change(func(draft *delta.Object) (any, error) { return nil, fn(draft.Array("entries")) }, ctx)
			return nil, err
		}
		if err := h.Serve(benchLinksStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := self.State(benchLinksDoc).(*documents.Singleton)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"doc": Expose(doc)},
				Methods: map[string]contracts.MethodFunction{
					"append": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return change(doc, func(entries *delta.Array) error { _, err := entries.Push(args); return err }, ctx)
					},
					"edit": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						edit := args.(*delta.JsonObject)
						return change(doc, func(entries *delta.Array) error {
							return entries.Object(int(edit.Value("index").(float64))).Set("text", edit.Value("text"))
						}, ctx)
					},
					"remove": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return change(doc, func(entries *delta.Array) error {
							_, err := entries.Splice(int(args.(float64)), 1)
							return err
						}, ctx)
					},
					"stream": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return change(doc, func(entries *delta.Array) error {
							last := entries.Object(entries.Len() - 1)
							return last.Set("text", last.Get("text").(string)+args.(string))
						}, ctx)
					},
				},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(benchLinksView, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"doc": Expose(self.Get(benchLinksStore, jsonObject("name", params.Value("name"))).Member("doc")),
				},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(benchLinksFiles, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
				Methods: map[string]contracts.MethodFunction{
					"ping": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
						n := args.(*delta.JsonObject).Value("n").(float64)
						pings.add(n)
						return n + 1, nil
					},
					"put": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
						return float64(len(args.(*delta.JsonObject).Value("data").([]byte))), nil
					},
				},
			}}, nil
		}, ServeOptions{})
	}
}

// countedLink is a link end counting what it sends and keeping the last message; it reports its close once.
type countedLink struct {
	link.Link
	mu       sync.Mutex
	bytes    int
	messages int
	last     []byte
	// receivedBytes is what this end received.
	receivedBytes int
	closed        chan struct{}
	once          sync.Once
}

func newCountedLink(end link.Link) *countedLink {
	return &countedLink{Link: end, closed: make(chan struct{})}
}

func (c *countedLink) Send(message []byte) {
	c.mu.Lock()
	c.bytes += len(message)
	c.messages++
	c.last = message
	c.mu.Unlock()
	c.Link.Send(message)
}

// OnClose forwards the listener once the counter's own has been set.
func (c *countedLink) OnClose(listener func()) {
	c.Link.OnClose(func() {
		c.once.Do(func() { close(c.closed) })
		listener()
	})
}

func (c *countedLink) OnMessage(listener func(message []byte)) {
	c.Link.OnMessage(func(message []byte) {
		c.mu.Lock()
		c.receivedBytes += len(message)
		c.mu.Unlock()
		listener(message)
	})
}

// received is the bytes this end received.
func (c *countedLink) received() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.receivedBytes
}

func (c *countedLink) sent() (bytes, messages int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes, c.messages
}

func benchLinkEntry(id, length int) *delta.JsonObject {
	return jsonObject("id", float64(id), "text", strings.Repeat("x", length), "author", "mario", "at", float64(1_700_000_000_000+id))
}

func benchLinkNotes() []*delta.JsonObject {
	entries := make([]*delta.JsonObject, 1_000)
	for id := range entries {
		entries[id] = benchLinkEntry(id, 80)
	}
	return entries
}

func benchLinkDocument() []*delta.JsonObject {
	entries := make([]*delta.JsonObject, 500)
	for id := range entries {
		entries[id] = benchLinkEntry(id, 960)
	}
	return entries
}

// benchLinks is a server with entries in store name, and a client connected over a counted link in codec.
type benchLinks struct {
	server, client Host
	store          *Reference
	down, up       *countedLink
	meter          *nexttest.Meter
	pings          *benchPings
	codec          link.Codec
	ctx            context.Context
}

// benchLinksSetup is Pi's `setup(name, entries, codec, metered)`: metered false leaves telemetry out (the in-memory
// adapter keeps every record, which a memory measurement would count).
func benchLinksSetup(b *testing.B, name string, entries []*delta.JsonObject, codec link.Codec, metered bool) *benchLinks {
	b.Helper()
	result := &benchLinks{pings: &benchPings{}, codec: codec, ctx: background}
	options := HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(err HostError) { b.Errorf("onError %s", err.Kind) }}
	if metered {
		result.meter = nexttest.Metered(b)
		options.Telemetry = result.meter.Telemetry
		result.ctx = result.meter.Context
	}
	result.server = newHost(b, options)
	must(b, result.server.Load([]HostFacet{{Name: "@bench/app", Facet: DefineFacet(benchLinksFacet(result.pings))}}, result.ctx))
	var err error
	result.store, err = result.server.Get(benchLinksStore, jsonObject("name", name), result.ctx, GetOptions{})
	must(b, err)
	for _, entry := range entries {
		_, err := result.store.Call("append", entry, result.ctx)
		must(b, err)
	}
	a, c := link.MemoryLinkPair()
	result.down, result.up = newCountedLink(a), newCountedLink(c)
	must(b, result.server.Accept(result.down, LinkOptions{Principal: ownerPrincipal(), Codec: codec}, result.ctx))
	result.client = newHost(b, HostOptions{OnError: func(err HostError) { b.Errorf("onError %s", err.Kind) }})
	must(b, result.client.Connect(result.up, LinkOptions{Principal: serverPrincipal(), Codec: codec}, result.ctx))
	must(b, result.client.Load(nil, result.ctx))
	return result
}

func (l *benchLinks) close(b *testing.B) {
	b.Helper()
	must(b, l.client.Close(background))
	must(b, l.server.Close(background))
}

// encodes are the encodes counted so far.
func (l *benchLinks) encodes() float64 {
	if l.meter == nil {
		return 0
	}
	return l.meter.Counts()["chord.link.encodes"]
}

// frames is a watch of ref's doc whose frames wake the caller: arrived is signalled once per frame.
func benchFrames(b *testing.B, ref *Reference) (arrived chan struct{}, stop func()) {
	b.Helper()
	watch, err := ref.Member("doc").Watch(background)
	must(b, err)
	arrived = make(chan struct{}, 1<<16)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
		arrived <- struct{}{}
		return nil
	})
	return arrived, func() { _, _ = watch.Stop() }
}

func BenchmarkLinks(b *testing.B) {
	for _, codec := range []link.Codec{link.BinaryCodec, link.TextCodec} {
		b.Run(codec.Name(), func(b *testing.B) {
			benchLinksRows(b, codec)
		})
	}
}

func benchLinksRows(b *testing.B, codec link.Codec) {
	snapshots := []struct {
		label   string
		entries func() []*delta.JsonObject
	}{
		{"notes list of 1,000 items", benchLinkNotes},
		{"500 KB document", benchLinkDocument},
	}
	// A snapshot over a link: hold to live (two hops).
	for _, each := range snapshots {
		b.Run(each.label+": hold to live (two hops)", func(b *testing.B) {
			l := benchLinksSetup(b, each.label, each.entries(), codec, true)
			defer l.close(b)
			// The first hold also activates the view; it stays held, so later holds measure the snapshot path alone.
			_, err := l.client.Get(benchLinksView, jsonObject("name", each.label), l.ctx, GetOptions{})
			must(b, err)
			b.ReportAllocs()
			for b.Loop() {
				ref, err := l.client.Get(benchLinksView, jsonObject("name", each.label), l.ctx, GetOptions{})
				if err != nil {
					b.Fatal(err)
				}
				ref.Release()
			}
		})
	}
	b.Run("first hold to live (activation, one sample)", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			b.StopTimer()
			l := benchLinksSetup(b, "first hold", benchLinkNotes(), codec, true)
			b.StartTimer()
			ref, err := l.client.Get(benchLinksView, jsonObject("name", "first hold"), l.ctx, GetOptions{})
			b.StopTimer()
			must(b, err)
			ref.Release()
			l.close(b)
			b.StartTimer()
		}
	})
	// One commit per run: commit-to-frame, bytes and messages per commit, encodes per hold.
	commits := []struct {
		label string
		write func(l *benchLinks, i int) error
	}{
		{"one appended item", func(l *benchLinks, i int) error {
			_, err := l.store.Call("append", benchLinkEntry(10_000+i, 80), l.ctx)
			return err
		}},
		{"one changed field", func(l *benchLinks, i int) error {
			_, err := l.store.Call("edit", jsonObject("index", 500.0, "text", "edit "+strconv.Itoa(i)), l.ctx)
			return err
		}},
		{"one removed item", func(l *benchLinks, i int) error {
			_, err := l.store.Call("remove", 0.0, l.ctx)
			return err
		}},
	}
	for _, each := range commits {
		b.Run(each.label+": commit to frame", func(b *testing.B) {
			l := benchLinksSetup(b, each.label, benchLinkNotes(), codec, true)
			defer l.close(b)
			view, err := l.client.Get(benchLinksView, jsonObject("name", each.label), l.ctx, GetOptions{})
			must(b, err)
			arrived, stop := benchFrames(b, view)
			defer stop()
			_, messages0 := l.down.sent()
			encodes0 := l.encodes()
			b.ReportAllocs()
			runs := 0
			for i := 0; b.Loop(); i++ {
				if err := each.write(l, i); err != nil {
					b.Fatal(err)
				}
				<-arrived
				runs++
			}
			_, messages := l.down.sent()
			b.ReportMetric(float64(messages-messages0)/float64(max(runs, 1)), "messages/commit")
			b.ReportMetric((l.encodes()-encodes0)/float64(max(runs, 1)), "encodes/commit")
		})
	}
	// A streamed reply: small text appends to one entry.
	b.Run("streamed reply: time per append", func(b *testing.B) {
		l := benchLinksSetup(b, "stream", []*delta.JsonObject{benchLinkEntry(0, 0)}, codec, true)
		defer l.close(b)
		view, err := l.client.Get(benchLinksView, jsonObject("name", "stream"), l.ctx, GetOptions{})
		must(b, err)
		arrived, stop := benchFrames(b, view)
		defer stop()
		bytes0, _ := l.down.sent()
		b.ReportAllocs()
		appends := 0
		for b.Loop() {
			if _, err := l.store.Call("stream", " tok", l.ctx); err != nil {
				b.Fatal(err)
			}
			<-arrived
			appends++
		}
		bytes, _ := l.down.sent()
		b.ReportMetric(float64(bytes-bytes0)/float64(max(appends, 1)), "wire-B/append")
	})
	// The memory retained per hold of 1,000 items (both ends), and the messages of one commit, with holds on one link.
	for _, holds := range []int{1, 10} {
		b.Run(strconv.Itoa(holds)+" hold(s) of 1,000 items: heap retained per hold (both ends)", func(b *testing.B) {
			name := "holds " + strconv.Itoa(holds)
			var perHold float64
			trials, messages := 0, 0
			for b.Loop() {
				b.StopTimer()
				l := benchLinksSetup(b, name, benchLinkNotes(), codec, false)
				before := benchSettledHeap()
				views := make([]*Reference, 0, holds)
				for range holds {
					view, err := l.client.Get(benchLinksView, jsonObject("name", name), background, GetOptions{})
					must(b, err)
					views = append(views, view)
				}
				_, err := l.store.Call("edit", jsonObject("index", 1.0, "text", "warm"), background)
				must(b, err)
				after := benchSettledHeap()
				_, messages0 := l.down.sent()
				_, err = l.store.Call("edit", jsonObject("index", 2.0, "text", "changed"), background)
				must(b, err)
				transported(l.down.Link.(*link.MemoryLink))
				_, messages1 := l.down.sent()
				messages = messages1 - messages0
				perHold += (float64(after) - float64(before)) / float64(holds)
				trials++
				runtime.KeepAlive(views)
				l.close(b)
				b.StartTimer()
			}
			b.ReportMetric(perHold/float64(max(trials, 1)), "heap-B/hold")
			b.ReportMetric(float64(messages), "messages/commit")
		})
	}
	// Calls: a small argument, and a 1 MiB `bytes()` argument.
	b.Run("call, small argument: round trip", func(b *testing.B) {
		l := benchLinksSetup(b, "calls", nil, codec, true)
		defer l.close(b)
		files, err := l.client.Get(benchLinksFiles, jsonObject(), l.ctx, GetOptions{})
		must(b, err)
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if _, err := files.Call("ping", jsonObject("n", float64(i)), l.ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("call, 1 MiB bytes argument: round trip", func(b *testing.B) {
		l := benchLinksSetup(b, "calls", nil, codec, true)
		defer l.close(b)
		files, err := l.client.Get(benchLinksFiles, jsonObject(), l.ctx, GetOptions{})
		must(b, err)
		payload := make([]byte, 1024*1024)
		b.ReportAllocs()
		for b.Loop() {
			if _, err := files.Call("put", jsonObject("data", payload), l.ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	// Reconnects with references held of the 1,000-item view (one provider object, a hold each): time from `connect` to
	// every reference live again, and the bytes of the re-holds both ways.
	for _, references := range []int{1, 100} {
		b.Run("reconnect, "+strconv.Itoa(references)+" reference(s) of 1,000 items: time to all live", func(b *testing.B) {
			name := "reconnect " + strconv.Itoa(references)
			l := benchLinksSetup(b, name, benchLinkNotes(), codec, false)
			defer l.close(b)
			views := make([]*Reference, references)
			for i := range views {
				view, err := l.client.Get(benchLinksView, jsonObject("name", name), background, GetOptions{})
				must(b, err)
				views[i] = view
			}
			down := l.down
			var bytesDown, bytesUp int
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				next := benchRelink(b, l, down)
				b.StartTimer()
				must(b, l.server.Accept(next.down, LinkOptions{Principal: ownerPrincipal(), Codec: codec}, background))
				must(b, l.client.Connect(next.up, LinkOptions{Principal: serverPrincipal(), Codec: codec}, background))
				for _, view := range views {
					awaitLive(b, view)
				}
				b.StopTimer()
				bytesDown, _ = next.down.sent()
				bytesUp, _ = next.up.sent()
				down = next.down
				b.StartTimer()
			}
			b.ReportMetric(float64(bytesDown), "wire-B/down")
			b.ReportMetric(float64(bytesUp), "wire-B/up")
		})
	}
	// 1,000 calls made while the link is down, flushed on reconnect: time from `connect` to the last result, and order.
	b.Run("1,000 calls parked while down, flushed on reconnect: time to the last result", func(b *testing.B) {
		l := benchLinksSetup(b, "parked", nil, codec, false)
		defer l.close(b)
		files, err := l.client.Get(benchLinksFiles, jsonObject(), background, GetOptions{})
		must(b, err)
		down := l.down
		ordered := true
		b.ReportAllocs()
		for b.Loop() {
			b.StopTimer()
			l.pings.reset()
			next := benchRelink(b, l, down)
			b.StartTimer()
			must(b, l.server.Accept(next.down, LinkOptions{Principal: ownerPrincipal(), Codec: codec}, background))
			must(b, l.client.Connect(next.up, LinkOptions{Principal: serverPrincipal(), Codec: codec}, background))
			// The link is connected but not yet live: every call below parks until the re-hold is bound.
			results := make([]*parkedCallResult, 1_000)
			for n := range results {
				results[n] = startCall(b, files, "ping", jsonObject("n", float64(n)), background)
			}
			values := make([]delta.JsonValue, len(results))
			for n, result := range results {
				value, err := result.wait(background)
				must(b, err)
				values[n] = value
			}
			b.StopTimer()
			for n, ping := range l.pings.all() {
				ordered = ordered && ping == float64(n)
			}
			for n, value := range values {
				ordered = ordered && value == float64(n+1)
			}
			down = next.down
			b.StartTimer()
		}
		if !ordered {
			b.Fatal("the parked calls did not run in the order they were made")
		}
	})
}

// benchRelinkLinks are the ends of the next link.
type benchRelinkLinks struct{ down, up *countedLink }

// benchRelink drops the client's parent link (the server's end closes it) and waits until the client saw the close; it
// returns the next counted link, not connected yet.
func benchRelink(b *testing.B, l *benchLinks, down *countedLink) benchRelinkLinks {
	b.Helper()
	down.Close()
	<-l.up.closed
	transported(down.Link.(*link.MemoryLink))
	a, c := link.MemoryLinkPair()
	return benchRelinkLinks{down: newCountedLink(a), up: newCountedLink(c)}
}

// benchSettledHeap is the heap in use once queued work ran and garbage was collected, repeatedly.
func benchSettledHeap() uint64 {
	for range 5 {
		nexttest.Delivered()
		runtime.GC()
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}
