package telemetry_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	chordtelemetry "github.com/MichaelKinsy/PiG/chord/next/telemetry"
	pitelemetry "github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/test/telemetry.test.ts at Pi main c23b9ec1: "the schema", "nothing recorded", "carriers",
// "open spans" and "metrics". The cross-host describes need the host's spans and are ported with them.

func TestSchemaDeclaresEverySpanChordStartsAndKeepsIdsOffMetrics(t *testing.T) {
	var spans []string
	for name := range chordtelemetry.Schema.Spans {
		spans = append(spans, name)
	}
	slices.Sort(spans)
	want := []string{"chord.activate", "chord.call", "chord.close", "chord.hold", "chord.http", "chord.job", "chord.load",
		"chord.method", "chord.open", "chord.relay", "chord.reload", "chord.resolve", "chord.revoke", "chord.routine"}
	if !slices.Equal(spans, want) {
		t.Fatalf("spans = %v", spans)
	}
	for name, metric := range chordtelemetry.Schema.Metrics {
		for _, id := range []string{"object", "hold", "call", "address", "job.id"} {
			if _, ok := metric.Attributes[id]; ok {
				t.Errorf("%s carries id attribute %s", name, id)
			}
		}
	}
}

func TestUntracedTreatsTheNoopContextAsUntraced(t *testing.T) {
	if !chordtelemetry.Untraced(pitelemetry.NoopTelemetryContext) {
		t.Fatal("noop is traced")
	}
	if chordtelemetry.Untraced(pitelemetry.NewInMemoryTelemetryContext()) {
		t.Fatal("in-memory is untraced")
	}
	if !chordtelemetry.Untraced(chordtelemetry.CountingOnly(pitelemetry.NoopTelemetryMetrics)) {
		t.Fatal("countingOnly is traced")
	}
	if carrier := chordtelemetry.CarrierOf(pitelemetry.NoopTelemetryContext); carrier != nil {
		t.Fatalf("carrier = %v", carrier)
	}
	if got := chordtelemetry.ParentOf(pitelemetry.TelemetryCarrier{"any": "carrier"}, pitelemetry.NoopTelemetryContext); got != pitelemetry.TelemetryContext(pitelemetry.NoopTelemetryContext) {
		t.Fatal("noop parent not its own root")
	}
}

func TestCountingOnlyCarriesMetricsAndStartsNoSpan(t *testing.T) {
	adapter := pitelemetry.NewInMemoryTelemetryContext()
	span := chordtelemetry.CountingOnly(adapter.Metrics())
	span.Metrics().Counter("chord.query.runs", pitelemetry.InstrumentOptions{}).Add(1, nil)
	if len(adapter.GetMetrics()) != 1 || len(adapter.GetSpans()) != 0 {
		t.Fatalf("metrics %d spans %d", len(adapter.GetMetrics()), len(adapter.GetSpans()))
	}
	open := chordtelemetry.NewOpenSpan(span, "chord.open", nil)
	open.End()
	if len(adapter.GetSpans()) != 0 {
		t.Fatal("countingOnly started a span")
	}
}

