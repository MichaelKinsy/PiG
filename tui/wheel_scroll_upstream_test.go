package tui

import (
	"slices"
	"testing"
)

func wheelScroll(accelerator *WheelScrollAccelerator, times []float64, direction ...int) []int {
	dir := 1
	if len(direction) > 0 {
		dir = direction[0]
	}
	out := make([]int, len(times))
	for i, now := range times {
		out[i] = accelerator.Next(dir, now)
	}
	return out
}

// .upstream/v0.99.2/packages/tui/test/wheel-scroll.test.ts:9 (#9758: fullscreen wheel scrolling was one line per notch on terminals that do not accelerate wheels).
func TestUpstreamWheelScrollAccelerator(t *testing.T) {
	auto := WheelScrollLines{Auto: true}
	expect := func(t *testing.T, got []int, want ...int) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	// wheel-scroll.test.ts:11.
	t.Run("uses fixed line counts regardless of timing", func(t *testing.T) {
		accelerator := NewWheelScrollAccelerator(WheelScrollLines{Lines: 3}, true)
		expect(t, wheelScroll(accelerator, []float64{0, 10, 20, 1000}), 3, 3, 3, 3)
		accelerator.SetLines(WheelScrollLines{Lines: 0.5})
		if got := accelerator.Next(1, 2000); got != 1 {
			t.Fatalf("got %d, want 1", got)
		}
	})
	// wheel-scroll.test.ts:18.
	t.Run("keeps one line per event in auto mode when the terminal already accelerates", func(t *testing.T) {
		accelerator := NewWheelScrollAccelerator(auto, false)
		expect(t, wheelScroll(accelerator, []float64{0, 10, 20, 30}), 1, 1, 1, 1)
	})
	// wheel-scroll.test.ts:23.
	t.Run("scales auto mode with wheel velocity", func(t *testing.T) {
		accelerator := NewWheelScrollAccelerator(auto, true)
		expect(t, wheelScroll(accelerator, []float64{0, 150, 300, 450}), 1, 1, 1, 1)
		expect(t, wheelScroll(accelerator, []float64{1000, 1050, 1100, 1150}), 1, 2, 2, 2)
		expect(t, wheelScroll(accelerator, []float64{2000, 2020, 2040, 2060}), 1, 5, 5, 5)
		expect(t, wheelScroll(accelerator, []float64{3000, 3010, 3020, 3030}), 1, 6, 6, 6)
	})
	// wheel-scroll.test.ts:31.
	t.Run("does not accelerate bursts of events for a single notch", func(t *testing.T) {
		accelerator := NewWheelScrollAccelerator(auto, true)
		expect(t, wheelScroll(accelerator, []float64{0, 3, 6, 9}), 1, 1, 1, 1)
	})
	// wheel-scroll.test.ts:36.
	t.Run("resets acceleration on direction changes and pauses", func(t *testing.T) {
		accelerator := NewWheelScrollAccelerator(auto, true)
		expect(t, wheelScroll(accelerator, []float64{0, 20, 40}), 1, 5, 5)
		if got := accelerator.Next(-1, 60); got != 1 {
			t.Fatalf("got %d, want 1", got)
		}
		expect(t, wheelScroll(accelerator, []float64{500, 520}), 1, 5)
	})
	// wheel-scroll.test.ts:43.
	t.Run("carries fractional lines between events", func(t *testing.T) {
		accelerator := NewWheelScrollAccelerator(auto, true)
		expect(t, wheelScroll(accelerator, []float64{0, 40, 80, 120, 160}), 1, 2, 3, 2, 3)
	})
}
