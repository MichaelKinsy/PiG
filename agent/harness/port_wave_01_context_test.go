package harness

import (
	"slices"
	"testing"
)

// upstream: packages/agent/test/harness/context.test.ts:11 is retained in TestGetTelemetryContextDefaultsToNoop without duplicate execution.
func TestPortWave01Context(t *testing.T) {
	t.Parallel()
	// upstream: packages/agent/test/harness/context.test.ts:16
	t.Run("carries telemetry as an ordinary context value", func(t *testing.T) {
		telemetry := &InMemoryTelemetryContext{}
		ctx := WithTelemetryContext(BackgroundContext(), telemetry)
		err := GetTelemetryContext(ctx).StartSpan(SpanOptions{Name: "parent"}, func(span TelemetrySpan) error {
			child := WithTelemetryContext(ctx, span)
			return GetTelemetryContext(child).StartSpan(SpanOptions{Name: "child"}, func(TelemetrySpan) error { return nil })
		})
		if err != nil {
			t.Fatal(err)
		}
		spans := telemetry.GetSpans()
		names := make([]string, len(spans))
		for i, span := range spans {
			names[i] = span.Name
		}
		if !slices.Equal(names, []string{"parent", "child"}) {
			t.Fatalf("span names = %v, want [parent child]", names)
		}
		if spans[1].ParentID == nil || *spans[1].ParentID != spans[0].ID {
			t.Fatalf("child parent = %v, want span %d", spans[1].ParentID, spans[0].ID)
		}
	})
}
