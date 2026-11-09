package telemetryingest_test

// Ports packages/chord/test/telemetry-ingest/facet.test.ts and sender.test.ts at Pi main c23b9ec1: the server's telemetry
// ingest (decisions "Telemetry from browser pages", "The server is the one place telemetry is stored", "Who a span was
// for"): a page's batch is checked by its schema (size, every field, no control characters), its spans must carry the
// page's own ids, are capped per page and minute, marked `chord.host: browser` and stamped with the link's principal; a
// worker's batch is written as it is, stamped with its host; nobody else gets the worker's `ingest`; the calls record no
// span of their own; the senders send what a recorder kept once the object is bound.

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/telemetryingest"
	"github.com/MichaelKinsy/PiG/telemetry"
)

var background = chordctx.Background()

var serverKind = func() *contracts.HostKind {
	kind, err := contracts.NewHostKind("server")
	if err != nil {
		panic(err)
	}
	return kind
}()

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func transported() {
	for range 5 {
		nexttest.Delivered()
	}
}

type collector struct {
	mu      sync.Mutex
	batches []telemetryingest.TelemetryBatch
}

func (c *collector) Ingest(batch telemetryingest.TelemetryBatch) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batches = append(c.batches, batch)
	return len(batch.Spans)
}

func (c *collector) taken() []telemetryingest.TelemetryBatch {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]telemetryingest.TelemetryBatch(nil), c.batches...)
}

func principalOf(kind string, more ...any) data.Principal {
	return delta.JsonObjectOf(append([]any{"kind", kind}, more...)...)
}

type server struct {
	host      host.Host
	telemetry *telemetry.InMemoryTelemetryContext
	batches   *collector
}

// serving is a server with the ingest facet (its batches collected); workers maps a principal's canonical JSON to the
// worker's address key.
func serving(t *testing.T, workers map[string]string, who func(data.Principal) telemetry.SpanAttributes) server {
	t.Helper()
	address := serverKind.Address(nil)
	memory := telemetry.NewInMemoryTelemetryContext()
	batches := &collector{}
	h, err := host.CreateHost(host.HostOptions{Address: &address, Principal: principalOf("server"), Telemetry: memory, OnError: func(host.HostError) {}})
	must(t, err)
	t.Cleanup(func() { _ = h.Close(background) })
	facet := telemetryingest.IngestFacet(telemetryingest.IngestOptions{
		Target: batches,
		WorkerHost: func(principal data.Principal) (string, bool) {
			key, ok := workers[principalJSON(principal)]
			return key, ok
		},
		PrincipalAttributes: who,
	})
	report, err := h.Load([]host.HostFacet{{Name: "chord.telemetry", Facet: facet}}, background)
	must(t, err)
	if len(report.Failed) > 0 {
		t.Fatalf("left out: %v", report.Failed[0].Error)
	}
	return server{host: h, telemetry: memory, batches: batches}
}

func principalJSON(principal data.Principal) string {
	encoded, _ := principal.MarshalJSON()
	return string(encoded)
}

func ownerAttributes(principal data.Principal) telemetry.SpanAttributes {
	return telemetry.SpanAttributes{telemetry.Attr("chord.principal", principalJSON(principal)), telemetry.Attr("user.name", "Ada")}
}

// clientOf is a client host linked to s as principal; options carry its telemetry.
func clientOf(t *testing.T, s server, principal data.Principal, options host.HostOptions) (host.Host, func()) {
	t.Helper()
	options.OnError = func(host.HostError) {}
	client, err := host.CreateHost(options)
	must(t, err)
	t.Cleanup(func() { _ = client.Close(background) })
	_, err = client.Load(nil, background)
	must(t, err)
	connect := func() {
		a, b := link.MemoryLinkPair()
		must(t, s.host.Accept(a, host.LinkOptions{Principal: principal, Codec: link.BinaryCodec}, background))
		must(t, client.Connect(b, host.LinkOptions{Principal: principalOf("server"), Codec: link.BinaryCodec}, background))
	}
	connect()
	transported()
	return client, connect
}

func spanRow(incarnation string, n int, mutate func(*delta.JsonObject)) *delta.JsonObject {
	prefix := "browser:" + incarnation + ":"
	row := delta.JsonObjectOf("id", prefix+itoa(n), "traceId", prefix+"1")
	if n != 1 {
		row.Set("parentId", prefix+"1")
	}
	row.Set("name", "page.click")
	row.Set("startedMs", 1000.0)
	row.Set("endedMs", 1002.0)
	row.Set("attributes", delta.NewJsonObject(0))
	row.Set("events", []any{})
	row.Set("links", []any{})
	row.Set("status", delta.JsonObjectOf("status", "ok"))
	if mutate != nil {
		mutate(row)
	}
	return row
}

