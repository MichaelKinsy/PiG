package telemetry_test

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// BenchmarkRecordSpan records one span with n start attributes, a SetAttributes call that overwrites half of them and
// adds one, and one event with n attributes.
func BenchmarkRecordSpan(b *testing.B) {
	for _, n := range []int{3, 8, 16} {
		b.Run(fmt.Sprintf("attributes=%d", n), func(b *testing.B) {
			start := telemetry.SpanAttributes{}
			update := telemetry.SpanAttributes{}
			for i := range n {
				start = append(start, telemetry.Attr(fmt.Sprintf("attribute.%d", i), i))
				if i%2 == 0 {
					update = append(update, telemetry.Attr(fmt.Sprintf("attribute.%d", i), i+1))
				}
			}
			update = append(update, telemetry.Attr("extra", "value"))
			recorder := telemetry.NewInMemoryTelemetryContext()
			b.ReportAllocs()
			for b.Loop() {
				_ = recorder.StartSpan(telemetry.SpanOptions{Name: "bench", Attributes: start}, func(span telemetry.TelemetrySpan) error {
					span.SetAttributes(update)
					span.AddEvent("event", start)
					return nil
				})
			}
		})
	}
}
