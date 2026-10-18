package telemetrytest

// Ports packages/telemetry/src/testing/conformance.ts.
//
// Go mapping: StartSpan blocks until its callback returns, so a callback's result is captured by the closure and a
// rejected Promise is the callback's returned error, compared by identity as strictEqual does. A callback that
// awaits before rejecting returns the error it receives from another goroutine; a synchronous throw also has the
// Go form of a panic, which the adapter records and re-raises with the same value. A Promise that the callback does
// not await is a goroutine the callback joins. Upstream's unreadable error (a Proxy whose traps throw) is an error
// whose Error method panics, and an unreadable payload value is one whose String, MarshalJSON and MarshalText methods
// panic: an adapter that inspects it fails, while one that only stores it does not. The three passivity cases that
// read a Proxy-trapped SpanOptions, SpanAttributes or SpanStatus keep their upstream names. Reading a Go SpanOptions
// or SpanAttributes struct or map cannot fail, so the two that build one assert what survives in Go: the callback
// runs once, no panic escapes, the span is recorded and succeeds, and each call with the unreadable payload is atomic
// (an adapter that inspects the payload records none of it, one that only stores it records all of it). Upstream's
// stricter "nothing was recorded" state is one of those two outcomes. "ignores failed status calls atomically" is not ported: a Go SpanStatus holds two
// strings and has no failing read. A rejection with undefined has no Go form, because a nil error is success.
//
// The metrics, links and propagation cases follow the same rule. A Go options struct, link, carrier or attribute bag
// cannot fail to read, so upstream's Proxy-trapped inputs have these forms: an unreadable options bag and an unreadable
// carrier have none and are not ported; an unreadable link object is a link with no target or with a target whose
// methods panic, which no adapter may record or call; and an unreadable attribute array is a value whose methods
// panic, after which a call records all of its payload or none of it. Mutating an input after the call is mutating the
// Go slice or pointer in place, as Go values are not shared by reference otherwise. A fixture without Propagation or
// Metrics skips those cases, as upstream's fixtures without the optional capability do.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"

	"github.com/MichaelKinsy/PiG/telemetry"
)

type conformanceTest func(context.Context, TelemetryAdapterFixture) error

// withFixture is `await using fixture = await factory()`: it runs test on a fresh fixture and disposes it afterwards,
// joining a dispose failure to the test's.
func withFixture(ctx context.Context, factory TelemetryAdapterFixtureFactory, test conformanceTest) (err error) {
	fixture, err := factory(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if fixture.Dispose == nil {
			return
		}
		if disposeErr := fixture.Dispose(ctx); disposeErr != nil {
			err = errors.Join(err, disposeErr)
		}
	}()
	return test(ctx, fixture)
}

func createCase(factory TelemetryAdapterFixtureFactory, group, name string, test conformanceTest) TelemetryAdapterConformanceCase {
	return TelemetryAdapterConformanceCase{
		Group: group,
		Name:  name,
		Run:   func(ctx context.Context) error { return withFixture(ctx, factory, test) },
	}
}

func findSpan(spans []telemetry.RecordedTelemetrySpan, name string) (telemetry.RecordedTelemetrySpan, error) {
	for _, span := range spans {
		if span.Name == name {
			return span, nil
		}
	}
	return telemetry.RecordedTelemetrySpan{}, fmt.Errorf("Expected recorded span %s", name)
}

func findRecorded(ctx context.Context, fixture TelemetryAdapterFixture, name string) (telemetry.RecordedTelemetrySpan, error) {
	spans, err := fixture.GetSpans(ctx)
	if err != nil {
		return telemetry.RecordedTelemetrySpan{}, err
	}
	return findSpan(spans, name)
}

func rejectsWithSameValue(err, expected error) error {
	if err == nil {
		return errors.New("Expected operation to reject")
	}
	if err != expected { //nolint:errorlint // strictEqual: the adapter must return the callback's exact error value, so a wrapped error fails.
		// The values are not formatted: the unreadable error panics when inspected.
		return errors.New("Expected the operation to reject with the thrown value")
	}
	return nil
}

func deepStrictEqual(actual, expected any, what string) error {
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("%s: expected %#v, got %#v", what, expected, actual)
	}
	return nil
}

func strictEqual[T comparable](actual, expected T, what string) error {
	if actual != expected {
		return fmt.Errorf("%s: expected %v, got %v", what, expected, actual)
	}
	return nil
}

