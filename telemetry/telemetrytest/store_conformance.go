package telemetrytest

// Ports packages/telemetry/test/store-conformance.ts (telemetryStoreConformance).
//
// Go mapping: the shared read-store checks are runner-independent cases like the adapter conformance cases, so the
// SQLite store and any other TelemetryStore adapter run the same checks. vi.spyOn(Date, "now") is jsclock.MockDateNow,
// installed before the fixture is created and restored when the case ends. A callback that awaits a gate is a goroutine
// the case joins. Upstream's non-string carrier values have no Go form, because a TelemetryCarrier holds strings, so the
// fallback check covers each carrier with one key removed. Attribute values are compared through their JSON form,
// because a store may return the decoded form of what the recorder was given.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// StoreFixture is a fresh store, the context that records into it, and the flush that makes recorded telemetry
// readable. Context supplies the Metrics and Propagation capabilities. A nil Flush readies nothing; a nil Dispose
// releases nothing.
type StoreFixture struct {
	Context telemetry.TelemetryContext
	Store   telemetry.TelemetryStore
	Flush   func(context.Context) error
	Dispose func(context.Context) error
}

// StoreFixtureFactory creates an isolated store fixture for one conformance case.
type StoreFixtureFactory func(context.Context) (StoreFixture, error)

type storeTest func(context.Context, StoreFixtureFactory) error

var storeIDPattern = regexp.MustCompile(`^[0-9a-f-]{36}:[1-9][0-9]*$`)

func (fixture StoreFixture) flush(ctx context.Context) error {
	if fixture.Flush == nil {
		return nil
	}
	return fixture.Flush(ctx)
}

func newStoreFixture(ctx context.Context, factory StoreFixtureFactory) (StoreFixture, func() error, error) {
	fixture, err := factory(ctx)
	if err != nil {
		return StoreFixture{}, nil, err
	}
	if fixture.Context.Metrics() == nil || fixture.Context.Propagation() == nil {
		return fixture, nil, errors.New("Store fixtures require a context with metrics and propagation")
	}
	return fixture, func() error {
		if fixture.Dispose == nil {
			return nil
		}
		return fixture.Dispose(ctx)
	}, nil
}

func jsonOf(value any) string {
	encoded, err := jsstring.MarshalJSON(value)
	if err != nil {
		return fmt.Sprintf("%#v", value)
	}
	return string(encoded)
}

// sameJSON is toEqual: the values have the same JSON form.
func sameJSON(actual, expected any, what string) error {
	if a, e := jsonOf(actual), jsonOf(expected); a != e {
		return fmt.Errorf("%s: expected %s, got %s", what, e, a)
	}
	return nil
}

func check(ok bool, format string, args ...any) error {
	if ok {
		return nil
	}
	return fmt.Errorf(format, args...)
}

func noop(telemetry.TelemetrySpan) error { return nil }

func findStoredSpan(spans []telemetry.StoredSpan, name string) (telemetry.StoredSpan, error) {
	for _, span := range spans {
		if span.Name == name {
			return span, nil
		}
	}
	return telemetry.StoredSpan{}, fmt.Errorf("Expected stored span %s", name)
}

func firstNames(store telemetry.TelemetryStore, query telemetry.TraceQuery) ([]string, error) {
	result, err := store.FindTraces(query)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(result.Traces))
	for _, trace := range result.Traces {
		names = append(names, trace.Spans[0].Name)
	}
	return names, nil
}

func sumMetric(rows []telemetry.StoredMetric) (count, sum float64) {
	for _, row := range rows {
		count += row.Count
		sum += row.Sum
	}
	return count, sum
}

