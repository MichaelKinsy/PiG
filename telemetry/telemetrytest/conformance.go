package telemetrytest

// Ports packages/telemetry/src/testing/conformance.ts.
//
// Go mapping: StartSpan blocks until its callback returns, so a callback's result is captured by the closure and a
// rejected Promise is the callback's returned error, compared by identity as strictEqual does. A callback that
// awaits before rejecting returns the error it receives from another goroutine; a synchronous throw also has the
// Go form of a panic, which the adapter records and re-raises with the same value. A Promise that the callback does
// not await is a goroutine the callback joins. Upstream's unreadable error (a Proxy whose traps throw) is an error
// whose Error method panics. The cases that read a Proxy-trapped SpanOptions, SpanAttributes or SpanStatus are not
// ported: reading those Go values cannot fail, so there is no failure for an adapter to suppress. A rejection with
// undefined has no Go form, because a nil error is success.

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/MichaelKinsy/PiG/telemetry"
)

type conformanceTest func(context.Context, TelemetryAdapterFixture) error

func createCase(factory TelemetryAdapterFixtureFactory, group, name string, test conformanceTest) TelemetryAdapterConformanceCase {
	return TelemetryAdapterConformanceCase{
		Group: group,
		Name:  name,
		Run: func(ctx context.Context) (err error) {
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
		},
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

func status(code telemetry.SpanStatusCode) telemetry.SpanStatus {
	return telemetry.SpanStatus{Status: code}
}

func errorStatus(name, message string) telemetry.SpanStatus {
	return telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: name, Message: message}}
}

// CreateTelemetryAdapterConformance creates runner-independent cases for the callback telemetry adapter contract.
func CreateTelemetryAdapterConformance(factory TelemetryAdapterFixtureFactory) []TelemetryAdapterConformanceCase {
	return []TelemetryAdapterConformanceCase{
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
	}
}
