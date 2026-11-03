package telemetrysqlite

import (
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// packages/chord/src/telemetry-sqlite/store.ts findMetrics: the stored intervals of one metric name, in interval order, narrowed by the inclusive
// StartMin and StartMax bounds on the interval start and by attribute equalities.
func TestSqliteTelemetryFindMetricsFiltersByIntervalBoundsAndAttributesInIntervalOrder(t *testing.T) {
	clock := mockDate(t, 20_001)
	f := newFixture(t, SqliteTelemetryOptions{})
	counter := f.telemetry.Counter("requests", telemetry.InstrumentOptions{})
	counter.Add(1, telemetry.SpanAttributes{telemetry.Attr("route", "a")})
	clock.Set(30_001)
	counter.Add(2, telemetry.SpanAttributes{telemetry.Attr("route", "b")})
	clock.Set(40_001)
	counter.Add(4, telemetry.SpanAttributes{telemetry.Attr("route", "a")})
	f.telemetry.Counter("other", telemetry.InstrumentOptions{}).Add(8, nil)
	check(t, f.telemetry.Close())

	sums := func(query telemetry.MetricQuery) [][2]float64 {
		t.Helper()
		metrics, err := f.telemetry.FindMetrics(query)
		check(t, err)
		var got [][2]float64
		for _, metric := range metrics {
			got = append(got, [2]float64{metric.IntervalMs, metric.Sum})
		}
		return got
	}
	same := func(got, want [][2]float64) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	if got, want := sums(telemetry.MetricQuery{Name: "requests"}), [][2]float64{{20_000, 1}, {30_000, 2}, {40_000, 4}}; !same(got, want) {
		t.Fatalf("all intervals = %v, want %v in interval order", got, want)
	}
	from, until := 30_000.0, 30_000.0
	if got, want := sums(telemetry.MetricQuery{Name: "requests", StartMin: &from}), [][2]float64{{30_000, 2}, {40_000, 4}}; !same(got, want) {
		t.Fatalf("StartMin 30000 = %v, want %v (inclusive)", got, want)
	}
	if got, want := sums(telemetry.MetricQuery{Name: "requests", StartMax: &until}), [][2]float64{{20_000, 1}, {30_000, 2}}; !same(got, want) {
		t.Fatalf("StartMax 30000 = %v, want %v (inclusive)", got, want)
	}
	if got, want := sums(telemetry.MetricQuery{Name: "requests", Attributes: map[string]telemetry.AttributeValue{"route": "a"}}), [][2]float64{{20_000, 1}, {40_000, 4}}; !same(got, want) {
		t.Fatalf("route a = %v, want %v", got, want)
	}
	if got := sums(telemetry.MetricQuery{Name: "missing"}); len(got) != 0 {
		t.Fatalf("an unknown name = %v, want nothing", got)
	}
}