// CreateTelemetryStoreConformance creates runner-independent cases for the shared read-store checks, with no
// raw-measurement or backend-specific persistence assumptions. Every case carries name as its Group.
func CreateTelemetryStoreConformance(name string, factory StoreFixtureFactory) []TelemetryAdapterConformanceCase {
	create := func(caseName string, test storeTest) TelemetryAdapterConformanceCase {
		return TelemetryAdapterConformanceCase{Group: name, Name: caseName, Run: func(ctx context.Context) error { return test(ctx, factory) }}
	}
	return []TelemetryAdapterConformanceCase{
		create("returns only settled traces, complete span snapshots, and detached results", settledTraces),
		create("filters by first-span name and start, any error, and all attributes on one span", filtersTraces),
		create("pages complete traces without duplicates when trace starts tie", pagesTraces),
		create("inherits remote trace IDs and preserves remote parents and cross-trace links", remoteTraces),
		create("accepts external string identities without imposing an ID format", externalIdentities),
		create("preserves attribute insertion order on spans, events, and links", preservesAttributeOrder),
		create("queries metric names, inclusive interval bounds, typed attribute equality, and detached values", queriesMetrics),
	}
}

func withStoreFixture(ctx context.Context, factory StoreFixtureFactory, test func(StoreFixture) error) (err error) {
	fixture, dispose, err := newStoreFixture(ctx, factory)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dispose()) }()
	return test(fixture)
}

func settledTraces(ctx context.Context, factory StoreFixtureFactory) error {
	_, restore := jsclock.MockDateNow(20_000)
	defer restore()
	return withStoreFixture(ctx, factory, func(f StoreFixture) error {
		gate := make(chan struct{})
		entered := make(chan struct{})
		active := make(chan error, 1)
		go func() {
			active <- f.Context.StartSpan(telemetry.SpanOptions{Name: "root", Attributes: telemetry.SpanAttributes{telemetry.Attr("tags", []string{"original"})}}, func(root telemetry.TelemetrySpan) error {
				root.AddEvent("event", telemetry.SpanAttributes{telemetry.Attr("tags", []string{"original"})})
				close(entered)
				<-gate
				return root.StartSpan(telemetry.SpanOptions{Name: "child"}, noop)
			})
		}()
		<-entered
		if err := f.flush(ctx); err != nil {
			close(gate)
			return errors.Join(err, <-active)
		}
		pending, err := f.Store.FindTraces(telemetry.TraceQuery{})
		missing, missingErr := f.Store.GetTrace("missing")
		if err := errors.Join(err, missingErr); err != nil {
			close(gate)
			return errors.Join(err, <-active)
		}
		if err := errors.Join(check(len(pending.Traces) == 0, "unsettled traces visible: %v", pending.Traces), check(missing == nil, "GetTrace(missing) = %v, want undefined", missing)); err != nil {
			close(gate)
			return errors.Join(err, <-active)
		}
		close(gate)
		if err := <-active; err != nil {
			return err
		}
		if err := f.flush(ctx); err != nil {
			return err
		}
		found, err := f.Store.FindTraces(telemetry.TraceQuery{})
		if err != nil {
			return err
		}
		if len(found.Traces) == 0 {
			return errors.New("expected a settled trace")
		}
		trace := found.Traces[0]
		root, err := findStoredSpan(trace.Spans, "root")
		if err != nil {
			return err
		}
		child, err := findStoredSpan(trace.Spans, "child")
		if err != nil {
			return err
		}
		if len(root.Events) == 0 {
			return errors.New("expected the root event")
		}
		if err := errors.Join(
			check(storeIDPattern.MatchString(root.ID), "root.id = %q", root.ID),
			check(root.TraceID == root.ID, "root.traceId = %q, want %q", root.TraceID, root.ID),
			check(root.ParentID == nil, "root.parentId = %v, want undefined", root.ParentID),
			check(child.ParentID != nil && *child.ParentID == root.ID, "child.parentId = %v, want %q", child.ParentID, root.ID),
			check(child.TraceID == root.TraceID, "child.traceId = %q, want %q", child.TraceID, root.TraceID),
			check(root.StartedMs == 20_000, "root.startedMs = %v, want 20000", root.StartedMs),
			check(root.EndedMs >= root.StartedMs, "root.endedMs = %v before startedMs %v", root.EndedMs, root.StartedMs),
			check(root.Events[0].TimeMs >= 0, "event timeMs = %v", root.Events[0].TimeMs),
		); err != nil {
			return err
		}
		root.Attributes.Set("tags", []string{"changed"})
		root.Events[0].Attributes.Set("tags", []string{"changed"})
		fresh, err := f.Store.GetTrace(trace.TraceID)
		if err != nil {
			return err
		}
		if fresh == nil {
			return errors.New("expected the trace")
		}
		freshRoot, err := findStoredSpan(fresh.Spans, "root")
		if err != nil {
			return err
		}
		if len(freshRoot.Events) == 0 {
			return errors.New("expected the fresh root event")
		}
		return errors.Join(
			sameJSON(freshRoot.Attributes.Value("tags"), []string{"original"}, "fresh root tags"),
			sameJSON(freshRoot.Events[0].Attributes.Value("tags"), []string{"original"}, "fresh event tags"),
		)
	})
}

