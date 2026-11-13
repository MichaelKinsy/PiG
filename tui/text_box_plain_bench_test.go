package tui

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// Plain Text and Box users that are not tool cards: static content, content that changes every frame, a box whose children change.

func plainBenchLines(n int, salt int) string {
	var b strings.Builder
	for i := range n {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "\x1b[3%dmline %d of the text, with some words to wrap around the edge of the terminal when it is narrow %d\x1b[0m", i%7, i, salt)
	}
	return b.String()
}

func BenchmarkPlainTextStatic(b *testing.B) {
	t := NewText(plainBenchLines(200, 0))
	t.Render(100)
	b.ReportAllocs()
	for b.Loop() {
		t.Render(100)
	}
}

// Every frame sets a different text with the same lines but the last.
func BenchmarkPlainTextLastLineChanges(b *testing.B) {
	base := plainBenchLines(200, 0)
	t := NewText(base)
	t.Render(100)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		i++
		t.SetText(base + fmt.Sprint(i))
		t.Render(100)
	}
}

// Every frame sets a text whose every line differs.
func BenchmarkPlainTextAllLinesChange(b *testing.B) {
	texts := [2]string{plainBenchLines(200, 1), plainBenchLines(200, 2)}
	t := NewText(texts[0])
	t.Render(100)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		i++
		t.SetText(texts[i&1])
		t.Render(100)
	}
}

// A text that is only ever drawn after being replaced: the first render of each new Text.
func BenchmarkPlainTextFresh(b *testing.B) {
	s := plainBenchLines(50, 3)
	b.ReportAllocs()
	for b.Loop() {
		NewText(s).Render(100)
	}
}

// A box of text children, one of which changes each frame; the box is rebuilt (Clear + AddChild) as ToolExecution does.
func BenchmarkPlainBoxChangingChild(b *testing.B) {
	texts := make([]*Text, 12)
	for i := range texts {
		texts[i] = NewText(plainBenchLines(3, i))
	}
	box := NewBox()
	box.BgFn = func(s string) string { return "\x1b[48;5;236m" + s + "\x1b[49m" }
	rebuild := func() {
		box.Clear()
		for _, t := range texts {
			box.AddChild(t)
		}
	}
	rebuild()
	box.Render(100)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		i++
		texts[5].SetText(plainBenchLines(3, i))
		rebuild()
		box.Render(100)
	}
}

// Same box, without rebuilding: children change, the box keeps its children.
func BenchmarkPlainBoxChangingChildNoRebuild(b *testing.B) {
	texts := make([]*Text, 12)
	box := NewBox()
	for i := range texts {
		texts[i] = NewText(plainBenchLines(3, i))
		box.AddChild(texts[i])
	}
	box.Render(100)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		i++
		texts[5].SetText(plainBenchLines(3, i))
		box.Invalidate()
		box.Render(100)
	}
}

// Box whose children all change every frame.
func BenchmarkPlainBoxAllChildrenChange(b *testing.B) {
	texts := make([]*Text, 12)
	box := NewBox()
	for i := range texts {
		texts[i] = NewText(plainBenchLines(3, i))
		box.AddChild(texts[i])
	}
	box.Render(100)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		i++
		for k, t := range texts {
			t.SetText(plainBenchLines(3, i*100+k))
		}
		box.Render(100)
	}
}

func heapInUse() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// Retained bytes of a large Text after one render, and after a changed render (the memo's cost).
func BenchmarkPlainTextRetained(b *testing.B) {
	const lines = 20000
	for _, mode := range []string{"rendered-once", "rendered-after-change"} {
		b.Run(mode, func(b *testing.B) {
			var keep []*Text
			before := heapInUse()
			for range b.N {
				t := NewText(plainBenchLines(lines, 0))
				t.Render(100)
				if mode == "rendered-after-change" {
					t.SetText(plainBenchLines(lines, 0) + "x")
					t.Render(100)
				}
				keep = append(keep, t)
			}
			after := heapInUse()
			b.ReportMetric(float64(after-before)/float64(b.N), "retained-B/op")
			runtime.KeepAlive(keep)
		})
	}
}