func itoa(n int) string { return strconv.Itoa(n) }

func ingest(t *testing.T, client host.Host, spans []any) (delta.JsonValue, error) {
	t.Helper()
	page, err := client.Get(background, telemetryingest.PageTelemetry, delta.NewJsonObject(0), host.GetOptions{})
	must(t, err)
	return page.Call("ingest", delta.JsonObjectOf("spans", spans), background)
}

func TestPageBatchWritesItsOwnSpansMarkedBrowserWithItsLinksPrincipalNoAttributeOfItsOverriding(t *testing.T) {
	s := serving(t, nil, ownerAttributes)
	client, _ := clientOf(t, s, principalOf("owner"), host.HostOptions{})
	first := spanRow(incarnationOf(client), 1, func(row *delta.JsonObject) {
		row.Set("attributes", delta.JsonObjectOf("chord.host", "server", "chord.principal", "root"))
	})
	receipt, err := ingest(t, client, []any{first, spanRow(incarnationOf(client), 2, nil)})
	must(t, err)
	if !reflect.DeepEqual(receipt, delta.JsonObjectOf("written", 2.0, "dropped", 0.0)) {
		t.Fatalf("receipt = %v", receipt)
	}
	batch := s.batches.taken()[0]
	for _, span := range batch.Spans {
		want := telemetry.SpanAttributes{telemetry.Attr("chord.host", "browser"), telemetry.Attr("chord.principal", `{"kind":"owner"}`), telemetry.Attr("user.name", "Ada")}
		if !reflect.DeepEqual(span.Attributes, want) {
			t.Fatalf("attributes = %v", span.Attributes)
		}
	}
	if len(batch.Metrics) != 0 {
		t.Fatalf("metrics = %v", batch.Metrics)
	}
}

func TestPageBatchCallsTheHookOncePerSpanItStampsCachingNothingItself(t *testing.T) {
	calls := 0
	s := serving(t, nil, func(data.Principal) telemetry.SpanAttributes {
		calls++
		return telemetry.SpanAttributes{telemetry.Attr("chord.principal", "owner")}
	})
	client, _ := clientOf(t, s, principalOf("owner"), host.HostOptions{})
	_, err := ingest(t, client, []any{spanRow(incarnationOf(client), 1, nil), spanRow(incarnationOf(client), 2, nil)})
	must(t, err)
	_, err = ingest(t, client, []any{spanRow(incarnationOf(client), 3, nil)})
	must(t, err)
	if calls != 3 {
		t.Fatalf("hook called %d times", calls)
	}
}

func TestPageBatchNeverKeepsAPrincipalThePageClaims(t *testing.T) {
	s := serving(t, nil, func(data.Principal) telemetry.SpanAttributes { return nil })
	client, _ := clientOf(t, s, principalOf("owner"), host.HostOptions{})
	claimed := spanRow(incarnationOf(client), 1, func(row *delta.JsonObject) {
		row.Set("attributes", delta.JsonObjectOf("chord.principal", "root", "chord.host", "server", "n", 1.0))
	})
	_, err := ingest(t, client, []any{claimed})
	must(t, err)
	got := s.batches.taken()[0].Spans[0].Attributes
	if len(got) != 2 || got.Value("n") != 1.0 || got.Value("chord.host") != "browser" || got.Value("chord.principal") != nil {
		t.Fatalf("attributes = %v", got)
	}
}

func TestPageBatchDropsSpansPosingAsAnotherSendersOrTheServers(t *testing.T) {
	s := serving(t, nil, ownerAttributes)
	client, _ := clientOf(t, s, principalOf("owner"), host.HostOptions{})
	incarnation := incarnationOf(client)
	foreign := "0193b2c4-1111-7000-8000-000000000000:7"
	spans := []any{
		spanRow(incarnation, 2, nil),
		spanRow(incarnation, 3, func(row *delta.JsonObject) { row.Set("id", foreign) }),
		spanRow(incarnation, 4, func(row *delta.JsonObject) { row.Set("traceId", foreign) }),
		spanRow(incarnation, 5, func(row *delta.JsonObject) { row.Set("parentId", foreign) }),
		spanRow(incarnation, 6, func(row *delta.JsonObject) { row.Set("id", "browser:someone-else:6") }),
		spanRow(incarnation, 7, func(row *delta.JsonObject) { row.Set("startedMs", 2000.0); row.Set("endedMs", 1000.0) }),
	}
	receipt, err := ingest(t, client, spans)
	must(t, err)
	if !reflect.DeepEqual(receipt, delta.JsonObjectOf("written", 1.0, "dropped", 5.0)) {
		t.Fatalf("receipt = %v", receipt)
	}
	if got := s.batches.taken()[0].Spans; len(got) != 1 || got[0].ID != "browser:"+incarnation+":2" {
		t.Fatalf("spans = %+v", got)
	}
}

