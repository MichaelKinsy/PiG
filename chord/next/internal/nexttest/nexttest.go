// Package nexttest ports the helpers of packages/chord/test/next/support.ts that the Chord v2 foundation tests share:
// counting contexts, metered contexts and watch collectors. Later lanes add the host, link and store helpers here.
//
// WeakRef/FinalizationRegistry collection helpers (expectCollected, settledHeap) have no Go form: Go has no weak
// references a test can wait on beyond runtime/weak, so a test asserting collection uses weak.Pointer with runtime.GC
// directly (see ExpectCollected).
package nexttest

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Counts are the counts of the `chord.data.*` counters.
type Counts struct{ Walks, Validations, Compilations int }

// Counted runs work with a counting context and returns the counters it added.
func Counted(t testing.TB, work func(ctx context.Context)) Counts {
	t.Helper()
	recorder := telemetry.NewInMemoryTelemetryContext()
	err := recorder.StartSpan(telemetry.SpanOptions{Name: "test"}, func(span telemetry.TelemetrySpan) error {
		work(chordctx.WithTelemetry(chordctx.Background(), span))
		return nil
	})
	if err != nil {
		t.Fatalf("Counted: %v", err)
	}
	var counts Counts
	for _, measurement := range recorder.GetMetrics() {
		switch strings.TrimPrefix(measurement.Name, "chord.data.") {
		case "walks":
			counts.Walks += int(measurement.Value)
		case "validations":
			counts.Validations += int(measurement.Value)
		case "compilations":
			counts.Compilations += int(measurement.Value)
		}
	}
	return counts
}

// Meter is a telemetry adapter, a context counting through it, and the counters recorded so far, by name.
type Meter struct {
	Telemetry *telemetry.InMemoryTelemetryContext
	Context   context.Context
}

// Metered returns a Meter; a settled span still records through its adapter's metrics.
func Metered(t testing.TB) *Meter {
	t.Helper()
	recorder := telemetry.NewInMemoryTelemetryContext()
	var settled telemetry.TelemetrySpan
	if err := recorder.StartSpan(telemetry.SpanOptions{Name: "test"}, func(span telemetry.TelemetrySpan) error {
		settled = span
		return nil
	}); err != nil {
		t.Fatalf("Metered: %v", err)
	}
	return &Meter{Telemetry: recorder, Context: chordctx.WithTelemetry(chordctx.Background(), settled)}
}

// Counts is the sum of every measurement recorded so far, by instrument name.
func (meter *Meter) Counts() map[string]float64 {
	counts := map[string]float64{}
	for _, measurement := range meter.Telemetry.GetMetrics() {
		counts[measurement.Name] += measurement.Value
	}
	return counts
}

// Delivered waits until queued watch frames have been delivered: one scheduler turn after the timers queued now.
func Delivered() { time.Sleep(time.Millisecond) }

// ExpectCollected expects every referent to be collected once nothing else holds it.
func ExpectCollected[T any](t testing.TB, refs ...weak.Pointer[T]) {
	t.Helper()
	for round := 0; round < 10; round++ {
		runtime.GC()
		held := false
		for _, ref := range refs {
			held = held || ref.Value() != nil
		}
		if !held {
			return
		}
		time.Sleep(time.Millisecond)
	}
	for i, ref := range refs {
		if ref.Value() != nil {
			t.Errorf("referent %d was not collected", i)
		}
	}
}

// Frame is one value frame of a watch: the value and the ops that produced it.
type Frame[T any] struct {
	Value T
	Ops   []delta.Op
}
