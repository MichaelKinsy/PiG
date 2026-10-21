package jsclock

import "testing"

func TestMockedClocksReturnTheirValueUntilRestored(t *testing.T) {
	date, restoreDate := MockDateNow(1000)
	perf, restorePerf := MockPerformanceNow(100)
	if DateNow() != 1000 || PerformanceNow() != 100 {
		t.Fatalf("mocked clocks = %v, %v", DateNow(), PerformanceNow())
	}
	date.Set(2000)
	perf.Set(110.5)
	if DateNow() != 2000 || PerformanceNow() != 110.5 {
		t.Fatalf("set clocks = %v, %v", DateNow(), PerformanceNow())
	}
	restoreDate()
	restorePerf()
	if DateNow() == 2000 || PerformanceNow() == 110.5 {
		t.Fatal("the real clocks were not restored")
	}
}
