package telemetry

import (
	"math"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsclock"
)

// Ports packages/telemetry/src/memory.ts `telemetryNow` and `microseconds` (Pi main 378d45a0). The clock of span times:
// wall-clock milliseconds at microsecond resolution, from the monotonic high-resolution clock anchored to the wall clock.

// telemetryToleranceMs is how far the anchored clock may part from Date.now() before it is anchored anew: more than
// Date.now()'s own truncation to whole milliseconds, less than any step worth following (a wall clock set, a sleep the
// monotonic clock did not count).
const telemetryToleranceMs = 10

var (
	anchorMu sync.Mutex
	// anchor is the wall-clock time of performance.now()'s zero; performance.timeOrigin until the first step is followed.
	anchor = jsclock.TimeOrigin()
)

// TelemetryNow is now as milliseconds since the epoch, rounded to the microsecond: PerformanceNow (monotonic,
// sub-millisecond) plus an anchor to the wall clock. While the two agree within 10 ms, times in one process keep their
// order and spacing to the microsecond; past that the clock follows DateNow again, at whole milliseconds. Every adapter of
// this package and Chord's SQLite store stamp span starts with it, so their times agree.
func TelemetryNow() float64 {
	clock := jsclock.PerformanceNow()
	wall := jsclock.DateNow()
	anchorMu.Lock()
	if math.Abs(anchor+clock-wall) > telemetryToleranceMs {
		anchor = wall - clock
	}
	now := anchor + clock
	anchorMu.Unlock()
	return Microseconds(now)
}

// Microseconds is ms rounded to the microsecond, as span times are (Math.round(ms * 1000) / 1000).
func Microseconds(ms float64) float64 { return math.Floor(ms*1000+0.5) / 1000 }