func TestCarriersExtractUnknownOrAbsentAsTheAdaptersRootNeverFailing(t *testing.T) {
	adapter := pitelemetry.NewInMemoryTelemetryContext()
	if chordtelemetry.ParentOf(nil, adapter) != pitelemetry.TelemetryContext(adapter) {
		t.Fatal("absent carrier is not the root")
	}
	if chordtelemetry.ParentOf(pitelemetry.TelemetryCarrier{"foo": "bar"}, adapter) != pitelemetry.TelemetryContext(adapter) {
		t.Fatal("unknown carrier is not the root")
	}
	carrier, err := pitelemetry.StartSpan(adapter, pitelemetry.SpanOptions{Name: "parent"}, func(span pitelemetry.TelemetrySpan) (pitelemetry.TelemetryCarrier, error) {
		return chordtelemetry.CarrierOf(span), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = chordtelemetry.ParentOf(carrier, adapter).StartSpan(pitelemetry.SpanOptions{Name: "child"}, func(pitelemetry.TelemetrySpan) error { return nil })
	spans := adapter.GetSpans()
	if spans[1].ParentID == nil || *spans[1].ParentID != spans[0].ID {
		t.Fatalf("child parent = %v", spans[1].ParentID)
	}
}

func TestOpenSpansEndOnceWithHowTheirWorkSettled(t *testing.T) {
	adapter := pitelemetry.NewInMemoryTelemetryContext()
	attributes := pitelemetry.SpanAttributes{
		pitelemetry.Attr("chord.host", "client"), pitelemetry.Attr("type", "t"), pitelemetry.Attr("member", "m"),
		pitelemetry.Attr("object", "o"), pitelemetry.Attr("principal.kind", "none"),
	}
	ok := chordtelemetry.NewOpenSpan(adapter, "chord.method", attributes)
	ok.EndWith(nil, "method_failed", nil)
	aborted := chordtelemetry.NewOpenSpan(adapter, "chord.method", attributes)
	aborted.EndWith(data.NewChordError(data.ChordErrorInit{Code: data.CodeAborted, Message: "stop"}, nil), "method_failed", nil)
	failed := chordtelemetry.NewOpenSpan(adapter, "chord.method", attributes)
	failed.EndWith(errors.New(strings.Repeat("x", 600)), "method_failed", nil)
	if !ok.Ended() || !aborted.Ended() || !failed.Ended() {
		t.Fatal("not ended")
	}
	ok.End()
	spans := adapter.GetSpans()
	var got [][2]any
	for _, span := range spans {
		got = append(got, [2]any{span.Attributes.Value("outcome"), span.Status.Status})
	}
	want := [][2]any{{"ok", pitelemetry.SpanStatusCodeOK}, {"aborted", pitelemetry.SpanStatusCodeOK}, {"error", pitelemetry.SpanStatusCodeError}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("span %d = %v, want %v", i, got[i], want[i])
		}
	}
	status := spans[2].Status
	if status.Error == nil || status.Error.Name != "method_failed" || status.Error.Message != strings.Repeat("x", 500) {
		t.Fatalf("status = %+v", status.Error)
	}
}

func TestOpenSpanEndsAsAbortedWhenItsContextIsDone(t *testing.T) {
	adapter := pitelemetry.NewInMemoryTelemetryContext()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	open := chordtelemetry.NewOpenSpan(adapter, "chord.hold", nil)
	open.EndWith(errors.New("boom"), "hold_failed", ctx)
	span := adapter.GetSpans()[0]
	if span.Attributes.Value("outcome") != "aborted" || span.Status.Status != pitelemetry.SpanStatusCodeOK {
		t.Fatalf("%v %v", span.Attributes, span.Status)
	}
}

func TestSpanFailedUsesAFixedCategoryAndAtMost500Characters(t *testing.T) {
	adapter := pitelemetry.NewInMemoryTelemetryContext()
	_, _ = pitelemetry.StartSpan(adapter, pitelemetry.SpanOptions{Name: "s"}, func(span pitelemetry.TelemetrySpan) (struct{}, error) {
		chordtelemetry.SpanFailed(span, "call_failed", strings.Repeat("y", 501))
		return struct{}{}, nil
	})
	status := adapter.GetSpans()[0].Status
	if status.Status != pitelemetry.SpanStatusCodeError || status.Error.Name != "call_failed" || status.Error.Message != strings.Repeat("y", 500) {
		t.Fatalf("%+v", status)
	}
}

func TestCappedAndHostAttribute(t *testing.T) {
	if got := chordtelemetry.Capped(strings.Repeat("s", 10_000)); len(got) != 256 {
		t.Fatalf("len = %d", len(got))
	}
	if chordtelemetry.Capped("short") != "short" || chordtelemetry.HostAttribute(nil) != "client" {
		t.Fatal("short or client")
	}
	if got := chordtelemetry.HostAttribute(&contracts.Address{Key: "session|S1"}); got != "session|S1" {
		t.Fatal(got)
	}
	// A character outside the BMP counts two UTF-16 units: 129 of them overflow 256.
	if got := chordtelemetry.Capped(strings.Repeat("\U0001F600", 129)); got != strings.Repeat("\U0001F600", 128) {
		t.Fatalf("astral cap = %d bytes", len(got))
	}
}

func TestMetricsMakeEachInstrumentOncePerAdapterAndNoneForNoop(t *testing.T) {
	adapter := pitelemetry.NewInMemoryTelemetryContext()
	metrics := chordtelemetry.Metrics(adapter.Metrics())
	if metrics.Counter("chord.link.encodes") != metrics.Counter("chord.link.encodes") {
		t.Fatal("counter made twice")
	}
	if chordtelemetry.Metrics(nil) != chordtelemetry.Metrics(pitelemetry.NoopTelemetryMetrics) {
		t.Fatal("noop not shared")
	}
	metrics.Counter("chord.link.bytes").Add(3, pitelemetry.SpanAttributes{pitelemetry.Attr("direction", "sent")})
	recorded := adapter.GetMetrics()
	if len(recorded) != 1 || recorded[0].Name != "chord.link.bytes" || recorded[0].Kind != pitelemetry.TelemetryMetricKindCounter ||
		recorded[0].Unit == nil || *recorded[0].Unit != "By" || recorded[0].Value != 3 || recorded[0].Attributes.Value("direction") != "sent" {
		t.Fatalf("recorded = %+v", recorded)
	}
}

// A call that records nothing allocates nothing for its metrics: the no-op instruments of a host without an adapter are the
// adapter's shared no-op ones, not the schema's metadata made afresh on every use (go: a host counts its calls in flight on
// every call).
func TestMetricsOfNoAdapterAllocateNothingPerUse(t *testing.T) {
	metrics := chordtelemetry.Metrics(nil)
	if allocated := testing.AllocsPerRun(100, func() {
		metrics.UpDownCounter("chord.calls.in_flight").Add(1, nil)
		metrics.Counter("chord.link.encodes").Add(1, nil)
		metrics.Histogram("chord.commit.duration").Record(1, nil, nil)
	}); allocated != 0 {
		t.Fatalf("a use of no-op instruments allocates %v, want 0", allocated)
	}
}
