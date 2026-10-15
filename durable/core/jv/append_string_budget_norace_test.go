//go:build !race

package jv

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// fastestNs is the lowest ns/op of each run over rounds that alternate between them, so one descheduled run or a slow
// stretch of a shared runner does not decide, and both sides see the same machine state.
func fastestNs(runs ...func()) []int64 {
	best := make([]int64, len(runs))
	for i := range best {
		best[i] = 1 << 62
	}
	for range 7 {
		for i, run := range runs {
			if ns := testing.Benchmark(func(b *testing.B) {
				for b.Loop() {
					run()
				}
			}).NsPerOp(); ns < best[i] {
				best[i] = ns
			}
		}
	}
	return best
}

// writeText is a 200 KB source text, the content of a Pi-Bolt write:200 call.
func writeText() string {
	var b strings.Builder
	for n := 0; b.Len() < 200<<10; n++ {
		fmt.Fprintf(&b, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n", n, n%97, n)
	}
	return b.String()
}

// AppendString skips runs that need no escape eight bytes at a time and copies them whole: on a 200 KB source text (Pi-Bolt write:200) it is
// at least 2.25 times as fast as the byte-at-a-time loop it equals (owner-approved bar 2026-10-15: pinned cores measure 2.68x on AMD
// EPYC-Milan, 4.0x on Intel Xeon and 5.3x on arm64).
func TestAppendString200KBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("CPU budget")
	}
	// A wall-clock ratio: it runs in the serial startup-proxies job (automation/perf/startup-proxies.json requires it there),
	// not beside the parallel test packages, whose memory-bandwidth contention halves the ratio on a 4-CPU runner.
	if os.Getenv("PIG_SERIAL_TIMING") == "" {
		t.Skip("wall-clock ratio: run by make startup-proxies (PIG_SERIAL_TIMING=1)")
	}
	payload := writeText()
	buf := make([]byte, 0, 2*len(payload))
	ns := fastestNs(func() { buf = AppendString(buf[:0], payload) }, func() { buf = bytewiseAppendString(buf[:0], payload) })
	scan, bytewise := ns[0], ns[1]
	t.Logf("AppendString: %d ns/op, byte at a time: %d ns/op", scan, bytewise)
	testenv.TimingGate(t, 9*scan > 4*bytewise, "AppendString of a 200 KB text takes %d ns, the byte-at-a-time loop %d ns: want at least 2.25x faster", scan, bytewise)
}

func BenchmarkAppendString200K(b *testing.B) {
	payload := writeText()
	buf := make([]byte, 0, 2*len(payload))
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		buf = AppendString(buf[:0], payload)
	}
}