func TestPageBatchRefusesAnInvalidBatchWritingNothing(t *testing.T) {
	long := func(n int, ch string) string { return strings.Repeat(ch, n) }
	cases := map[string]func(incarnation string) []any{
		"more spans than a batch takes": func(inc string) []any {
			spans := make([]any, telemetryingest.PageBatchSpans+1)
			for n := range spans {
				spans[n] = spanRow(inc, n+2, nil)
			}
			return spans
		},
		"a name with a control character": func(inc string) []any {
			return []any{spanRow(inc, 1, func(row *delta.JsonObject) { row.Set("name", "\u001b]0;PWNED\u0007") })}
		},
		"an id with a space": func(inc string) []any {
			return []any{spanRow(inc, 1, func(row *delta.JsonObject) { row.Set("id", "browser:"+inc+": 1") })}
		},
		"a 129-character name": func(inc string) []any {
			return []any{spanRow(inc, 1, func(row *delta.JsonObject) { row.Set("name", long(129, "n")) })}
		},
		"a 257-character attribute": func(inc string) []any {
			return []any{spanRow(inc, 1, func(row *delta.JsonObject) { row.Set("attributes", delta.JsonObjectOf("text", long(257, "t"))) })}
		},
		"an attribute key with a control character": func(inc string) []any {
			return []any{spanRow(inc, 1, func(row *delta.JsonObject) { row.Set("attributes", delta.JsonObjectOf("a\x00", 1.0)) })}
		},
		"33 attributes": func(inc string) []any {
			return []any{spanRow(inc, 1, func(row *delta.JsonObject) {
				attributes := delta.NewJsonObject(33)
				for n := range 33 {
					attributes.Set("k"+itoa(n), float64(n))
				}
				row.Set("attributes", attributes)
			})}
		},
		"an array attribute": func(inc string) []any {
			return []any{spanRow(inc, 1, func(row *delta.JsonObject) { row.Set("attributes", delta.JsonObjectOf("tags", []any{"a"})) })}
		},
		"17 events": func(inc string) []any {
			return []any{spanRow(inc, 1, func(row *delta.JsonObject) {
				events := make([]any, 17)
				for n := range events {
					events[n] = delta.JsonObjectOf("name", "e", "timeMs", 0.0, "attributes", delta.NewJsonObject(0))
				}
				row.Set("events", events)
			})}
		},
		"a 501-character error message": func(inc string) []any {
			return []any{spanRow(inc, 1, func(row *delta.JsonObject) {
				row.Set("status", delta.JsonObjectOf("status", "error", "error", delta.JsonObjectOf("name", "x", "message", long(501, "m"))))
			})}
		},
	}
	for name, spans := range cases {
		t.Run(name, func(t *testing.T) {
			s := serving(t, nil, ownerAttributes)
			client, _ := clientOf(t, s, principalOf("owner"), host.HostOptions{})
			_, err := ingest(t, client, spans(incarnationOf(client)))
			if _, ok := data.IsChordErrorCode(err, data.CodeInvalid); !ok {
				t.Fatalf("error = %v", err)
			}
			if len(s.batches.taken()) != 0 {
				t.Fatal("a refused batch was written")
			}
		})
	}
}

