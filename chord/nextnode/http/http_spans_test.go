package http

// pi: packages/chord/src/node/http/http.ts

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitelemetry "github.com/MichaelKinsy/PiG/telemetry"
)

// Tests of the request spans (slice 9): a routed request's `chord.http` span, its W3C parent taken only from a request a
// contributed relay verified, and within its bounds. Ports the describe "request spans (slice 9)" of
// packages/chord/test/node/http/http.test.ts.
//
// Forced differences. Pi's `W3cTelemetry` extends `InMemoryTelemetryContext` and overrides `extract`; Go's recorder is its
// own propagation, so the adapter embeds it and answers `Propagation` with a wrapper overriding `Extract`. Pi's `ctx.trace`
// (T6, not ported on the host) is `chordctx.TraceIn` on the handler's ctx.

const traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"

// w3cTelemetry is an adapter reading a W3C `traceparent` as the carrier of parent, its first span.
type w3cTelemetry struct {
	*pitelemetry.InMemoryTelemetryContext
	mu        sync.Mutex
	parent    pitelemetry.TelemetryCarrier
	extracted []pitelemetry.TelemetryCarrier
}

func newW3cTelemetry() *w3cTelemetry {
	return &w3cTelemetry{InMemoryTelemetryContext: pitelemetry.NewInMemoryTelemetryContext()}
}

func (w *w3cTelemetry) Propagation() pitelemetry.TelemetryPropagation { return w3cPropagation{w} }

type w3cPropagation struct{ w *w3cTelemetry }

func (p w3cPropagation) Inject(context pitelemetry.TelemetryContext) pitelemetry.TelemetryCarrier {
	return p.w.Inject(context)
}

func (p w3cPropagation) Extract(carrier pitelemetry.TelemetryCarrier) pitelemetry.TelemetryContext {
	p.w.mu.Lock()
	p.w.extracted = append(p.w.extracted, carrier)
	parent := p.w.parent
	p.w.mu.Unlock()
	if carrier["traceparent"] != traceparent || parent == nil {
		return p.w.Extract(carrier)
	}
	return p.w.Extract(parent)
}

func (w *w3cTelemetry) spansNamed(name string) []pitelemetry.RecordedTelemetrySpan {
	var found []pitelemetry.RecordedTelemetrySpan
	for _, span := range w.GetSpans() {
		if span.Name == name {
			found = append(found, span)
		}
	}
	return found
}

// itemsFacet is the plugin the span tests route through: a public lookup (a child span in its ctx), a throwing route, a relay.
func itemsFacet() host.Facet {
	return contributing(HttpContribution{
		Routes: []HttpRouteEntry{
			publicRoute("GET /items/:id", func(_ HttpRequest, ctx host.Ctx) (HttpResponse, error) {
				_, err := chordctx.TraceIn(ctx, pitelemetry.SpanOptions{Name: "app.lookup"}, func(context.Context) (int, error) { return 1, nil })
				return HttpResponse{Kind: KindJSON, Value: 1}, err
			}),
			publicRoute("GET /broken", func(HttpRequest, host.Ctx) (HttpResponse, error) {
				return HttpResponse{}, context.DeadlineExceeded
			}),
		},
		Relays: []*HttpRelay{{
			Name: "radius",
			Verify: func(request *nethttp.Request) (*RelayVerified, error) {
				if request.Header.Get("x-relay") == "secret" {
					return &RelayVerified{Origin: "https://me.relay.example"}, nil
				}
				return nil, nil
			},
		}},
	})
}

