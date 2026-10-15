//go:build !race

package jv

import (
	"fmt"
	"strings"
	"testing"
)

// fastestNs is the lowest ns/op of run over a few benchmark runs, so one descheduled run on a loaded machine does not decide.
func fastestNs(run func()) int64 {
	best := int64(1 << 62)
	for range 5 {
		if ns := testing.Benchmark(func(b *testing.B) {
			for b.Loop() {
				run()
			}
		}).NsPerOp(); ns < best {
			best = ns
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
// at least three times as fast as the byte-at-a-time loop it equals.
func TestAppendString200KBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("CPU budget")
	}
	payload := writeText()
	buf := make([]byte, 0, 2*len(payload))
	scan := fastestNs(func() { buf = AppendString(buf[:0], payload) })
	bytewise := fastestNs(func() { buf = bytewiseAppendString(buf[:0], payload) })
	t.Logf("AppendString: %d ns/op, byte at a time: %d ns/op", scan, bytewise)
	if 3*scan > bytewise {
		t.Errorf("AppendString of a 200 KB text takes %d ns, the byte-at-a-time loop %d ns: want at least 3x faster", scan, bytewise)
	}
}

func BenchmarkAppendString200K(b *testing.B) {
	payload := writeText()
	buf := make([]byte, 0, 2*len(payload))
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		buf = AppendString(buf[:0], payload)
	}
}