// unreadableValue is the Go form of upstream's unreadable payload: every way an adapter might inspect it panics.
type unreadableValue struct{}

func (unreadableValue) String() string               { panic("read") }
func (unreadableValue) GoString() string             { panic("read") }
func (unreadableValue) MarshalJSON() ([]byte, error) { panic("read") }
func (unreadableValue) MarshalText() ([]byte, error) { panic("read") }

// unreadableError is the Go form of upstream's unreadable thrown value: inspecting it panics.
type unreadableError struct{ kind string }

func (*unreadableError) Error() string { panic("read") }

// afterAwait returns err from another goroutine, the Go form of a callback that awaits before rejecting.
func afterAwait(err error) error {
	result := make(chan error, 1)
	go func() { result <- err }()
	return <-result
}

// panicsWith runs operation and returns the value it panicked with, or nil.
func panicsWith(operation func() error) (value any) {
	defer func() { value = recover() }()
	_ = operation()
	return nil
}

// unreadableContext is the Go form of upstream's unreadable link target: every method an adapter might call on it panics.
type unreadableContext struct{}

func (unreadableContext) StartSpan(telemetry.SpanOptions, func(telemetry.TelemetrySpan) error) error {
	panic("read")
}

// doesNotThrow is node:assert's doesNotThrow: a panic out of operation is the failure.
func doesNotThrow(operation func()) (err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("Expected the call not to throw, got %v", failure)
		}
	}()
	operation()
	return nil
}

// roundTrip is JSON.parse(JSON.stringify(carrier)): the carrier as a transport delivers it.
func roundTrip(carrier telemetry.TelemetryCarrier) (telemetry.TelemetryCarrier, error) {
	//portlint:allow mapkeyorder a carrier is an unordered string map: the receiver reads it by key
	encoded, err := json.Marshal(carrier)
	if err != nil {
		return nil, err
	}
	var decoded telemetry.TelemetryCarrier
	//portlint:allow jsonclone the round trip is the case: it is the transport that serializes the carrier between processes
	return decoded, json.Unmarshal(encoded, &decoded) //perf:ok conformance test support: the JSON round trip is the transport under test, not a clone
}

func measurementsNamed(measurements []telemetry.RecordedTelemetryMeasurement, name string) []telemetry.RecordedTelemetryMeasurement {
	return slices.DeleteFunc(slices.Clone(measurements), func(measurement telemetry.RecordedTelemetryMeasurement) bool { return measurement.Name != name })
}

func stringPointerEqual(actual *string, expected string, what string) error {
	if actual == nil {
		return fmt.Errorf("%s: expected %q, got undefined", what, expected)
	}
	return strictEqual(*actual, expected, what)
}

func status(code telemetry.SpanStatusCode) telemetry.SpanStatus {
	return telemetry.SpanStatus{Status: code}
}

func errorStatus(name, message string) telemetry.SpanStatus {
	return telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: name, Message: message}}
}