func TestPageBatchCapsAPagesSpansPerMinuteDroppingAndCountingPastTheBudget(t *testing.T) {
	s := serving(t, nil, ownerAttributes)
	client, _ := clientOf(t, s, principalOf("owner"), host.HostOptions{})
	written, dropped := 0.0, 0.0
	for batch := 0; batch*telemetryingest.PageBatchSpans < telemetryingest.PageSpansPerMinute+telemetryingest.PageBatchSpans; batch++ {
		spans := make([]any, telemetryingest.PageBatchSpans)
		for n := range spans {
			spans[n] = spanRow(incarnationOf(client), batch*telemetryingest.PageBatchSpans+n+2, nil)
		}
		receipt, err := ingest(t, client, spans)
		must(t, err)
		written += receipt.(*delta.JsonObject).Value("written").(float64)
		dropped += receipt.(*delta.JsonObject).Value("dropped").(float64)
	}
	if written != telemetryingest.PageSpansPerMinute || dropped != telemetryingest.PageBatchSpans {
		t.Fatalf("written %v dropped %v", written, dropped)
	}
	total := 0
	for _, batch := range s.batches.taken() {
		total += len(batch.Spans)
	}
	if total != telemetryingest.PageSpansPerMinute {
		t.Fatalf("stored %d", total)
	}
}

func TestPageBatchRecordsNoSpanForAnIngestCall(t *testing.T) {
	s := serving(t, nil, ownerAttributes)
	client, _ := clientOf(t, s, principalOf("owner"), host.HostOptions{})
	_, err := ingest(t, client, []any{spanRow(incarnationOf(client), 1, nil)})
	must(t, err)
	for _, span := range s.telemetry.GetSpans() {
		if span.Name == "chord.method" {
			t.Fatal("an ingest call recorded a method span")
		}
	}
}

func TestWorkerBatchWritesSpansAndMetricsAsTheyAreStampedWithItsHostAnyoneElseHasNothingToCall(t *testing.T) {
	worker := principalOf("worker", "key", "project|p1")
	s := serving(t, map[string]string{principalJSON(worker): "project|p1"}, ownerAttributes)
	client, _ := clientOf(t, s, worker, host.HostOptions{})
	held, err := client.Get(background, telemetryingest.WorkerTelemetry, delta.NewJsonObject(0), host.GetOptions{})
	must(t, err)
	if variant, _, _ := held.Variant(); variant != "worker" {
		t.Fatalf("variant = %q", variant)
	}
	own := spanRow(incarnationOf(client), 1, func(row *delta.JsonObject) {
		row.Set("id", "w:1")
		row.Set("traceId", "w:1")
		row.Delete("parentId")
		row.Set("attributes", delta.JsonObjectOf("tags", []any{"a", "b"}))
	})
	metric := delta.JsonObjectOf("intervalMs", 10_000.0, "name", "chord.link.bytes", "kind", "counter",
		"attributes", delta.JsonObjectOf("direction", "sent"), "count", 2.0, "sum", 10.0, "min", 4.0, "max", 6.0)
	receipt, err := held.Call("ingest", delta.JsonObjectOf("spans", []any{own}, "metrics", []any{metric}), background)
	must(t, err)
	if !reflect.DeepEqual(receipt, delta.JsonObjectOf("written", 1.0, "dropped", 0.0)) {
		t.Fatalf("receipt = %v", receipt)
	}
	batch := s.batches.taken()[0]
	if got := batch.Spans[0].Attributes; len(got) != 2 || got.Value("chord.host") != "project|p1" || !reflect.DeepEqual(got.Value("tags"), []string{"a", "b"}) {
		t.Fatalf("attributes = %v", got)
	}
	if len(batch.Metrics) != 1 || batch.Metrics[0].Sum != 10 || batch.Metrics[0].Count != 2 {
		t.Fatalf("metrics = %+v", batch.Metrics)
	}
	other := serving(t, nil, ownerAttributes)
	page, _ := clientOf(t, other, principalOf("owner"), host.HostOptions{})
	heldOther, err := page.Get(background, telemetryingest.WorkerTelemetry, delta.NewJsonObject(0), host.GetOptions{})
	must(t, err)
	if variant, _, _ := heldOther.Variant(); variant != "other" {
		t.Fatalf("variant = %q", variant)
	}
}

