// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build !race

package screenwriter

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/screenwriter/gridterm"
)

// A frame is translated without an allocation once the rows have their buffers: the terminal's write path allocates for a frame what
// it did without the writer.
func TestTranslateDoesNotAllocate(t *testing.T) {
	for name, stream := range testStreams(t) {
		writes := gridterm.SplitWrites(stream)
		w := New(Options{Size: func() (int, int) { return 160, 48 }, Settle: -1})
		replay := func() {
			for _, write := range writes {
				w.Translate(write)
			}
			w.Settled()
		}
		// The rows trade buffers as they are written: a few replays until each has grown to the longest line.
		for range 4 {
			replay()
		}
		if allocs := testing.AllocsPerRun(5, replay); allocs != 0 {
			t.Errorf("%s: %v allocations for %d writes, want 0", name, allocs, len(writes))
		}
	}
}

// The settle timer is one timer for the writer's life: arming it again allocates nothing.
func TestSettleTimerDoesNotAllocate(t *testing.T) {
	w := New(Options{Size: func() (int, int) { return 20, 5 }, Settle: 1 << 40, Lock: func() {}, Unlock: func() {}, Emit: func(string) {}})
	for _, write := range gridterm.SplitWrites(started) {
		w.Translate(write)
	}
	frames := [2]string{frame(line(2, "hello")), frame(line(2, "hello world"))}
	step := 0
	run := func() {
		w.Translate(frames[step%2])
		step++
	}
	run()
	run()
	if !w.armed {
		t.Fatal("the settle timer is not waiting after a row was written in part")
	}
	if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
		t.Errorf("%v allocations per frame with the settle timer waiting, want 0", allocs)
	}
	w.timer.Stop()
}

// BenchmarkTranslate is the writer's whole cost for a recorded step of the benchmark: every write of it read, compared and translated.
func BenchmarkTranslate(b *testing.B) {
	for name, stream := range testStreams(b) {
		writes := gridterm.SplitWrites(stream)
		b.Run(name, func(b *testing.B) {
			w := New(Options{Size: func() (int, int) { return 160, 48 }, Settle: -1})
			b.SetBytes(int64(len(stream)))
			b.ReportAllocs()
			for b.Loop() {
				for _, write := range writes {
					w.Translate(write)
				}
				w.Settled()
			}
			b.ReportMetric(float64(len(writes)), "writes/op")
		})
	}
}
