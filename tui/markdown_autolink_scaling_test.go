package tui

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The reported input is an unbroken 64 KiB paragraph, not a cached render.
// Pi 0.87.1 markdown.ts:303 lexes the whole paragraph; marked 18.0.11
// inlineText consumes ordinary text without probing every remaining suffix.
func TestMarkdownUnbrokenLinearScaling(t *testing.T) {
	var previousBytes uint64
	for _, size := range []int{16 << 10, 32 << 10, 64 << 10} {
		text := strings.Repeat("x", size)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		lines := NewMarkdown(text).Render(80)
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("%d bytes: %s, %d B allocated", size, elapsed, allocated)
		if got := strings.TrimRight(strings.Join(lines, ""), " "); got != text {
			t.Fatalf("render lost or changed unbroken text: got %d bytes, want %d", len(got), size)
		}
		if elapsed >= time.Second {
			t.Errorf("%d-byte cold render took %s, must finish under 1s", size, elapsed)
		}
		// Allocation growth is a deterministic scaling guard, rather than a
		// wall-clock ratio that would depend on other lanes' CPU scheduling.
		// Linear work doubles; the old suffix copies grew by approximately 4x.
		if previousBytes != 0 && allocated > previousBytes*5/2 {
			t.Errorf("doubling input grew allocations from %d to %d (>2.5x)", previousBytes, allocated)
		}
		previousBytes = allocated
	}
}

func BenchmarkMarkdownUnbroken(b *testing.B) {
	for _, size := range []int{16 << 10, 32 << 10, 64 << 10, 128 << 10} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			text := strings.Repeat("x", size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				NewMarkdown(text).Render(80)
			}
		})
	}
}
