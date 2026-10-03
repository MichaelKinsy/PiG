package telemetry_test

// Ports packages/telemetry/test/conformance.test.ts.

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry/telemetrytest"
)

// TestConformanceUpstream ports packages/telemetry/test/conformance.test.ts. Each subtest names one upstream case.
func TestConformanceUpstream(t *testing.T) {
	conformance := telemetrytest.CreateTelemetryAdapterConformance(func(context.Context) (telemetrytest.TelemetryAdapterFixture, error) {
		recorder := &telemetry.InMemoryTelemetryContext{}
		return telemetrytest.TelemetryAdapterFixture{
			Context: recorder,
			GetSpans: func(context.Context) ([]telemetry.RecordedTelemetrySpan, error) {
				return recorder.GetSpans(), nil
			},
		}, nil
	})
	if len(conformance) == 0 {
		t.Fatal("conformance suite is empty")
	}
	for _, testCase := range conformance {
		t.Run("InMemoryTelemetryContext conformance › "+testCase.Group+" › "+testCase.Name, func(t *testing.T) {
			// upstream: packages/telemetry/test/conformance.test.ts:18
			if err := testCase.Run(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("InMemoryTelemetryContext conformance › returns detached snapshots without exposing mutable recording state", func(t *testing.T) {
		// upstream: packages/telemetry/test/conformance.test.ts:23
		recorder := &telemetry.InMemoryTelemetryContext{}
		var openSettled *bool
		var openEndSequence *int
		if err := recorder.StartSpan(telemetry.SpanOptions{Name: "snapshot", Attributes: telemetry.SpanAttributes{"tags": []string{"initial"}}}, func(span telemetry.TelemetrySpan) error {
			span.AddEvent("event", telemetry.SpanAttributes{"value": 1})
			open := recorder.GetSpans()
			if len(open) > 0 {
				openSettled = &open[0].Settled
				openEndSequence = open[0].EndSequence
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}

		if openSettled == nil || *openSettled {
			t.Fatalf("open span settled = %v, want false", openSettled)
		}
		if openEndSequence != nil {
			t.Fatalf("open span endSequence = %d, want undefined", *openEndSequence)
		}
		first := recorder.GetSpans()[0]
		if !first.Settled || first.EndSequence == nil || *first.EndSequence != 1 {
			t.Fatalf("settled snapshot = %+v, want settled with endSequence 1", first)
		}
		// A Go slice aliases its backing array, so the element is mutated in place as well as replaced.
		first.Attributes["tags"].([]string)[0] = "mutated"
		first.Attributes["tags"] = []string{"mutated"}
		first.Events[0].Attributes["value"] = 2

		second := recorder.GetSpans()[0]
		if !reflect.DeepEqual(second.Attributes, telemetry.SpanAttributes{"tags": []string{"initial"}}) {
			t.Fatalf("attributes = %#v", second.Attributes)
		}
		if !reflect.DeepEqual(second.Events, []telemetry.RecordedTelemetryEvent{{Name: "event", Attributes: telemetry.SpanAttributes{"value": 1}}}) {
			t.Fatalf("events = %#v", second.Events)
		}
	})
}
