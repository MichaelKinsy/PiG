//go:build !race

package tui

import "testing"

// The first scan of a transcript whose children are polled allocates their list once: appended one index at a time it was allocated
// 13 times for 1,500 children, and a five-frame benchmark read that first frame's growth as three to five allocations per frame.
func TestSettledScanAllocatesThePolledListOnce(t *testing.T) {
	const kids = 1500
	chat := NewContainer()
	for range kids {
		chat.Add(&countingDirty{})
	}
	chat.Render(80)
	var n nestedRender
	scan := func() {
		chat.mu.Lock()
		defer chat.mu.Unlock()
		chat.settled, chat.polled = 0, nil
		if first := chat.scanSettledLocked(80, &n); first != kids || len(chat.polled) != kids {
			t.Fatalf("the scan settled %d children and polls %d, want %d of each", first, len(chat.polled), kids)
		}
	}
	if got := testing.AllocsPerRun(10, scan); got != 1 {
		t.Errorf("the first scan of %d polled children allocated %v times, want 1: the list", kids, got)
	}
	// A container none of whose children are polled keeps no list at all.
	texts := NewContainer()
	for range kids {
		texts.Add(NewText("settled"))
	}
	texts.Render(80)
	texts.Render(80)
	if texts.polled != nil {
		t.Errorf("a container of %d children that report their invalidations holds a polled list of capacity %d, want none", kids, cap(texts.polled))
	}
}