// CreateTelemetryAdapterConformance creates runner-independent cases for the callback telemetry adapter contract.
func CreateTelemetryAdapterConformance(factory TelemetryAdapterFixtureFactory) []TelemetryAdapterConformanceCase {
	return []TelemetryAdapterConformanceCase{
		createCase(factory, "metrics", "records ordered measurements and optional histogram exemplars", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			if fixture.Metrics == nil {
				return nil
			}
			if fixture.GetMetrics == nil {
				return errors.New("Metrics fixtures require a normalized measurement reader")
			}
			fixture.Metrics.Counter("bytes", telemetry.InstrumentOptions{Description: new("Encoded bytes"), Unit: new("By")}).
				Add(12, telemetry.SpanAttributes{"direction": "out"})
			active := fixture.Metrics.UpDownCounter("active", telemetry.InstrumentOptions{})
			active.Add(1, nil)
			active.Add(-1, nil)
			histogram := fixture.Metrics.Histogram("duration", telemetry.HistogramOptions{InstrumentOptions: telemetry.InstrumentOptions{Unit: new("ms")}, Boundaries: []float64{1, 10}})
			if err := fixture.Context.StartSpan(telemetry.SpanOptions{Name: "measured"}, func(span telemetry.TelemetrySpan) error {
				histogram.Record(5, telemetry.SpanAttributes{"method": "read"}, span)
				return nil
			}); err != nil {
				return err
			}
			measurements, err := fixture.GetMetrics(ctx)
			if err != nil {
				return err
			}
			type summary struct {
				Kind  telemetry.TelemetryMetricKind
				Name  string
				Value float64
			}
			summaries := make([]summary, len(measurements))
			for i, measurement := range measurements {
				summaries[i] = summary{measurement.Kind, measurement.Name, measurement.Value}
			}
			if err := deepStrictEqual(summaries, []summary{
				{telemetry.TelemetryMetricKindCounter, "bytes", 12},
				{telemetry.TelemetryMetricKindUpDownCounter, "active", 1},
				{telemetry.TelemetryMetricKindUpDownCounter, "active", -1},
				{telemetry.TelemetryMetricKindHistogram, "duration", 5},
			}, "measurements"); err != nil {
				return err
			}
			measured, err := findRecorded(ctx, fixture, "measured")
			if err != nil {
				return err
			}
			return errors.Join(
				deepStrictEqual(measurements[0].Attributes, telemetry.SpanAttributes{"direction": "out"}, "counter attributes"),
				stringPointerEqual(measurements[0].Description, "Encoded bytes", "counter description"),
				stringPointerEqual(measurements[0].Unit, "By", "counter unit"),
				deepStrictEqual(measurements[3].Boundaries, []float64{1, 10}, "histogram boundaries"),
				deepStrictEqual(measurements[3].Context, measured.Context, "histogram exemplar"),
			)
		}),

		createCase(factory, "metrics", "copies inputs and suppresses invalid measurements atomically", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			if fixture.Metrics == nil {
				return nil
			}
			if fixture.GetMetrics == nil {
				return errors.New("Metrics fixtures require a normalized measurement reader")
			}
			// The instrument's options and the measurement's attributes are copied: mutating them in place afterwards changes nothing recorded.
			boundaries := []float64{1, 10}
			description := "Original"
			options := telemetry.HistogramOptions{InstrumentOptions: telemetry.InstrumentOptions{Description: &description}, Boundaries: boundaries}
			histogram := fixture.Metrics.Histogram("snapshot", options)
			boundaries[0], boundaries[1] = 2, 100
			description = "Changed"
			tags := []string{"original"}
			histogram.Record(-1, telemetry.SpanAttributes{"tags": tags}, nil)
			tags[0] = "changed"
			counter := fixture.Metrics.Counter("invalid", telemetry.InstrumentOptions{})
			for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
				if err := doesNotThrow(func() { counter.Add(value, nil) }); err != nil {
					return err
				}
			}
			// Upstream's attribute bag holds an unreadable array, so the call fails and records nothing. A Go bag cannot fail to
			// read, but a value whose methods panic can: an adapter that cannot read it records none of the call, and one that
			// only stores the values records all of it (checked below).
			if err := doesNotThrow(func() {
				counter.Add(1, telemetry.SpanAttributes{"partial": true, "broken": unreadableValue{}})
			}); err != nil {
				return err
			}
			// Upstream's unreadable options bag has no Go form: a Go options struct cannot fail to read.
			if err := doesNotThrow(func() {
				fixture.Metrics.Histogram("bad-boundaries", telemetry.HistogramOptions{Boundaries: []float64{2, 1}}).Record(1, nil, nil)
			}); err != nil {
				return err
			}
			measurements, err := fixture.GetMetrics(ctx)
			if err != nil {
				return err
			}
			snapshots := measurementsNamed(measurements, "snapshot")
			if err := strictEqual(len(snapshots), 1, "snapshot measurements"); err != nil {
				return err
			}
			invalid := measurementsNamed(measurements, "invalid")
			if len(invalid) > 1 || (len(invalid) == 1 && (invalid[0].Value != 1 || len(invalid[0].Attributes) != 2)) {
				return fmt.Errorf("invalid measurements: expected none or the whole unreadable call, got %v", invalid)
			}
			return errors.Join(
				strictEqual(len(measurements), 1+len(invalid), "measurement count"),
				deepStrictEqual(snapshots[0].Attributes, telemetry.SpanAttributes{"tags": []string{"original"}}, "attributes"),
				deepStrictEqual(snapshots[0].Boundaries, []float64{1, 10}, "boundaries"),
				stringPointerEqual(snapshots[0].Description, "Original", "description"),
				deepStrictEqual(snapshots[0].Context, telemetry.TelemetryCarrier(nil), "context"),
			)
		}),

		createCase(factory, "links", "records initial and late links without changing parents", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			var target telemetry.TelemetrySpan
			if err := fixture.Context.StartSpan(telemetry.SpanOptions{Name: "target"}, func(span telemetry.TelemetrySpan) error {
				target = span
				return nil
			}); err != nil {
				return err
			}
			if target == nil {
				return errors.New("Expected target span")
			}
			reason := []string{"initial"}
			if err := fixture.Context.StartSpan(telemetry.SpanOptions{Name: "linked", Links: []telemetry.SpanLink{{Context: target, Attributes: telemetry.SpanAttributes{"reason": reason}}}}, func(span telemetry.TelemetrySpan) error {
				reason[0] = "mutated"
				span.AddLink(telemetry.SpanLink{Context: target, Attributes: telemetry.SpanAttributes{"reason": "late"}})
				span.AddLink(telemetry.SpanLink{Context: telemetry.NoopTelemetryContext})
				return nil
			}); err != nil {
				return err
			}
			spans, err := fixture.GetSpans(ctx)
			if err != nil {
				return err
			}
			linked, err := findSpan(spans, "linked")
			if err != nil {
				return err
			}
			targetSpan, err := findSpan(spans, "target")
			if err != nil {
				return err
			}
			if linked.ParentID != nil {
				return fmt.Errorf("linked.parentId: expected null, got %d", *linked.ParentID)
			}
			return errors.Join(
				deepStrictEqual(linked.ParentContext, telemetry.TelemetryCarrier(nil), "linked.parentContext"),
				deepStrictEqual(linked.Links, []telemetry.RecordedTelemetryLink{
					{Context: targetSpan.Context, Attributes: telemetry.SpanAttributes{"reason": []string{"initial"}}},
					{Context: targetSpan.Context, Attributes: telemetry.SpanAttributes{"reason": "late"}},
				}, "links"),
			)
		}),

		createCase(factory, "links", "suppresses malformed links atomically and ignores settled sources", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			var source telemetry.TelemetrySpan
			if err := fixture.Context.StartSpan(telemetry.SpanOptions{Name: "link-source"}, func(span telemetry.TelemetrySpan) error {
				source = span
				// Upstream's unreadable link object has no Go form. A target with no identity (no context) and an unsupported
				// target whose every method panics stand for a malformed link: neither may be recorded or inspected.
				if err := doesNotThrow(func() { span.AddLink(telemetry.SpanLink{}) }); err != nil {
					return err
				}
				if err := doesNotThrow(func() { span.AddLink(telemetry.SpanLink{Context: unreadableContext{}}) }); err != nil {
					return err
				}
				// The attribute bag holds a value whose methods panic: an adapter that cannot read it records none of the link, and
				// one that only stores the values records the whole link (checked below).
				return doesNotThrow(func() {
					span.AddLink(telemetry.SpanLink{Context: span, Attributes: telemetry.SpanAttributes{"partial": true, "broken": unreadableValue{}}})
				})
			}); err != nil {
				return err
			}
			if source == nil {
				return errors.New("Expected source span")
			}
			if err := doesNotThrow(func() {
				source.AddLink(telemetry.SpanLink{Context: source, Attributes: telemetry.SpanAttributes{"late": true}})
			}); err != nil {
				return err
			}
			linkSource, err := findRecorded(ctx, fixture, "link-source")
			if err != nil {
				return err
			}
			links := linkSource.Links
			if len(links) > 1 || (len(links) == 1 && (len(links[0].Attributes) != 2 || links[0].Attributes["partial"] == nil || links[0].Attributes["broken"] == nil)) {
				return fmt.Errorf("links: expected none or the whole unreadable link, got %v", links)
			}
			return nil
		}),

		createCase(factory, "propagation", "roundtrips parent identity through JSON without retaining the carrier", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			if fixture.Propagation == nil {
				return nil
			}
			propagation := fixture.Propagation
			if err := fixture.Context.StartSpan(telemetry.SpanOptions{Name: "sender"}, func(span telemetry.TelemetrySpan) error {
				carrier := propagation.Inject(span)
				if carrier == nil {
					return errors.New("Expected a carrier for the span")
				}
				wire, err := roundTrip(carrier)
				if err != nil {
					return err
				}
				parent := propagation.Extract(wire)
				for key := range wire {
					wire[key] = "mutated"
				}
				if err := parent.StartSpan(telemetry.SpanOptions{Name: "receiver"}, func(telemetry.TelemetrySpan) error { return nil }); err != nil {
					return err
				}
				return deepStrictEqual(propagation.Inject(span), carrier, "carrier after extraction")
			}); err != nil {
				return err
			}
			spans, err := fixture.GetSpans(ctx)
			if err != nil {
				return err
			}
			receiver, err := findSpan(spans, "receiver")
			if err != nil {
				return err
			}
			sender, err := findSpan(spans, "sender")
			if err != nil {
				return err
			}
			return deepStrictEqual(receiver.ParentContext, sender.Context, "receiver.parentContext")
		}),

		createCase(factory, "propagation", "unknown and unreadable carriers produce recorded roots", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			if fixture.Propagation == nil {
				return nil
			}
			if carrier := fixture.Propagation.Inject(telemetry.NoopTelemetryContext); carrier != nil {
				return fmt.Errorf("a no-op context injects %v, expected undefined", carrier)
			}
			// Upstream's fourth carrier is an unreadable Proxy, which has no Go form: a Go map cannot fail to read.
			for index, carrier := range []telemetry.TelemetryCarrier{nil, {}, {"unknown": "format"}} {
				result := 0
				if err := doesNotThrow(func() {
					root := fixture.Propagation.Extract(carrier)
					if root == nil {
						panic("Expected root context")
					}
					_ = root.StartSpan(telemetry.SpanOptions{Name: fmt.Sprintf("fallback-%d", index)}, func(telemetry.TelemetrySpan) error {
						result = 42
						return nil
					})
				}); err != nil {
					return err
				}
				if err := strictEqual(result, 42, "fallback callback result"); err != nil {
					return err
				}
			}
			spans, err := fixture.GetSpans(ctx)
			if err != nil {
				return err
			}
			for _, span := range spans {
				if span.ParentContext != nil {
					return fmt.Errorf("%s.parentContext: expected null, got %v", span.Name, span.ParentContext)
				}
			}
			return nil
		}),

		createCase(factory, "propagation", "propagates across fresh adapters and multiple hops", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			if fixture.Propagation == nil {
				return nil
			}
			return withFixture(ctx, factory, func(ctx context.Context, receiver TelemetryAdapterFixture) error {
				return withFixture(ctx, factory, func(ctx context.Context, worker TelemetryAdapterFixture) error {
					if receiver.Propagation == nil || worker.Propagation == nil {
						return errors.New("Expected propagation on each fixture")
					}
					var senderCarrier telemetry.TelemetryCarrier
					if err := fixture.Context.StartSpan(telemetry.SpanOptions{Name: "sender"}, func(span telemetry.TelemetrySpan) error {
						senderCarrier = fixture.Propagation.Inject(span)
						return nil
					}); err != nil {
						return err
					}
					if senderCarrier == nil {
						return errors.New("Expected a sender carrier")
					}
					wire, err := roundTrip(senderCarrier)
					if err != nil {
						return err
					}
					var receiverCarrier telemetry.TelemetryCarrier
					if err := receiver.Propagation.Extract(wire).StartSpan(telemetry.SpanOptions{Name: "receiver"}, func(span telemetry.TelemetrySpan) error {
						receiverCarrier = receiver.Propagation.Inject(span)
						return nil
					}); err != nil {
						return err
					}
					if receiverCarrier == nil {
						return errors.New("Expected a receiver carrier")
					}
					wire, err = roundTrip(receiverCarrier)
					if err != nil {
						return err
					}
					if err := worker.Propagation.Extract(wire).StartSpan(telemetry.SpanOptions{Name: "worker"}, func(telemetry.TelemetrySpan) error { return nil }); err != nil {
						return err
					}
					receiverSpan, err := findRecorded(ctx, receiver, "receiver")
					if err != nil {
						return err
					}
					workerSpan, err := findRecorded(ctx, worker, "worker")
					if err != nil {
						return err
					}
					return errors.Join(
						deepStrictEqual(receiverSpan.ParentContext, senderCarrier, "receiver.parentContext"),
						deepStrictEqual(workerSpan.ParentContext, receiverCarrier, "worker.parentContext"),
					)
				})
			})
		}),

		createCase(factory, "callback lifecycle", "admits once synchronously and preserves the result", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			admitted := false
			calls := 0
			expected := &struct{ value int }{42}
			var result any
			err := fixture.Context.StartSpan(telemetry.SpanOptions{Name: "success"}, func(telemetry.TelemetrySpan) error {
				admitted = true
				calls++
				result = expected
				return nil
			})
			if err := errors.Join(strictEqual(admitted, true, "admitted"), strictEqual(calls, 1, "calls"), err); err != nil {
				return err
			}
			if result != expected {
				return fmt.Errorf("result: expected %p, got %v", expected, result)
			}
			span, err := findRecorded(ctx, fixture, "success")
			if err != nil {
				return err
			}
			return errors.Join(deepStrictEqual(span.Status, status(telemetry.SpanStatusCodeOK), "status"), strictEqual(span.Settled, true, "settled"))
		}),

		createCase(factory, "callback lifecycle", "preserves synchronous and asynchronous rejection values", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			start := func(name string, callback func(telemetry.TelemetrySpan) error) error {
				return fixture.Context.StartSpan(telemetry.SpanOptions{Name: name}, callback)
			}
			syncError := errors.New("sync")
			if err := rejectsWithSameValue(start("sync-error", func(telemetry.TelemetrySpan) error { return syncError }), syncError); err != nil {
				return err
			}
			asyncError := errors.New("async")
			if err := rejectsWithSameValue(start("async-error", func(telemetry.TelemetrySpan) error { return afterAwait(asyncError) }), asyncError); err != nil {
				return err
			}
			var unreadable error = &unreadableError{kind: "unreadable"}
			if err := rejectsWithSameValue(start("unreadable-error", func(telemetry.TelemetrySpan) error { return unreadable }), unreadable); err != nil {
				return err
			}
			var asyncUnreadable error = &unreadableError{kind: "async-unreadable"}
			if err := rejectsWithSameValue(start("async-unreadable-error", func(telemetry.TelemetrySpan) error { return afterAwait(asyncUnreadable) }), asyncUnreadable); err != nil {
				return err
			}
			thrown := &struct{ kind string }{"panic"}
			if value := panicsWith(func() error {
				return start("panic-error", func(telemetry.TelemetrySpan) error { panic(thrown) })
			}); value != any(thrown) {
				return fmt.Errorf("panic: expected %p, got %v", thrown, value)
			}

			spans, err := fixture.GetSpans(ctx)
			if err != nil {
				return err
			}
			for _, name := range []string{"sync-error", "async-error", "unreadable-error", "async-unreadable-error", "panic-error"} {
				span, err := findSpan(spans, name)
				if err != nil {
					return err
				}
				if err := strictEqual(span.Status.Status, telemetry.SpanStatusCodeError, name+" status"); err != nil {
					return err
				}
			}
			return nil
		}),

		createCase(factory, "status", "uses last explicit status without automatic overwrite", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			if err := fixture.Context.StartSpan(telemetry.SpanOptions{Name: "last-status"}, func(span telemetry.TelemetrySpan) error {
				span.SetStatus(errorStatus("Expected", "first"))
				span.SetStatus(status(telemetry.SpanStatusCodeOK))
				return nil
			}); err != nil {
				return err
			}

			thrown := errors.New("after explicit status")
			if err := rejectsWithSameValue(fixture.Context.StartSpan(telemetry.SpanOptions{Name: "explicit-before-throw"}, func(span telemetry.TelemetrySpan) error {
				span.SetStatus(status(telemetry.SpanStatusCodeOK))
				return thrown
			}), thrown); err != nil {
				return err
			}

			rejected := errors.New("after async explicit status")
			if err := rejectsWithSameValue(fixture.Context.StartSpan(telemetry.SpanOptions{Name: "explicit-before-rejection"}, func(span telemetry.TelemetrySpan) error {
				span.SetStatus(errorStatus("Expected", "async failure"))
				return afterAwait(rejected)
			}), rejected); err != nil {
				return err
			}

			if err := fixture.Context.StartSpan(telemetry.SpanOptions{Name: "expected-failure"}, func(span telemetry.TelemetrySpan) error {
				span.SetStatus(errorStatus("Expected", "returned failure"))
				return nil
			}); err != nil {
				return err
			}

			spans, err := fixture.GetSpans(ctx)
			if err != nil {
				return err
			}
			for _, want := range []struct {
				name   string
				status telemetry.SpanStatus
			}{
				{"last-status", status(telemetry.SpanStatusCodeOK)},
				{"explicit-before-throw", status(telemetry.SpanStatusCodeOK)},
				{"explicit-before-rejection", errorStatus("Expected", "async failure")},
				{"expected-failure", errorStatus("Expected", "returned failure")},
			} {
				span, err := findSpan(spans, want.name)
				if err != nil {
					return err
				}
				if err := deepStrictEqual(span.Status, want.status, want.name+" status"); err != nil {
					return err
				}
			}
			return nil
		}),

		createCase(factory, "recording", "merges attributes and records ordered events", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			if err := fixture.Context.StartSpan(telemetry.SpanOptions{
				Name:       "recording",
				Attributes: telemetry.SpanAttributes{"start": "value", "overwrite": "start", "ignored": nil},
			}, func(span telemetry.TelemetrySpan) error {
				span.SetAttributes(telemetry.SpanAttributes{"count": 1, "overwrite": "middle"})
				span.SetAttributes(telemetry.SpanAttributes{"count": nil, "overwrite": "end"})
				span.AddEvent("first", telemetry.SpanAttributes{"index": 1, "ignored": nil})
				span.AddEvent("second", telemetry.SpanAttributes{"index": 2})
				return nil
			}); err != nil {
				return err
			}

			span, err := findRecorded(ctx, fixture, "recording")
			if err != nil {
				return err
			}
			return errors.Join(
				deepStrictEqual(span.Attributes, telemetry.SpanAttributes{"start": "value", "overwrite": "end", "count": 1}, "attributes"),
				deepStrictEqual(span.Events, []telemetry.RecordedTelemetryEvent{
					{Name: "first", Attributes: telemetry.SpanAttributes{"index": 1}},
					{Name: "second", Attributes: telemetry.SpanAttributes{"index": 2}},
				}, "events"),
			)
		}),

		createCase(factory, "recording", "ignores failed attribute calls atomically", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			if err := fixture.Context.StartSpan(telemetry.SpanOptions{Name: "atomic-attributes", Attributes: telemetry.SpanAttributes{"retained": "value"}}, func(span telemetry.TelemetrySpan) error {
				span.SetAttributes(telemetry.SpanAttributes{"partial": "must not survive alone", "unreadable": []any{unreadableValue{}}})
				return nil
			}); err != nil {
				return err
			}
			span, err := findRecorded(ctx, fixture, "atomic-attributes")
			if err != nil {
				return err
			}
			// The call is atomic: an adapter that cannot read the payload records none of it, and one that only
			// stores the values records all of them.
			_, partial := span.Attributes["partial"]
			_, unreadable := span.Attributes["unreadable"]
			if partial != unreadable || len(span.Attributes) != map[bool]int{false: 1, true: 3}[partial] {
				return fmt.Errorf("attributes: expected all or none of the failed call, got keys %v", slices.Sorted(maps.Keys(span.Attributes)))
			}
			return strictEqual(span.Attributes["retained"], any("value"), "retained attribute")
		}),

		createCase(factory, "recording", "makes calls after settlement inert", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			var settledSpan telemetry.TelemetrySpan
			if err := fixture.Context.StartSpan(telemetry.SpanOptions{Name: "settled", Attributes: telemetry.SpanAttributes{"value": "initial"}}, func(span telemetry.TelemetrySpan) error {
				settledSpan = span
				return nil
			}); err != nil {
				return err
			}
			if settledSpan == nil {
				return errors.New("Expected callback span")
			}

			settledSpan.SetAttributes(telemetry.SpanAttributes{"value": "late"})
			settledSpan.AddEvent("late", telemetry.SpanAttributes{"value": true})
			settledSpan.SetStatus(status(telemetry.SpanStatusCodeError))
			childAdmitted := false
			childResult := 0
			if err := settledSpan.StartSpan(telemetry.SpanOptions{Name: "late-child"}, func(telemetry.TelemetrySpan) error {
				childAdmitted = true
				childResult = 7
				return nil
			}); err != nil {
				return err
			}
			if err := errors.Join(strictEqual(childAdmitted, true, "child admitted"), strictEqual(childResult, 7, "child result")); err != nil {
				return err
			}

			spans, err := fixture.GetSpans(ctx)
			if err != nil {
				return err
			}
			if err := strictEqual(len(spans), 1, "span count"); err != nil {
				return err
			}
			return errors.Join(
				deepStrictEqual(spans[0].Attributes, telemetry.SpanAttributes{"value": "initial"}, "attributes"),
				deepStrictEqual(spans[0].Events, []telemetry.RecordedTelemetryEvent{}, "events"),
				deepStrictEqual(spans[0].Status, status(telemetry.SpanStatusCodeOK), "status"),
			)
		}),

		createCase(factory, "parentage", "records nested and concurrent child relationships", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			releaseFirst := make(chan struct{})
			if err := fixture.Context.StartSpan(telemetry.SpanOptions{Name: "parent"}, func(parent telemetry.TelemetrySpan) error {
				firstAdmitted := make(chan struct{})
				first := make(chan error, 1)
				go func() {
					first <- parent.StartSpan(telemetry.SpanOptions{Name: "first-child"}, func(telemetry.TelemetrySpan) error {
						close(firstAdmitted)
						<-releaseFirst
						return nil
					})
				}()
				<-firstAdmitted
				second := ""
				if err := parent.StartSpan(telemetry.SpanOptions{Name: "second-child"}, func(telemetry.TelemetrySpan) error {
					second = "done"
					return nil
				}); err != nil {
					return err
				}
				if err := strictEqual(second, "done", "second result"); err != nil {
					return err
				}
				close(releaseFirst)
				return <-first
			}); err != nil {
				return err
			}

			spans, err := fixture.GetSpans(ctx)
			if err != nil {
				return err
			}
			parent, err := findSpan(spans, "parent")
			if err != nil {
				return err
			}
			first, err := findSpan(spans, "first-child")
			if err != nil {
				return err
			}
			second, err := findSpan(spans, "second-child")
			if err != nil {
				return err
			}
			if parent.ParentID != nil {
				return fmt.Errorf("parent.parentId: expected null, got %d", *parent.ParentID)
			}
			if first.ParentID == nil || *first.ParentID != parent.ID || second.ParentID == nil || *second.ParentID != parent.ID {
				return errors.New("children must record the parent span ID")
			}
			if second.EndSequence == nil || first.EndSequence == nil || parent.EndSequence == nil {
				return errors.New("settled spans must record an end sequence")
			}
			if *second.EndSequence >= *first.EndSequence || *first.EndSequence >= *parent.EndSequence {
				return fmt.Errorf("end order: second %d, first %d, parent %d", *second.EndSequence, *first.EndSequence, *parent.EndSequence)
			}
			return nil
		}),

		createCase(factory, "passivity", "suppresses unreadable telemetry payload failures", func(ctx context.Context, fixture TelemetryAdapterFixture) error {
			calls, result := 0, 0
			options := telemetry.SpanOptions{Name: "unreadable-options", Attributes: telemetry.SpanAttributes{"secret": unreadableValue{}}}
			if err := fixture.Context.StartSpan(options, func(telemetry.TelemetrySpan) error {
				calls++
				result = 9
				return nil
			}); err != nil {
				return err
			}
			if err := errors.Join(strictEqual(calls, 1, "calls"), strictEqual(result, 9, "result")); err != nil {
				return err
			}
			if err := fixture.Context.StartSpan(telemetry.SpanOptions{Name: "unreadable-recording"}, func(span telemetry.TelemetrySpan) error {
				attributes := telemetry.SpanAttributes{"secret": unreadableValue{}}
				span.SetAttributes(attributes)
				span.AddEvent("unreadable-event", attributes)
				return nil
			}); err != nil {
				return err
			}

			// The span with the unreadable payload is still recorded once and succeeds; each failed call leaves
			// nothing or all of its payload.
			spans, err := fixture.GetSpans(ctx)
			if err != nil {
				return err
			}
			recorded := 0
			for _, span := range spans {
				switch span.Name {
				case "unreadable-recording":
					recorded++
					if err := deepStrictEqual(span.Status, status(telemetry.SpanStatusCodeOK), "status"); err != nil {
						return err
					}
					for _, event := range span.Events {
						if _, secret := event.Attributes["secret"]; event.Name != "unreadable-event" || !secret || len(event.Attributes) != 1 {
							return fmt.Errorf("events: expected none or the whole unreadable event, got %v", event.Name)
						}
					}
				case "unreadable-options":
				default:
					return fmt.Errorf("unexpected span %s", span.Name)
				}
			}
			return strictEqual(recorded, 1, "unreadable-recording spans")
		}),
	}
}
