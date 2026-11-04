package plugins

import (
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// Not upstream's (docs/plan/chord-plugins-node-evaluator.md, 10.6 #7): the app's `telemetry` adapter is the Node host's. Pi's host gets the adapter and
// calls it in process; here the Node host builds a proxy TelemetryContext whose spans and metrics are ordered data events
// that Go replays into the adapter, so a plugin's `ctx.telemetry` spans and counters land in the app's own recorder
// (pi-telemetry's InMemoryTelemetryContext, ported as telemetry.InMemoryTelemetryContext, the recording adapter), each as
// pi-telemetry's own recorder would have recorded them in the plugin's process.

// cbTelemetryOps are the probe ops that use `ctx.telemetry`.
const cbTelemetryOps = `
work: async (_a, ctx) =>
	ctx.telemetry.startSpan({ name: "plugin.work", attributes: { n: 1, tags: ["a", "b"] } }, async (span) => {
		span.addEvent("step", { k: "v" });
		span.setAttributes({ done: true });
		await span.startSpan({ name: "plugin.child" }, async () => {});
		ctx.telemetry.metrics.counter("plugin.items", { description: "items", unit: "1" }).add(3, { kind: "x" });
		ctx.telemetry.metrics.histogram("plugin.size", { boundaries: [1, 10] }).record(5, {}, span);
		const carrier = ctx.telemetry.propagation.inject(span);
		await ctx.telemetry.propagation.extract(carrier).startSpan({ name: "plugin.continued" }, async () => {});
		return carrier;
	}),
fails: async (_a, ctx) => {
	for (const thrown of [new TypeError("boom"), "text"]) {
		await ctx.telemetry.startSpan({ name: "plugin.fails" }, async () => { throw thrown; }).catch(() => {});
	}
	await ctx.telemetry.startSpan({ name: "plugin.explicit" }, async (span) => {
		span.setStatus({ status: "error", error: { name: "Custom", message: "set by hand" } });
		throw new Error("ignored");
	}).catch(() => {});
},
foreign: async (_a, ctx) => {
	const parent = ctx.telemetry.propagation.extract({ "pi-memory-scope": "other", "pi-memory-span": "7", "pi-memory-trace": "other:1" });
	await parent.startSpan({ name: "plugin.foreign" }, async () => {});
	return ctx.telemetry.propagation.inject(parent);
},
linked: async (_a, ctx) => {
	let first;
	await ctx.telemetry.startSpan({ name: "plugin.first" }, async (span) => { first = span; });
	await ctx.telemetry.startSpan({ name: "plugin.second" }, async (span) => {
		span.addLink({ context: first, attributes: { why: "after" } });
		span.addLink({ context: { startSpan() {} } });
	});
	// A span started from a settled span records nothing, and a settled span ignores its setters.
	let settled;
	await ctx.telemetry.startSpan({ name: "plugin.settled" }, async (span) => { settled = span; });
	settled.setAttributes({ late: true });
	settled.addEvent("late");
	await settled.startSpan({ name: "plugin.inert" }, async () => {});
	return ctx.telemetry.propagation.inject(settled) ?? null;
},
`

func cbRecorded(recorder *telemetry.InMemoryTelemetryContext) map[string]telemetry.RecordedTelemetrySpan {
	spans := map[string]telemetry.RecordedTelemetrySpan{}
	for _, span := range recorder.GetSpans() {
		spans[span.Name] = span
	}
	return spans
}

// cbWithTelemetry starts the probe plugin with ops on a Node host whose adapter is a new recorder.
func cbWithTelemetry(t *testing.T) (*NodeHost, *probe, *telemetry.InMemoryTelemetryContext) {
	t.Helper()
	recorder := telemetry.NewInMemoryTelemetryContext()
	node, p := cbStart(t, cbTelemetryOps, func(load *RuntimeLoad) { load.Telemetry = recorder })
	return node, p, recorder
}

// The spans of a plugin's request, with their attributes, events, children and status, and a measurement, are in the app's
// recorder in the order the plugin made them: what the recorder would hold had the plugin run in the app's process.
func TestTelemetryASpansOfAPluginAreRecordedInTheAppsAdapter(t *testing.T) {
	node, p, recorder := cbWithTelemetry(t)
	carrier, _ := p.must("work", nil).(map[string]any)
	spans := recorder.GetSpans()
	var names []string
	for _, span := range spans {
		names = append(names, span.Name)
		if !span.Settled {
			t.Fatalf("span %s is not settled", span.Name)
		}
	}
	if want := []string{"plugin.work", "plugin.child", "plugin.continued"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("spans = %v, want %v (the adapter's recorder got the plugin's spans)", names, want)
	}
	work, child, continued := spans[0], spans[1], spans[2]
	if want := (telemetry.SpanAttributes{telemetry.Attr("n", 1.0), telemetry.Attr("tags", []string{"a", "b"}), telemetry.Attr("done", true)}); !reflect.DeepEqual(work.Attributes, want) {
		t.Fatalf("work attributes = %#v, want %#v", work.Attributes, want)
	}
	if want := []telemetry.RecordedTelemetryEvent{{Name: "step", Attributes: telemetry.SpanAttributes{telemetry.Attr("k", "v")}}}; !reflect.DeepEqual(work.Events, want) {
		t.Fatalf("work events = %#v, want %#v", work.Events, want)
	}
	if work.ParentID != nil || work.ParentContext != nil {
		t.Fatalf("work has a parent: %v %v", work.ParentID, work.ParentContext)
	}
	// Children: by the span's own startSpan (local parent) and by the extracted carrier (the same span).
	for _, each := range []telemetry.RecordedTelemetrySpan{child, continued} {
		if each.ParentID == nil || *each.ParentID != work.ID || !reflect.DeepEqual(each.ParentContext, work.Context) {
			t.Fatalf("%s: parent = %v / %v, want span %d / %v", each.Name, each.ParentID, each.ParentContext, work.ID, work.Context)
		}
	}
	// The carrier the plugin got is a proxy's: it names the span for the Go side, and the adapter's own carrier resolves through it.
	if len(carrier) != 1 {
		t.Fatalf("carrier = %v", carrier)
	}
	extracted := node.ExtractTelemetry(telemetry.TelemetryCarrier{"pig-node-telemetry": carrier["pig-node-telemetry"].(string)})
	if err := extracted.StartSpan(telemetry.SpanOptions{Name: "go.continued"}, func(telemetry.TelemetrySpan) error { return nil }); err != nil {
		t.Fatal(err)
	}
	goSpan := cbRecorded(recorder)["go.continued"]
	if goSpan.ParentID == nil || *goSpan.ParentID != work.ID || !reflect.DeepEqual(goSpan.ParentContext, work.Context) {
		t.Fatalf("go.continued: parent = %v / %v, want span %d / %v", goSpan.ParentID, goSpan.ParentContext, work.ID, work.Context)
	}
	// The measurements: the plugin's counter with its metadata, and the histogram with its exemplar's carrier.
	metrics := map[string]telemetry.RecordedTelemetryMeasurement{}
	for _, measurement := range recorder.GetMetrics() {
		metrics[measurement.Name] = measurement
	}
	items := metrics["plugin.items"]
	description, unit := "items", "1"
	if items.Kind != telemetry.TelemetryMetricKindCounter || items.Value != 3 || !reflect.DeepEqual(items.Attributes, telemetry.SpanAttributes{telemetry.Attr("kind", "x")}) ||
		items.Description == nil || *items.Description != description || items.Unit == nil || *items.Unit != unit {
		t.Fatalf("plugin.items = %+v", items)
	}
	size := metrics["plugin.size"]
	if size.Kind != telemetry.TelemetryMetricKindHistogram || size.Value != 5 || !reflect.DeepEqual(size.Boundaries, []float64{1, 10}) || !reflect.DeepEqual(size.Context, work.Context) {
		t.Fatalf("plugin.size = %+v, want a histogram of 5 with the work span's carrier", size)
	}
}

// A thrown Error gives the span an error status with the Error's name and message; a thrown value that is no Error a bare
// error status; a status the span set itself wins over the automatic one.
func TestTelemetryAThrowingSpanHasPisErrorStatus(t *testing.T) {
	_, p, recorder := cbWithTelemetry(t)
	p.must("fails", nil)
	var got []telemetry.SpanStatus
	for _, span := range recorder.GetSpans() {
		got = append(got, span.Status)
	}
	want := []telemetry.SpanStatus{
		{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: "TypeError", Message: "boom"}},
		{Status: telemetry.SpanStatusCodeError},
		{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: "Custom", Message: "set by hand"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %+v, want %+v", got, want)
	}
}

// A carrier that is not the proxy's reaches the adapter as it is: the adapter's own extract makes its parent (here, one of
// another scope: the recorded span has the carrier as its parent context and no local parent), and `inject` of such a parent is
// the carrier it was made from.
func TestTelemetryAForeignCarrierIsExtractedByTheAdapter(t *testing.T) {
	_, p, recorder := cbWithTelemetry(t)
	injected := p.must("foreign", nil)
	if want := map[string]any{"pi-memory-scope": "other", "pi-memory-span": "7", "pi-memory-trace": "other:1"}; !reflect.DeepEqual(injected, want) {
		t.Fatalf("inject(extract(carrier)) = %v, want the carrier back", injected)
	}
	span := cbRecorded(recorder)["plugin.foreign"]
	if span.ParentID != nil || !reflect.DeepEqual(span.ParentContext, telemetry.TelemetryCarrier{"pi-memory-scope": "other", "pi-memory-span": "7", "pi-memory-trace": "other:1"}) {
		t.Fatalf("plugin.foreign: parent = %v / %v", span.ParentID, span.ParentContext)
	}
}

// Links go to spans of the proxy only (a context that is not one is dropped, as the recorder drops an unknown one); a span
// that settled ignores its setters and starts only inert children, and has a carrier all the same.
func TestTelemetryLinksAndSettledSpansFollowTheRecorder(t *testing.T) {
	_, p, recorder := cbWithTelemetry(t)
	injected := p.must("linked", nil)
	spans := cbRecorded(recorder)
	if _, ok := spans["plugin.inert"]; ok {
		t.Fatal("a span started from a settled span was recorded")
	}
	second := spans["plugin.second"]
	wantLink := []telemetry.RecordedTelemetryLink{{Context: spans["plugin.first"].Context, Attributes: telemetry.SpanAttributes{telemetry.Attr("why", "after")}}}
	if !reflect.DeepEqual(second.Links, wantLink) {
		t.Fatalf("plugin.second links = %+v, want %+v", second.Links, wantLink)
	}
	settled := spans["plugin.settled"]
	if len(settled.Attributes) != 0 || len(settled.Events) != 0 {
		t.Fatalf("a settled span took a setter: %+v", settled)
	}
	if injected == nil {
		t.Fatal("a settled span has no carrier")
	}
}

// A carrier one process made resolves in every other process of the same app (the scope in it names the process that made it),
// so a trace crosses from one plugin host to the next through the adapter: here a Go span continues the first host's span
// through the second host.
func TestTelemetryACarrierOfOneProcessResolvesThroughAnother(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	_, first := cbStart(t, cbTelemetryOps, func(load *RuntimeLoad) { load.Telemetry = recorder })
	other, _ := cbStart(t, cbTelemetryOps, func(load *RuntimeLoad) { load.Telemetry = recorder })
	carrier, _ := first.must("work", nil).(map[string]any)
	if len(carrier) != 1 {
		t.Fatalf("carrier = %v", carrier)
	}
	parent := other.ExtractTelemetry(telemetry.TelemetryCarrier{"pig-node-telemetry": carrier["pig-node-telemetry"].(string)})
	if err := parent.StartSpan(telemetry.SpanOptions{Name: "go.across"}, func(telemetry.TelemetrySpan) error { return nil }); err != nil {
		t.Fatal(err)
	}
	spans := cbRecorded(recorder)
	across, work := spans["go.across"], spans["plugin.work"]
	if across.ParentID == nil || *across.ParentID != work.ID {
		t.Fatalf("go.across: parent = %v, want the first host's plugin.work (%d)", across.ParentID, work.ID)
	}
}

// A span still open when the process ends ends with an error status: the process's callback never returns.
func TestTelemetryASpanOpenWhenTheProcessEndsEndsWithAnErrorStatus(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	node, p := cbStart(t, `open: async (_a, ctx) => ctx.telemetry.startSpan({ name: "plugin.open" }, () => new Promise(() => {})),`, func(load *RuntimeLoad) { load.Telemetry = recorder })
	go func() { _, _ = p.call("open", nil) }()
	eventually(t, 15*time.Second, "the open span to start", func() bool { return len(recorder.GetSpans()) == 1 })
	node.kill()
	<-node.Exited()
	eventually(t, 15*time.Second, "the open span to end", func() bool {
		spans := recorder.GetSpans()
		return len(spans) == 1 && spans[0].Settled
	})
	if status := recorder.GetSpans()[0].Status; status.Status != telemetry.SpanStatusCodeError {
		t.Fatalf("status = %+v, want an error", status)
	}
}