func filtersTraces(ctx context.Context, factory StoreFixtureFactory) error {
	now, restore := jsclock.MockDateNow(10_000)
	defer restore()
	return withStoreFixture(ctx, factory, func(f StoreFixture) error {
		if err := f.Context.StartSpan(telemetry.SpanOptions{Name: "old"}, func(root telemetry.TelemetrySpan) error {
			now.Set(30_000)
			if err := root.StartSpan(telemetry.SpanOptions{Name: "recent-child", Attributes: telemetry.SpanAttributes{telemetry.Attr("left", 1.0)}}, noop); err != nil {
				return err
			}
			return root.StartSpan(telemetry.SpanOptions{Name: "split", Attributes: telemetry.SpanAttributes{telemetry.Attr("right", 2.0)}}, noop)
		}); err != nil {
			return err
		}
		now.Set(20_000)
		if err := f.Context.StartSpan(telemetry.SpanOptions{Name: "wanted"}, func(root telemetry.TelemetrySpan) error {
			now.Set(20_001)
			return root.StartSpan(telemetry.SpanOptions{Name: "failure", Attributes: telemetry.SpanAttributes{telemetry.Attr("left", 1.0), telemetry.Attr("right", 2.0), telemetry.Attr("valid", true), telemetry.Attr(`odd."key`, "value")}}, func(child telemetry.TelemetrySpan) error {
				child.SetStatus(telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError})
				return nil
			})
		}); err != nil {
			return err
		}
		now.Set(40_000)
		if err := f.Context.StartSpan(telemetry.SpanOptions{Name: "new", Attributes: telemetry.SpanAttributes{telemetry.Attr("valid", 1.0)}}, noop); err != nil {
			return err
		}
		if err := f.flush(ctx); err != nil {
			return err
		}
		for _, test := range []struct {
			query telemetry.TraceQuery
			want  []string
		}{
			{telemetry.TraceQuery{Name: new("wanted"), Attributes: map[string]telemetry.AttributeValue{"left": 1, "right": 2.0}, Errors: true, StartMin: new(20_000.0), StartMax: new(20_000.0)}, []string{"wanted"}},
			{telemetry.TraceQuery{Name: new("failure")}, []string{}},
			{telemetry.TraceQuery{Attributes: map[string]telemetry.AttributeValue{"left": 1, "right": 2.0}}, []string{"wanted"}},
			{telemetry.TraceQuery{Attributes: map[string]telemetry.AttributeValue{"valid": true, `odd."key`: "value"}}, []string{"wanted"}},
			{telemetry.TraceQuery{Attributes: map[string]telemetry.AttributeValue{"valid": 1.0}}, []string{"new"}},
			{telemetry.TraceQuery{Errors: true}, []string{"wanted"}},
			{telemetry.TraceQuery{StartMin: new(20_000.0)}, []string{"new", "wanted"}},
			{telemetry.TraceQuery{StartMax: new(10_000.0)}, []string{"old"}},
		} {
			got, err := firstNames(f.Store, test.query)
			if err != nil {
				return err
			}
			if err := sameJSON(got, test.want, fmt.Sprintf("names for %+v", test.query)); err != nil {
				return err
			}
		}
		return nil
	})
}

