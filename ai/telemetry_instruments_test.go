package ai

import (
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// releasableMetrics is an adapter whose instruments do not reference it, so its cache entry can be released with it.
type releasableMetrics struct {
	histograms, counters int
	_                    [64]byte
}

func (m *releasableMetrics) Counter(name string, options telemetry.InstrumentOptions) telemetry.TelemetryCounter {
	m.counters++
	return telemetry.NoopTelemetryMetrics.Counter(name, options)
}

func (m *releasableMetrics) UpDownCounter(name string, options telemetry.InstrumentOptions) telemetry.TelemetryUpDownCounter {
	return telemetry.NoopTelemetryMetrics.UpDownCounter(name, options)
}

func (m *releasableMetrics) Histogram(name string, options telemetry.HistogramOptions) telemetry.TelemetryHistogram {
	m.histograms++
	return telemetry.NoopTelemetryMetrics.Histogram(name, options)
}

func aiInstrumentCacheLen() int {
	aiInstruments.Lock()
	defer aiInstruments.Unlock()
	return len(aiInstruments.weak)
}

// Pi's instrumentCache is a WeakMap<TelemetryMetrics, instruments> (telemetry.ts:80): instruments are created once per
// live metrics object, and a dropped object's entry goes away with it.
func TestAIInstrumentCacheIsCreatedOncePerLiveAdapterAndReleasedWithIt(t *testing.T) {
	baseline := aiInstrumentCacheLen()
	b := &releasableMetrics{}
	other := aiInstrumentsFor(b)
	dropped := func() weak.Pointer[releasableMetrics] {
		a := &releasableMetrics{}
		first, again := aiInstrumentsFor(a), aiInstrumentsFor(a)
		if first != again || first == other {
			t.Fatalf("instruments: first %p again %p other %p", first, again, other)
		}
		for name, adapter := range map[string]*releasableMetrics{"a": a, "b": b} {
			if adapter.histograms != 2 || adapter.counters != 2 {
				t.Fatalf("adapter %s created %d histograms and %d counters, want 2 and 2", name, adapter.histograms, adapter.counters)
			}
		}
		if got := aiInstrumentCacheLen(); got != baseline+2 {
			t.Fatalf("cache entries = %d, want %d", got, baseline+2)
		}
		return weak.Make(a)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for dropped.Value() != nil || aiInstrumentCacheLen() != baseline+1 {
		if time.Now().After(deadline) {
			t.Fatalf("dropped adapter alive = %v, cache entries = %d, want released and %d", dropped.Value() != nil, aiInstrumentCacheLen(), baseline+1)
		}
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	if aiInstrumentsFor(b) != other || b.histograms != 2 || b.counters != 2 {
		t.Fatal("the live adapter's instruments were recreated")
	}
	runtime.KeepAlive(b)
}
