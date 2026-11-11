package app

import (
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/telemetryingest"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// ingested is an app telemetry store that keeps the batches the kit's ingest gives it.
type ingested struct {
	telemetry.TelemetryContext
	mu      sync.Mutex
	batches []telemetryingest.TelemetryBatch
}

func (i *ingested) Ingest(batch telemetryingest.TelemetryBatch) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.batches = append(i.batches, batch)
	return len(batch.Spans)
}

// page.test.ts "records a click as the root of one trace…" asserts the click span's chord.principal over a real page; this is
// the same wiring without a browser: the app gives the ingest facet the server host's PrincipalAttributes, so a span a page
// sends records the attributes AppOptions.PrincipalAttributes returns for its link's principal, never what the page claims.
func TestTheAppsIngestFacetStampsAPagesSpansWithTheAppsPrincipalAttributesNotThePagesClaim(t *testing.T) {
	store := &ingested{TelemetryContext: telemetry.NewInMemoryTelemetryContext()}
	s := startTestApp(t, nil, func(o *AppOptions) {
		o.Telemetry = store
		o.PrincipalAttributes = func(principal data.Principal) telemetry.SpanAttributes {
			return telemetry.SpanAttributes{telemetry.Attr("chord.principal", "the-hook"), telemetry.Attr("user.name", "Ada")}
		}
	})
	page, err := s.app.Host.Get(background, telemetryingest.PageTelemetry, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prefix := "browser:" + s.app.Host.Incarnation() + ":"
	span := delta.JsonObjectOf("id", prefix+"1", "traceId", prefix+"1", "name", "page.click", "startedMs", 1000.0, "endedMs", 1002.0,
		"attributes", delta.JsonObjectOf("chord.principal", "claimed-by-the-page"), "events", []any{}, "links", []any{},
		"status", delta.JsonObjectOf("status", "ok"))
	if _, err := page.Call("ingest", delta.JsonObjectOf("spans", []any{span}), background); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.batches) != 1 || len(store.batches[0].Spans) != 1 {
		t.Fatalf("batches = %+v, want one span", store.batches)
	}
	got := store.batches[0].Spans[0].Attributes
	want := telemetry.SpanAttributes{telemetry.Attr("chord.host", "browser"), telemetry.Attr("chord.principal", "the-hook"), telemetry.Attr("user.name", "Ada")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the span's attributes = %v, want %v", got, want)
	}
}