// sending is a server with the ingest facet and a client of principal recording to a recorder of scope(incarnation).
func sending(t *testing.T, principal data.Principal, scope func(incarnation string) string) (server, *telemetryingest.Recorder, host.Host, func()) {
	t.Helper()
	address := serverKind.Address(nil)
	batches := &collector{}
	srv, err := host.CreateHost(host.HostOptions{Address: &address, Principal: principalOf("server"), OnError: func(host.HostError) {}})
	must(t, err)
	t.Cleanup(func() { _ = srv.Close(background) })
	facet := telemetryingest.IngestFacet(telemetryingest.IngestOptions{
		Target: batches,
		WorkerHost: func(who data.Principal) (string, bool) {
			return "project|p1", who.Value("kind") == "worker"
		},
		PrincipalAttributes: func(data.Principal) telemetry.SpanAttributes { return nil },
	})
	_, err = srv.Load([]host.HostFacet{{Name: "chord.telemetry", Facet: facet}}, background)
	must(t, err)
	var incarnation string
	recorder := telemetryingest.NewRecorder(func() string { return scope(incarnation) }, 100)
	client, err := host.CreateHost(host.HostOptions{Telemetry: recorder, OnError: func(host.HostError) {}})
	must(t, err)
	t.Cleanup(func() { _ = client.Close(background) })
	incarnation = incarnationOf(client)
	_, err = client.Load(nil, background)
	must(t, err)
	connect := func() {
		a, b := link.MemoryLinkPair()
		must(t, srv.Accept(a, host.LinkOptions{Principal: principal, Codec: link.BinaryCodec}, background))
		must(t, client.Connect(b, host.LinkOptions{Principal: principalOf("server"), Codec: link.BinaryCodec}, background))
	}
	return server{host: srv, batches: batches}, recorder, client, connect
}

func record(t *testing.T, recorder *telemetryingest.Recorder, name string) {
	t.Helper()
	must(t, recorder.StartSpan(telemetry.SpanOptions{Name: name}, func(telemetry.TelemetrySpan) error { return nil }))
}

func TestPageSenderSendsItsSpansWithoutMetricsOnceBoundUntilThenTheyWaitInTheRecorder(t *testing.T) {
	s, recorder, client, connect := sending(t, principalOf("owner"), func(inc string) string { return "browser:" + inc })
	sender, err := telemetryingest.SendPageTelemetry(client, recorder, background)
	must(t, err)
	record(t, recorder, "page.click")
	recorder.Counter("clicks", telemetry.InstrumentOptions{}).Add(1, nil)
	sender.Send(true)
	if len(s.batches.taken()) != 0 {
		t.Fatal("sent while unbound")
	}
	connect()
	transported()
	sender.Send(true)
	batches := s.batches.taken()
	if len(batches) != 1 || len(batches[0].Spans) != 1 || batches[0].Spans[0].Name != "page.click" || len(batches[0].Metrics) != 0 {
		t.Fatalf("batches = %+v", batches)
	}
	if recorder.QueuedSpans() != 0 {
		t.Fatalf("queued = %d", recorder.QueuedSpans())
	}
	// Nothing waits: no call.
	sender.Send(true)
	if len(s.batches.taken()) != 1 {
		t.Fatal("sent with nothing waiting")
	}
	sender.Stop()
}

func TestPageSenderGivesARefusedBatchUp(t *testing.T) {
	// Ids the server refuses: not under the page's prefix.
	s, recorder, client, connect := sending(t, principalOf("owner"), func(string) string { return "elsewhere" })
	connect()
	sender, err := telemetryingest.SendPageTelemetry(client, recorder, background)
	must(t, err)
	transported()
	record(t, recorder, "page.click")
	sender.Send(false)
	batches := s.batches.taken()
	if len(batches) != 1 || len(batches[0].Spans) != 0 {
		t.Fatalf("batches = %+v", batches)
	}
	if recorder.QueuedSpans() != 0 {
		t.Fatalf("queued = %d", recorder.QueuedSpans())
	}
}

func TestWorkerSenderSendsSpansAndMetricsToTheWorkerVariant(t *testing.T) {
	s, recorder, client, connect := sending(t, principalOf("worker"), func(string) string { return "w" })
	connect()
	sender, err := telemetryingest.SendWorkerTelemetry(client, recorder, background)
	must(t, err)
	transported()
	record(t, recorder, "worker.job")
	recorder.Counter("runs", telemetry.InstrumentOptions{}).Add(2, nil)
	sender.Send(true)
	batches := s.batches.taken()
	if len(batches) != 1 || len(batches[0].Spans) != 1 || batches[0].Spans[0].Name != "worker.job" {
		t.Fatalf("batches = %+v", batches)
	}
	var sum float64
	for _, metric := range batches[0].Metrics {
		if metric.Name == "runs" {
			sum = metric.Sum
		}
	}
	if sum != 2 {
		t.Fatalf("runs = %v in %+v", sum, batches[0].Metrics)
	}
}

var _ context.Context = background

// incarnationOf is the host's incarnation (Pi's `host.incarnation`): a page's span ids carry it.
func incarnationOf(h host.Host) string { return h.Incarnation() }