func pagesTraces(ctx context.Context, factory StoreFixtureFactory) error {
	now, restore := jsclock.MockDateNow(20_000)
	defer restore()
	return withStoreFixture(ctx, factory, func(f StoreFixture) error {
		for index := range 25 {
			now.Set(20_000)
			if err := f.Context.StartSpan(telemetry.SpanOptions{Name: "root", Attributes: telemetry.SpanAttributes{telemetry.Attr("index", float64(index))}}, func(root telemetry.TelemetrySpan) error {
				now.Set(20_001)
				return root.StartSpan(telemetry.SpanOptions{Name: "child"}, noop)
			}); err != nil {
				return err
			}
		}
		if err := f.flush(ctx); err != nil {
			return err
		}
		first, err := f.Store.FindTraces(telemetry.TraceQuery{})
		if err != nil {
			return err
		}
		if err := errors.Join(check(len(first.Traces) == 20, "first page = %d traces, want 20", len(first.Traces)), check(first.Cursor != nil, "the first page has no cursor")); err != nil {
			return err
		}
		rest, err := f.Store.FindTraces(telemetry.TraceQuery{Cursor: first.Cursor})
		if err != nil {
			return err
		}
		if err := errors.Join(check(len(rest.Traces) == 5, "rest = %d traces, want 5", len(rest.Traces)), check(rest.Cursor == nil, "the last page has a cursor")); err != nil {
			return err
		}
		everything, err := f.Store.FindTraces(telemetry.TraceQuery{Limit: new(100)})
		if err != nil {
			return err
		}
		all := everything.Traces
		if err := sameJSON(slices.Concat(first.Traces, rest.Traces), all, "pages"); err != nil {
			return err
		}
		for _, trace := range all {
			if len(trace.Spans) != 2 {
				return fmt.Errorf("trace %s has %d spans, want 2", trace.TraceID, len(trace.Spans))
			}
		}
		var ids []string
		var cursor *string
		for {
			page, err := f.Store.FindTraces(telemetry.TraceQuery{Name: new("root"), Limit: new(3), Cursor: cursor})
			if err != nil {
				return err
			}
			for _, trace := range page.Traces {
				ids = append(ids, trace.TraceID)
			}
			cursor = page.Cursor
			if cursor == nil {
				break
			}
		}
		want := make([]string, len(all))
		for index, trace := range all {
			want[index] = trace.TraceID
		}
		return sameJSON(ids, want, "paged trace IDs")
	})
}

