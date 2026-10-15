//go:build !race

package json

import (
	"fmt"
	"strings"
	"testing"
)

// budgetPayload mirrors Pi-Bolt bench/fake_model_stress.py write:N: TypeScript lines with one `=>` and one newline each.
func budgetPayload(size int) string {
	var b strings.Builder
	for n := 0; b.Len() < size; n++ {
		if n > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }", n, n%97, n)
	}
	return b.String()[:size]
}

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

// The string escaper skips runs that need no escape eight bytes at a time and copies them whole: on a 200 KB source text it is at least
// twice as fast as the byte-at-a-time encoder it equals (appendStringBytewise; about 3-4x on an M4 Pro, ~140 µs byte by byte).
func TestAppendString200KBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("CPU budget")
	}
	payload := budgetPayload(200 << 10)
	buf := make([]byte, 0, 2*len(payload))
	scan := fastestNs(func() { buf = appendString(buf[:0], payload, escapeLineSeparators) })
	bytewise := fastestNs(func() { buf = appendStringBytewise(buf[:0], payload, escapeLineSeparators) })
	t.Logf("appendString: %d ns/op, byte at a time: %d ns/op", scan, bytewise)
	if 2*scan > bytewise {
		t.Errorf("appendString of a 200 KB text takes %d ns, the byte-at-a-time encoder %d ns: want at least 2x faster", scan, bytewise)
	}
}

// One large value does not stay pinned in the encode-state pool: a buffer above 64 KiB is dropped on Put (golang/go#23199).
func TestEncodeStatePoolDropsLargeBuffers(t *testing.T) {
	payload := strings.Repeat("x", 1<<20)
	for _, encode := range []func(any) ([]byte, error){Marshal, MarshalJS} {
		if _, err := encode(payload); err != nil {
			t.Fatal(err)
		}
		e := newEncodeState()
		capacity := cap(e.AvailableBuffer())
		encodeStatePool.Put(e)
		if capacity > 64<<10 {
			t.Fatalf("the pooled encode state keeps a %d-byte buffer after a 1 MiB value, want at most 64 KiB", capacity)
		}
	}
}
