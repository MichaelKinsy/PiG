package telemetry_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// packages/telemetry/src/memory.ts: ids count from 1 (nextSpanId: 1, :195) in start order; setAttributes (:152, mergeAttributes) overwrites
// named keys and keeps the rest; getSpans (:214) hands out copies, so a caller mutating a snapshot changes nothing recorded; a span started
// on a settled parent is not recorded (:129 `parent?.settled` runs the callback on the noop context).
// mutation-checked: span id offset, SetAttributes dropping its merge, GetSpans sharing the attribute map, and recording a child of a settled
// parent each fail it.
func TestInMemoryRecorderIdsAttributesSnapshotsAndSettledParents(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	var parent telemetry.TelemetrySpan
	err := recorder.StartSpan(telemetry.SpanOptions{Name: "parent", Attributes: telemetry.SpanAttributes{telemetry.Attr("keep", "a"), telemetry.Attr("over", "old")}}, func(span telemetry.TelemetrySpan) error {
		parent = span
		span.SetAttributes(telemetry.SpanAttributes{telemetry.Attr("over", "new"), telemetry.Attr("added", int64(7))})
		return span.StartSpan(telemetry.SpanOptions{Name: "child"}, func(telemetry.TelemetrySpan) error { return nil })
	})
	if err != nil {
		t.Fatal(err)
	}

	spans := recorder.GetSpans()
	if len(spans) != 2 || spans[0].ID != 1 || spans[1].ID != 2 || spans[1].ParentID == nil || *spans[1].ParentID != 1 {
		t.Fatalf("spans = %+v, want ids 1 and 2 with the child's parent 1", spans)
	}
	if want := (telemetry.SpanAttributes{telemetry.Attr("keep", "a"), telemetry.Attr("over", "new"), telemetry.Attr("added", int64(7))}); !reflect.DeepEqual(spans[0].Attributes, want) {
		t.Fatalf("attributes = %v, want %v", spans[0].Attributes, want)
	}

	spans[0].Attributes.Set("keep", "mutated")
	if got := recorder.GetSpans()[0].Attributes.Value("keep"); got != "a" {
		t.Fatalf("a caller's change to a snapshot reached the recorder: keep = %v", got)
	}

	called := false
	if err := parent.StartSpan(telemetry.SpanOptions{Name: "late"}, func(telemetry.TelemetrySpan) error { called = true; return nil }); err != nil || !called {
		t.Fatalf("late child: err = %v, called = %v; want the callback to run", err, called)
	}
	if n := len(recorder.GetSpans()); n != 2 {
		t.Fatalf("a child of a settled parent was recorded: %d spans, want 2", n)
	}
}