func remoteTraces(ctx context.Context, factory StoreFixtureFactory) error {
	_, restore := jsclock.MockDateNow(20_000)
	defer restore()
	return withStoreFixture(ctx, factory, func(sender StoreFixture) error {
		return withStoreFixture(ctx, factory, func(receiver StoreFixture) error {
			var carrier telemetry.TelemetryCarrier
			if err := sender.Context.StartSpan(telemetry.SpanOptions{Name: "sender"}, func(span telemetry.TelemetrySpan) error {
				carrier = sender.Context.Propagation().Inject(span)
				return nil
			}); err != nil {
				return err
			}
			if carrier == nil {
				return errors.New("expected a carrier")
			}
			if err := sender.flush(ctx); err != nil {
				return err
			}
			sentTraces, err := sender.Store.FindTraces(telemetry.TraceQuery{})
			if err != nil {
				return err
			}
			if len(sentTraces.Traces) == 0 {
				return errors.New("expected the sender trace")
			}
			sent := sentTraces.Traces[0]
			wire, err := roundTrip(carrier)
			if err != nil {
				return err
			}
			remote := receiver.Context.Propagation().Extract(wire)
			if err := remote.StartSpan(telemetry.SpanOptions{Name: "receiver"}, noop); err != nil {
				return err
			}
			if err := receiver.Context.StartSpan(telemetry.SpanOptions{Name: "linked", Links: []telemetry.SpanLink{{Context: remote, Attributes: telemetry.SpanAttributes{telemetry.Attr("reason", "background")}}}}, noop); err != nil {
				return err
			}
			if err := receiver.flush(ctx); err != nil {
				return err
			}
			continued, err := receiver.Store.GetTrace(sent.TraceID)
			if err != nil {
				return err
			}
			if continued == nil || len(continued.Spans) != 1 {
				return fmt.Errorf("continued trace = %v, want one span", continued)
			}
			span := continued.Spans[0]
			if err := errors.Join(
				check(span.TraceID == sent.TraceID, "traceId = %q, want %q", span.TraceID, sent.TraceID),
				check(span.ParentID != nil && *span.ParentID == sent.Spans[0].ID, "parentId = %v, want %q", span.ParentID, sent.Spans[0].ID),
				check(span.Name == "receiver", "name = %q", span.Name),
				check(span.ID != sent.Spans[0].ID, "the receiver reused the sender span ID"),
			); err != nil {
				return err
			}
			linkedTraces, err := receiver.Store.FindTraces(telemetry.TraceQuery{Name: new("linked")})
			if err != nil {
				return err
			}
			if len(linkedTraces.Traces) == 0 {
				return errors.New("expected the linked trace")
			}
			linked := linkedTraces.Traces[0]
			if err := errors.Join(
				check(linked.TraceID != sent.TraceID, "the linked span joined the linked trace"),
				sameJSON(linked.Spans[0].Links, []telemetry.StoredSpanLink{{TraceID: sent.TraceID, SpanID: sent.Spans[0].ID, Attributes: telemetry.SpanAttributes{telemetry.Attr("reason", "background")}}}, "links"),
			); err != nil {
				return err
			}
			for key := range carrier {
				incomplete := maps.Clone(carrier)
				delete(incomplete, key)
				if err := receiver.Context.Propagation().Extract(incomplete).StartSpan(telemetry.SpanOptions{Name: "fallback"}, noop); err != nil {
					return err
				}
			}
			if err := receiver.flush(ctx); err != nil {
				return err
			}
			fallbacks, err := receiver.Store.FindTraces(telemetry.TraceQuery{Name: new("fallback")})
			if err != nil {
				return err
			}
			if len(fallbacks.Traces) != len(carrier) {
				return fmt.Errorf("fallback traces = %d, want %d", len(fallbacks.Traces), len(carrier))
			}
			for _, trace := range fallbacks.Traces {
				if trace.Spans[0].ParentID != nil || trace.TraceID != trace.Spans[0].ID {
					return fmt.Errorf("fallback %v is not a root of its own trace", trace)
				}
			}
			return nil
		})
	})
}

