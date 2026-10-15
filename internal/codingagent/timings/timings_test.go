package timings

// pi: packages/coding-agent/src/core/timings.ts

import (
	"strings"
	"testing"
)

// setup enables the recorder against a fake clock and a captured stderr, then restores the package state.
func setup(t *testing.T, on bool) (clock *int64, out *strings.Builder) {
	t.Helper()
	mu.Lock()
	prevEnabled, prevErr, prevNow, prevOrder, prevNS := enabled, stderr, now, order, namespaces
	enabled, order, namespaces = on, nil, map[Label]*namespace{}
	var tick int64 = 1000
	now = func() int64 { return tick }
	out = &strings.Builder{}
	stderr = out
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		enabled, stderr, now, order, namespaces = prevEnabled, prevErr, prevNow, prevOrder, prevNS
		mu.Unlock()
	})
	return &tick, out
}

// timings.ts: time() records the milliseconds since the previous mark, resetTimings() restarts the clock, printTimings() writes
// "\n--- Startup Timings: <ns> ---", one "  label: Nms" row per mark, "  TOTAL: Nms" and title.length+8 dashes, each group followed by a blank line.
func TestPrintTimingsWritesPisReport(t *testing.T) {
	clock, out := setup(t, true)
	ResetTimings()
	*clock += 12
	Time("parseArgs")
	*clock += 30
	Time("createRuntime")
	PrintTimings()
	want := "\n--- Startup Timings: main ---\n  parseArgs: 12ms\n  createRuntime: 30ms\n  TOTAL: 42ms\n" + strings.Repeat("-", len("Startup Timings: main")+8) + "\n\n"
	if out.String() != want {
		t.Fatalf("report =\n%q\nwant\n%q", out.String(), want)
	}
}

// timings.ts: a mark in a namespace nobody reset starts that namespace's clock at the mark (its first row is 0ms); namespaces print in
// first-use order; resetting an existing one keeps its place and clears its rows; a negative row is not printed or counted.
func TestNamespacesPrintInFirstUseOrderAndResetKeepsThePlace(t *testing.T) {
	clock, out := setup(t, true)
	*clock += 5
	Time("first", Extensions)
	*clock += 7
	Time("second", Extensions)
	ResetTimings(Main)
	*clock += 3
	Time("main mark")
	ResetTimings(Extensions)
	*clock += 4
	Time("after reset", Extensions)
	*clock -= 10
	Time("clock went back", Extensions)
	PrintTimings()
	want := "\n--- Startup Timings: extensions ---\n  after reset: 4ms\n  TOTAL: 4ms\n" + strings.Repeat("-", len("Startup Timings: extensions")+8) + "\n\n" +
		"\n--- Startup Timings: main ---\n  main mark: 3ms\n  TOTAL: 3ms\n" + strings.Repeat("-", len("Startup Timings: main")+8) + "\n\n"
	if out.String() != want {
		t.Fatalf("report =\n%q\nwant\n%q", out.String(), want)
	}
}

// timings.ts: ENABLED is PI_TIMING === "1"; disabled, every call is a no-op and nothing is printed; a namespace without printable rows prints nothing.
func TestDisabledRecorderPrintsNothingAndAnEmptyGroupIsSkipped(t *testing.T) {
	_, out := setup(t, false)
	ResetTimings()
	Time("x")
	PrintTimings()
	if out.Len() != 0 || len(order) != 0 {
		t.Fatalf("a disabled recorder recorded or printed: %q %v", out.String(), order)
	}
	setup(t, true)
	ResetTimings()
	out2 := &strings.Builder{}
	stderr = out2
	PrintTimings()
	if out2.Len() != 0 {
		t.Fatalf("a namespace with no marks printed %q", out2.String())
	}
}

// timings.ts: time() in a namespace nobody reset resets it first, so that namespace's first row is 0ms and the next is the gap since it;
// printTimingGroup keeps rows with ms >= 0, so the 0ms row prints.
func TestFirstMarkOfAnUnresetNamespaceIsZero(t *testing.T) {
	clock, out := setup(t, true)
	*clock += 9
	Time("a", Extensions)
	*clock += 7
	Time("b", Extensions)
	rows := namespaces[Extensions].timings
	if len(rows) != 2 || rows[0].ms != 0 || rows[1].ms != 7 {
		t.Fatalf("rows = %+v, want 0ms then 7ms", rows)
	}
	PrintTimings()
	want := "\n--- Startup Timings: extensions ---\n  a: 0ms\n  b: 7ms\n  TOTAL: 7ms\n" + strings.Repeat("-", len("Startup Timings: extensions")+8) + "\n\n"
	if out.String() != want {
		t.Fatalf("report =\n%q\nwant\n%q", out.String(), want)
	}
}
