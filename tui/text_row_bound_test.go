package tui

import (
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func longTextContent(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "\x1b[38;5;" + strconv.Itoa(1+i%200) + "mline " + strconv.Itoa(i) + " export function step(input) { return input.length; }\x1b[39m"
	}
	return strings.Join(lines, "\n")
}

// heapAfterGC is the heap bytes allocated after two collections: what a pool keeps in its victim cache and what a finalizer's
// object holds are freed by the second one.
func heapAfterGC() uint64 {
	var m runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// A Text of many thousand lines that changed once after it was drawn holds the memo of its rows for the next change; that memo is
// bounded, so the Text retains about what one drawn once retains (without the bound the memo made it a third larger).
//
// The retained bytes are the growth of the heap of the process, which counts every goroutine in it, not only the Texts: a goroutine
// that an earlier test left running and that holds 2 MB for a moment, or what the test before this one has not released yet, moved
// one reading against the other by 5% to 26% where the margin is 10% (the Text retains 22 MB). The test runs in a process of its
// own (testenv.InOwnProcess), where nothing but its Texts allocates.
func TestTextAfterOneChangeRetainsAboutAsMuchAsATextDrawnOnce(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
	const lines, width = 100000, 100
	retained := func(changeOnce bool) uint64 {
		before := heapAfterGC()
		text := NewText(longTextContent(lines))
		text.Render(width)
		if changeOnce {
			text.SetText(longTextContent(lines) + "x")
			text.Render(width)
		}
		after := heapAfterGC()
		runtime.KeepAlive(text)
		return after - before
	}
	once := retained(false)
	changed := retained(true)
	t.Logf("drawn once %d bytes, drawn after one change %d", once, changed)
	if changed > once+once/10 {
		t.Errorf("a %d line Text retains %d bytes after one change, %d drawn once (+%d%%)", lines, changed, once, (changed-once)*100/once)
	}
}
