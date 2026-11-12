package tui

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func longTextContent(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "\x1b[38;5;" + strconv.Itoa(1+i%200) + "mline " + strconv.Itoa(i) + " export function step(input) { return input.length; }\x1b[39m"
	}
	return strings.Join(lines, "\n")
}

func heapAfterGC() uint64 {
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// A Text of many thousand lines that changed once after it was drawn holds the memo of its rows for the next change; that memo is
// bounded, so the Text retains about what one drawn once retains (without the bound the memo made it a third larger).
func TestTextAfterOneChangeRetainsAboutAsMuchAsATextDrawnOnce(t *testing.T) {
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
