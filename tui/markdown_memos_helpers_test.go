package tui

import (
	"runtime/debug"
	"sync"
	"testing"
)

// memosOf is what a render of m kept for the next one, or an empty set when none was kept or a garbage collection released it.
func memosOf(m *Markdown) *markdownMemos {
	if memos := m.memos(); memos != nil {
		return memos
	}
	return &markdownMemos{}
}

var gcPause struct {
	sync.Mutex
	holders         int
	percent, memory int64
}

// pauseGC holds off the collector until the test ends, as a JavaScript job keeps a WeakRef's target alive until it ends:
// markdown.ts's tests that expect a render to reuse the last one's blocks run without a collection in between, and the
// memos of a Go Markdown are held weakly the same way. A test that inspects the memos, or counts the work a streamed render
// skips, calls it; an explicit runtime.GC still collects. The memory limit makes the collector run anyway before a long
// loop exhausts the machine, and a test then sees a collection, which only costs it the memos.
func pauseGC(t testing.TB) {
	t.Helper()
	gcPause.Lock()
	if gcPause.holders == 0 {
		gcPause.percent = int64(debug.SetGCPercent(-1))
		gcPause.memory = debug.SetMemoryLimit(3 << 30)
	}
	gcPause.holders++
	gcPause.Unlock()
	t.Cleanup(func() {
		gcPause.Lock()
		defer gcPause.Unlock()
		if gcPause.holders--; gcPause.holders == 0 {
			debug.SetGCPercent(int(gcPause.percent))
			debug.SetMemoryLimit(gcPause.memory)
		}
	})
}