func TestRequestSpansTracesARoutedRequestItsMethodRoutePatternAndStatusNeverItsPathItsHandlersCtxUnderIt(t *testing.T) {
	telemetry := newW3cTelemetry()
	server := serving(t, []plugin{{"@app/items", itemsFacet()}}, servedOptions{telemetry: telemetry})
	// Pi sends these in this order, one after the other; a span is recorded in start order (telemetry.getSpans), so the
	// order of the requests is the order of the spans.
	for _, request := range []struct {
		path   string
		status int
	}{{"/items/secret-id", 200}, {"/broken", 500}, {"/nowhere", 404}} {
		if got := mustSend(t, server.port, "GET", request.path, sendOptions{}).status; got != request.status {
			t.Fatalf("GET %s = %d, want %d", request.path, got, request.status)
		}
	}
	server.close()
	requests := telemetry.spansNamed("chord.http")
	type shown struct {
		attributes map[string]any
		parent     *int
		status     pitelemetry.SpanStatusCode
	}
	var got []shown
	for _, span := range requests {
		attributes := map[string]any{}
		for _, attribute := range span.Attributes {
			attributes[attribute.Name] = attribute.Value
		}
		got = append(got, shown{attributes, span.ParentID, span.Status.Status})
	}
	if len(got) != 2 {
		t.Fatalf("chord.http spans = %d, want 2 (the 404 has no route)", len(got))
	}
	for i, want := range []struct {
		route  string
		status int
		code   pitelemetry.SpanStatusCode
	}{{"GET /items/:id", 200, pitelemetry.SpanStatusCodeOK}, {"GET /broken", 500, pitelemetry.SpanStatusCodeError}} {
		if g := got[i]; g.parent != nil || g.status != want.code || g.attributes["route"] != want.route || g.attributes["method"] != "GET" ||
			g.attributes["chord.host"] != "client" || !numberIs(g.attributes["status"], want.status) || len(g.attributes) != 4 {
			t.Fatalf("span %d = %+v, want route %q status %d code %v", i, g, want.route, want.status, want.code)
		}
	}
	spans, _ := json.Marshal(telemetry.GetSpans())
	if strings.Contains(string(spans), "secret-id") {
		t.Fatalf("a span carries the path: %s", spans)
	}
	lookups := telemetry.spansNamed("app.lookup")
	if len(lookups) != 1 || lookups[0].ParentID == nil || *lookups[0].ParentID != requests[0].ID {
		t.Fatalf("app.lookup = %+v, want a child of %d", lookups, requests[0].ID)
	}
}

func TestRequestSpansContinuesAW3CTraceparentOnlyFromARequestAContributedRelayVerified(t *testing.T) {
	telemetry := newW3cTelemetry()
	var carrier pitelemetry.TelemetryCarrier
	if err := telemetry.StartSpan(pitelemetry.SpanOptions{Name: "edge"}, func(span pitelemetry.TelemetrySpan) error {
		carrier = telemetry.Inject(span)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	telemetry.parent = carrier
	server := serving(t, []plugin{{"@app/items", itemsFacet()}}, servedOptions{telemetry: telemetry})
	headers := map[string]string{"traceparent": traceparent, "x-forwarded-for": "203.0.113.9", "x-relay": "secret"}
	if got := mustSend(t, server.port, "GET", "/items/1", sendOptions{headers: headers}).status; got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	if got := mustSend(t, server.port, "GET", "/items/2", sendOptions{headers: map[string]string{"traceparent": traceparent}}).status; got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	server.close()
	edge := telemetry.spansNamed("edge")[0]
	var parents []*int
	for _, span := range telemetry.spansNamed("chord.http") {
		parents = append(parents, span.ParentID)
	}
	if len(parents) != 2 || parents[0] == nil || *parents[0] != edge.ID || parents[1] != nil {
		t.Fatalf("parents = %v, want [%d, nil]", parents, edge.ID)
	}
}

func TestRequestSpansTakesARelayedTraceparentAndTracestateOnlyWithinTheirBoundsAndPrintableReviewL2(t *testing.T) {
	telemetry := newW3cTelemetry()
	server := serving(t, []plugin{{"@app/items", itemsFacet()}}, servedOptions{telemetry: telemetry})
	relay := map[string]string{"x-forwarded-for": "203.0.113.9", "x-relay": "secret"}
	get := func(extra map[string]string) int {
		return mustSend(t, server.port, "GET", "/items/1", sendOptions{headers: httpMerge(relay, extra)}).status
	}
	for _, extra := range []map[string]string{
		{"traceparent": traceparent, "tracestate": "a=1, b=2"},
		{"traceparent": "00-" + strings.Repeat("a", 300)},
		{"traceparent": traceparent, "tracestate": strings.Repeat("s", 513)},
	} {
		if got := get(extra); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
	}
	server.close()
	want := []pitelemetry.TelemetryCarrier{{"traceparent": traceparent, "tracestate": "a=1, b=2"}, {"traceparent": traceparent}}
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	if !reflect.DeepEqual(telemetry.extracted, want) {
		t.Fatalf("extracted = %v, want %v", telemetry.extracted, want)
	}
}

// numberIs is whether v, an attribute's number, is want.
func numberIs(v any, want int) bool {
	switch n := v.(type) {
	case int:
		return n == want
	case float64:
		return n == float64(want)
	}
	return false
}