func externalIdentities(ctx context.Context, factory StoreFixtureFactory) error {
	return withStoreFixture(ctx, factory, func(f StoreFixture) error {
		var carrier telemetry.TelemetryCarrier
		if err := f.Context.StartSpan(telemetry.SpanOptions{Name: "source"}, func(span telemetry.TelemetrySpan) error {
			carrier = f.Context.Propagation().Inject(span)
			return nil
		}); err != nil {
			return err
		}
		external := telemetry.TelemetryCarrier{}
		for key := range carrier {
			external[key] = "external-" + key
		}
		parent := f.Context.Propagation().Extract(external)
		if err := reflectEqual(f.Context.Propagation().Inject(parent), external, "injected parent"); err != nil {
			return err
		}
		if err := parent.StartSpan(telemetry.SpanOptions{Name: "continued"}, noop); err != nil {
			return err
		}
		if err := f.flush(ctx); err != nil {
			return err
		}
		found, err := f.Store.FindTraces(telemetry.TraceQuery{Name: new("continued")})
		if err != nil {
			return err
		}
		if len(found.Traces) == 0 {
			return errors.New("expected the continued trace")
		}
		trace := found.Traces[0]
		return errors.Join(
			check(strings.HasPrefix(trace.TraceID, "external-"), "traceId = %q", trace.TraceID),
			check(trace.Spans[0].ParentID != nil && strings.HasPrefix(*trace.Spans[0].ParentID, "external-"), "parentId = %v", trace.Spans[0].ParentID),
		)
	})
}

func reflectEqual(actual, expected any, what string) error {
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("%s: expected %#v, got %#v", what, expected, actual)
	}
	return nil
}

func queriesMetrics(ctx context.Context, factory StoreFixtureFactory) error {
	now, restore := jsclock.MockDateNow(20_001)
	defer restore()
	return withStoreFixture(ctx, factory, func(f StoreFixture) error {
		metrics := f.Context.Metrics()
		counter := metrics.Counter("hits", telemetry.InstrumentOptions{Unit: new("By")})
		counter.Add(2, telemetry.SpanAttributes{telemetry.Attr("method", "read"), telemetry.Attr("valid", true)})
		counter.Add(3, telemetry.SpanAttributes{telemetry.Attr("valid", true), telemetry.Attr("method", "read")})
		counter.Add(11, telemetry.SpanAttributes{telemetry.Attr("method", "write"), telemetry.Attr("valid", 1.0)})
		metrics.Counter("other", telemetry.InstrumentOptions{}).Add(100, telemetry.SpanAttributes{telemetry.Attr("method", "read"), telemetry.Attr("valid", true)})
		now.Set(30_001)
		counter.Add(7, telemetry.SpanAttributes{telemetry.Attr("method", "read"), telemetry.Attr("valid", true)})
		now.Set(40_001)
		if err := f.flush(ctx); err != nil {
			return err
		}
		query := telemetry.MetricQuery{Name: "hits", StartMin: new(20_000.0), StartMax: new(20_000.0), Attributes: map[string]telemetry.AttributeValue{"method": "read", "valid": true}}
		rows, err := f.Store.FindMetrics(query)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return errors.New("expected metric rows")
		}
		count, sum := sumMetric(rows)
		minimum, maximum := rows[0].Min, rows[0].Max
		for _, row := range rows {
			minimum, maximum = min(minimum, row.Min), max(maximum, row.Max)
			if row.Kind != telemetry.TelemetryMetricKindCounter || row.Unit == nil || *row.Unit != "By" || row.IntervalMs != 20_000 {
				return fmt.Errorf("row = %+v, want a counter in By at interval 20000", row)
			}
		}
		if err := errors.Join(
			check(count == 2, "count = %v, want 2", count),
			check(sum == 5, "sum = %v, want 5", sum),
			check(minimum == 2, "min = %v, want 2", minimum),
			check(maximum == 3, "max = %v, want 3", maximum),
		); err != nil {
			return err
		}
		rows[0].Attributes.Set("method", "changed")
		again, err := f.Store.FindMetrics(query)
		if err != nil {
			return err
		}
		if len(again) == 0 {
			return errors.New("expected metric rows again")
		}
		if err := check(again[0].Attributes.Value("method") == "read", "attributes method = %v, want read", again[0].Attributes.Value("method")); err != nil {
			return err
		}
		missing, err := f.Store.FindMetrics(telemetry.MetricQuery{Name: "missing"})
		if err != nil {
			return err
		}
		typed, err := f.Store.FindMetrics(telemetry.MetricQuery{Name: "hits", Attributes: map[string]telemetry.AttributeValue{"valid": 1.0}})
		if err != nil {
			return err
		}
		recent, err := f.Store.FindMetrics(telemetry.MetricQuery{Name: "hits", StartMin: new(30_000.0)})
		if err != nil {
			return err
		}
		_, typedSum := sumMetric(typed)
		_, recentSum := sumMetric(recent)
		return errors.Join(
			check(len(missing) == 0, "missing metric rows = %v", missing),
			check(typedSum == 11, "typed sum = %v, want 11", typedSum),
			check(recentSum == 7, "recent sum = %v, want 7", recentSum),
		)
	})
}

