package delta

import (
	"fmt"
	"strings"
	"testing"
)

// The cost of a streaming writer's appends: AppendText on a tracked text, adopted one commit at a time, and a slow
// consumer's PendingOps folding the appends of those commits. Run with -benchmem.

const appendDelta = "12345678"

var appendLadder = []int{10, 100, 1_000, 10_000}

// BenchmarkTrackerAppendTextPrepare prepares one append of 5 bytes to a text of the given length, then aborts it: AppendText against a plain Set of the longer string.
func BenchmarkTrackerAppendTextPrepare(b *testing.B) {
	for _, length := range []int{10, 100, 1_000, 10_000, 100_000} {
		text := strings.Repeat("x", length)
		for _, mode := range []string{"AppendText", "SetConcat"} {
			b.Run(fmt.Sprintf("%s/%d", mode, length), func(b *testing.B) {
				tracker := Track(JsonObjectOf("text", text))
				b.ReportAllocs()
				for b.Loop() {
					change := tracker.BeginChange()
					state := change.State()
					if mode == "AppendText" {
						if err := AppendText(state, "text", "delta"); err != nil {
							b.Fatal(err)
						}
					} else {
						_ = state.Set("text", text+"delta")
					}
					prepared, err := change.Prepare()
					if err != nil {
						b.Fatal(err)
					}
					prepared.Abort()
				}
			})
		}
	}
}

// streamAppends adopts n commits that each append 8 bytes to one text.
func streamAppends(tb testing.TB, n int, mode string) *Tracker {
	tracker := Track(JsonObjectOf("text", ""))
	for range n {
		change := tracker.BeginChange()
		state := change.State()
		if mode == "AppendText" {
			if err := AppendText(state, "text", appendDelta); err != nil {
				tb.Fatal(err)
			}
		} else {
			_ = state.Set("text", state.Get("text").(string)+appendDelta)
		}
		prepared, err := change.Prepare()
		if err != nil {
			tb.Fatal(err)
		}
		if err := tracker.Adopt(prepared); err != nil {
			tb.Fatal(err)
		}
	}
	return tracker
}

// setCounts adopts n commits that each set one number: the cost of a commit that does not grow a text, which the append stream pays too.
func setCounts(tb testing.TB, n int) {
	tracker := Track(JsonObjectOf("count", 0.0))
	for i := range n {
		change := tracker.BeginChange()
		_ = change.State().Set("count", float64(i+1))
		prepared, err := change.Prepare()
		if err != nil {
			tb.Fatal(err)
		}
		if err := tracker.Adopt(prepared); err != nil {
			tb.Fatal(err)
		}
	}
}

// BenchmarkTrackerSetCountStream is the cost the append stream cannot go under: n adopted commits that each set a number.
func BenchmarkTrackerSetCountStream(b *testing.B) {
	for _, n := range appendLadder {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				setCounts(b, n)
			}
		})
	}
}

// BenchmarkTrackerAppendTextStream is the cost of a whole stream of n adopted 8-byte appends.
func BenchmarkTrackerAppendTextStream(b *testing.B) {
	for _, mode := range []string{"AppendText", "SetConcat"} {
		for _, n := range appendLadder {
			b.Run(fmt.Sprintf("%s/%d", mode, n), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					streamAppends(b, n, mode)
				}
			})
		}
	}
}

// BenchmarkPendingOpsFoldAppends queues n single-op commits that each append 8 bytes to one path, then takes them: a slow consumer of a streamed text.
func BenchmarkPendingOpsFoldAppends(b *testing.B) {
	for _, n := range appendLadder {
		batch := []Op{{"a", []any{"text"}, appendDelta}}
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var pending PendingOps
				for range n {
					pending.Push(batch)
				}
				_ = pending.Take()
			}
		})
	}
}
