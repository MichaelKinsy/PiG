package telemetry

import (
	"testing"
)

// packages/telemetry/src/memory.ts:195-196,109,98 a recorder numbers spans from 1 in start order (nextSpanId) and numbers settlements from 1 in
// settle order (nextEndSequence), so a child settles before its parent and its parentId is the parent's id.
func TestInMemoryTelemetryContextNumbersSpansAndSettlements(t *testing.T) {
	recorder := NewInMemoryTelemetryContext()
	err := recorder.StartSpan(SpanOptions{Name: "root"}, func(root TelemetrySpan) error {
		if err := root.StartSpan(SpanOptions{Name: "child"}, func(TelemetrySpan) error { return nil }); err != nil {
			return err
		}
		return root.StartSpan(SpanOptions{Name: "sibling"}, func(TelemetrySpan) error { return nil })
	})
	if err != nil {
		t.Fatal(err)
	}
	spans := recorder.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("recorded %d spans, want 3", len(spans))
	}
	wantID := []int{1, 2, 3}
	wantEnd := []int{3, 1, 2}
	for i, span := range spans {
		if span.ID != wantID[i] || span.EndSequence == nil || *span.EndSequence != wantEnd[i] {
			t.Errorf("span %q: id %d endSequence %v, want id %d endSequence %d", span.Name, span.ID, span.EndSequence, wantID[i], wantEnd[i])
		}
	}
	if spans[0].ParentID != nil || spans[1].ParentID == nil || *spans[1].ParentID != 1 || spans[2].ParentID == nil || *spans[2].ParentID != 1 {
		t.Errorf("parent ids = %v %v %v, want nil 1 1", spans[0].ParentID, spans[1].ParentID, spans[2].ParentID)
	}
}

// memory.ts:144 addEvent records `copyAttributes(attributes)` and :206-214 getSpans copies again, so neither the caller's attribute map nor a
// returned snapshot is shared with the recorder; ids and sequence numbers are numbers, not references.
func TestInMemoryTelemetryContextKeepsEventsAndSnapshotsDetached(t *testing.T) {
	recorder := NewInMemoryTelemetryContext()
	attributes := SpanAttributes{Attr("list", []string{"a", "b"}), Attr("n", 1)}
	err := recorder.StartSpan(SpanOptions{Name: "outer"}, func(outer TelemetrySpan) error {
		return outer.StartSpan(SpanOptions{Name: "inner"}, func(inner TelemetrySpan) error {
			inner.AddEvent("event", attributes)
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	attributes.Set("n", 2)
	attributes.Value("list").([]string)[0] = "changed"
	first := recorder.GetSpans()
	event := first[1].Events[0]
	if event.Attributes.Value("n") != 1 || event.Attributes.Value("list").([]string)[0] != "a" {
		t.Fatalf("the recorded event shares the caller's attributes: %v", event.Attributes)
	}
	*first[1].ParentID = 99
	*first[1].EndSequence = 99
	second := recorder.GetSpans()
	if *second[1].ParentID != 1 || *second[1].EndSequence != 1 {
		t.Fatalf("a snapshot's parent id / end sequence is shared with the recorder: parent %d end %d, want 1 and 1", *second[1].ParentID, *second[1].EndSequence)
	}
}