func attributeNames(attributes telemetry.SpanAttributes) []string {
	names := make([]string, len(attributes))
	for index, attribute := range attributes {
		names[index] = attribute.Name
	}
	return names
}

// preservesAttributeOrder checks that attribute order is observable as in an object: insertion order, an overwritten
// name keeping its position and integer-like names first. Metric rows are checked by name only: a store may report a
// series' attributes in a canonical order.
func preservesAttributeOrder(ctx context.Context, factory StoreFixtureFactory) error {
	now, restore := jsclock.MockDateNow(20_000)
	defer restore()
	return withStoreFixture(ctx, factory, func(f StoreFixture) error {
		bag := func(names ...string) telemetry.SpanAttributes {
			attributes := telemetry.SpanAttributes{}
			for index, name := range names {
				attributes = append(attributes, telemetry.Attr(name, float64(index)))
			}
			return attributes
		}
		var target telemetry.TelemetrySpan
		if err := f.Context.StartSpan(telemetry.SpanOptions{Name: "target"}, func(span telemetry.TelemetrySpan) error {
			target = span
			return nil
		}); err != nil {
			return err
		}
		if err := f.Context.StartSpan(telemetry.SpanOptions{Name: "ordered", Attributes: bag("zeta", "10", "alpha", "2"), Links: []telemetry.SpanLink{{Context: target, Attributes: bag("link.z", "link.a")}}}, func(span telemetry.TelemetrySpan) error {
			span.SetAttributes(telemetry.SpanAttributes{telemetry.Attr("alpha", 9.0), telemetry.Attr("beta", 1.0)})
			span.AddEvent("event", bag("event.z", "event.a"))
			return nil
		}); err != nil {
			return err
		}
		f.Context.Metrics().Counter("ordered", telemetry.InstrumentOptions{}).Add(1, bag("metric.z", "metric.a"))
		now.Set(30_001)
		if err := f.flush(ctx); err != nil {
			return err
		}
		found, err := f.Store.FindTraces(telemetry.TraceQuery{Name: new("ordered")})
		if err != nil {
			return err
		}
		if len(found.Traces) == 0 {
			return errors.New("expected the ordered trace")
		}
		span := found.Traces[0].Spans[0]
		if len(span.Events) == 0 || len(span.Links) == 0 {
			return fmt.Errorf("ordered span = %+v, want an event and a link", span)
		}
		rows, err := f.Store.FindMetrics(telemetry.MetricQuery{Name: "ordered"})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return errors.New("expected the ordered metric row")
		}
		return errors.Join(
			deepStrictEqual(attributeNames(span.Attributes), []string{"2", "10", "zeta", "alpha", "beta"}, "span attribute order"),
			deepStrictEqual(attributeNames(span.Events[0].Attributes), []string{"event.z", "event.a"}, "event attribute order"),
			deepStrictEqual(attributeNames(span.Links[0].Attributes), []string{"link.z", "link.a"}, "link attribute order"),
			// A metric row is an aggregate of a series, and a store may report the series' attributes in a canonical order, as
			// Pi's SQLite store does with sorted names, so only the row's names are compared.
			deepStrictEqual(slices.Sorted(slices.Values(attributeNames(rows[0].Attributes))), []string{"metric.a", "metric.z"}, "metric attribute names"),
		)
	})
}
