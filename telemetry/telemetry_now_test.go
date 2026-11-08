package telemetry_test

import (
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports the `telemetryNow` describe of packages/telemetry/test/store.test.ts (Pi main 378d45a0).

func TestTelemetryNowCountsSubMillisecondStepsOfTheMonotonicClockWhileTheWallClockStaysInItsMillisecond(t *testing.T) {
	_, restoreWall := jsclock.MockDateNow(50_000)
	defer restoreWall()
	clock, restoreClock := jsclock.MockPerformanceNow(1000.25)
	defer restoreClock()
	first := telemetry.TelemetryNow()
	clock.Set(1000.2504)
	if d := telemetry.TelemetryNow() - first; math.Abs(d) > 5e-7 {
		t.Errorf("after 0.0004 ms = %v, want 0", d)
	}
	clock.Set(1000.7512)
	if d := telemetry.TelemetryNow() - first; math.Abs(d-0.501) > 5e-7 {
		t.Errorf("after 0.5012 ms = %v, want 0.501", d)
	}
	now := telemetry.TelemetryNow() * 1000
	if now != math.Trunc(now) && math.Abs(now-math.Round(now)) > 1e-6 {
		t.Errorf("not a whole number of microseconds: %v", now)
	}
}

func TestTelemetryNowFollowsAStepOfTheWallClockPast10ms(t *testing.T) {
	wall, restoreWall := jsclock.MockDateNow(80_000)
	defer restoreWall()
	clock, restoreClock := jsclock.MockPerformanceNow(1000)
	defer restoreClock()
	// Far from the anchor any earlier call left: anchored anew, at the wall clock's millisecond.
	if got := telemetry.TelemetryNow(); got != 80_000 {
		t.Fatalf("first = %v, want 80000", got)
	}
	clock.Set(1003.5)
	wall.Set(80_005)
	// Within 10 ms the anchored clock keeps its own spacing.
	if got := telemetry.TelemetryNow(); got != 80_003.5 {
		t.Errorf("within tolerance = %v, want 80003.5", got)
	}
	clock.Set(1004)
	wall.Set(3_650_004)
	if got := telemetry.TelemetryNow(); got != 3_650_004 {
		t.Errorf("after a step = %v, want 3650004", got)
	}
	clock.Set(1004.125)
	if got := telemetry.TelemetryNow(); got != 3_650_004.125 {
		t.Errorf("after the step = %v, want 3650004.125", got)
	}
}
