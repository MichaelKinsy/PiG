package tui

import (
	"sync/atomic"
	"testing"
	"time"
)

func newTestScrollView(t *testing.T, opts ScrollViewOptions) *ScrollView {
	t.Helper()
	sv := NewScrollView(&stubComponent{lines: []string{"x"}}, opts)
	t.Cleanup(sv.Dispose)
	return sv
}

func TestScrollViewDefaults(t *testing.T) {
	sv := newTestScrollView(t, ScrollViewOptions{})
	if sv.Primary() {
		t.Error("Primary default should be false")
	}
	if sv.Overscroll() != "chain" {
		t.Errorf("Overscroll = %q, want chain", sv.Overscroll())
	}
	if sv.Scrollbar() != "hidden" {
		t.Errorf("Scrollbar = %q, want hidden", sv.Scrollbar())
	}
	if sv.scrollbarHideDelayMs != 1000 {
		t.Errorf("hide delay = %d, want 1000", sv.scrollbarHideDelayMs)
	}
}

func TestScrollViewUnsupportedAxisPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unsupported axis")
		}
	}()
	NewScrollView(&stubComponent{}, ScrollViewOptions{Axis: "horizontal"})
}

func TestScrollViewScrollToClamps(t *testing.T) {
	sv := newTestScrollView(t, ScrollViewOptions{})
	sv.UpdateLayout(100, 10, func() {}) // maxScrollTop = 90
	sv.ScrollTo(200)
	if sv.ScrollTop() != 90 {
		t.Errorf("ScrollTop = %d, want 90 (clamped to max)", sv.ScrollTop())
	}
	sv.ScrollTo(-5)
	if sv.ScrollTop() != 0 {
		t.Errorf("ScrollTop = %d, want 0 (clamped to min)", sv.ScrollTop())
	}
}

func TestScrollViewScrollByReturnsLeftover(t *testing.T) {
	sv := newTestScrollView(t, ScrollViewOptions{})
	sv.UpdateLayout(100, 10, func() {}) // maxScrollTop = 90, start 0
	// Ask for 95, only 90 available -> leftover 5.
	if leftover := sv.ScrollBy(95); leftover != 5 {
		t.Errorf("ScrollBy leftover = %d, want 5", leftover)
	}
	if sv.ScrollTop() != 90 {
		t.Errorf("ScrollTop = %d, want 90", sv.ScrollTop())
	}
}

func TestScrollViewFollowEndPinsToBottom(t *testing.T) {
	sv := newTestScrollView(t, ScrollViewOptions{Follow: "end"})
	sv.UpdateLayout(50, 10, func() {}) // maxScrollTop 40, followingEnd -> scrollTop 40
	if sv.ScrollTop() != 40 {
		t.Errorf("ScrollTop = %d, want 40 (followed end)", sv.ScrollTop())
	}
	if !sv.IsFollowingEnd() {
		t.Error("should be following end")
	}
	// Growing content keeps it pinned to the new bottom.
	sv.UpdateLayout(70, 10, func() {})
	if sv.ScrollTop() != 60 {
		t.Errorf("ScrollTop = %d, want 60 after growth", sv.ScrollTop())
	}
}

func TestScrollViewGetContentWidthReservesColumnForAlwaysScrollbar(t *testing.T) {
	sv := newTestScrollView(t, ScrollViewOptions{Scrollbar: "always"})
	if got := sv.GetContentWidth(20); got != 19 {
		t.Errorf("GetContentWidth(always) = %d, want 19", got)
	}
	if got := sv.GetContentWidth(1); got != 1 {
		t.Errorf("GetContentWidth(1) = %d, want 1 (no room to reserve)", got)
	}
	hidden := newTestScrollView(t, ScrollViewOptions{})
	if got := hidden.GetContentWidth(20); got != 20 {
		t.Errorf("GetContentWidth(hidden) = %d, want 20", got)
	}
}

func TestScrollViewIsScrollbarVisibleAlways(t *testing.T) {
	sv := newTestScrollView(t, ScrollViewOptions{Scrollbar: "always"})
	sv.UpdateLayout(5, 0, func() {})
	if sv.IsScrollbarVisible() {
		t.Error("always scrollbar hidden when viewport height 0")
	}
	sv.UpdateLayout(5, 3, func() {})
	if !sv.IsScrollbarVisible() {
		t.Error("always scrollbar should show when viewport > 0")
	}
}

func TestScrollViewRenderPadsWhenScrollbarReserved(t *testing.T) {
	child := &stubComponent{lines: []string{"ab", "cd"}}
	sv := NewScrollView(child, ScrollViewOptions{Scrollbar: "always"})
	t.Cleanup(sv.Dispose)
	got := sv.Render(3) // contentWidth 2 != 3 -> pad each line with a space
	want := []string{"ab ", "cd "}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestScrollViewMutatorsPanic(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*ScrollView)
	}{
		{"Add", func(s *ScrollView) { s.Add(&stubComponent{}) }},
		{"Remove", func(s *ScrollView) { s.Remove(&stubComponent{}) }},
		{"Clear", func(s *ScrollView) { s.Clear() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv := newTestScrollView(t, ScrollViewOptions{})
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("%s should panic", tc.name)
				}
			}()
			tc.call(sv)
		})
	}
}

// TestScrollViewTransientScrollbarAutoHides exercises the owned hide timer: auto
// scrollbar shows on activity, then the timer fires off-goroutine, hides it, and
// invokes the render callback.
func TestScrollViewTransientScrollbarAutoHides(t *testing.T) {
	var renders atomic.Int64
	sv := NewScrollView(&stubComponent{lines: []string{"x"}}, ScrollViewOptions{
		Scrollbar:            "auto",
		ScrollbarHideDelayMs: new(20),
	})
	t.Cleanup(sv.Dispose)
	sv.UpdateLayout(100, 10, func() { renders.Add(1) }) // content > viewport
	sv.ScrollBy(5)                                      // activity -> transient shows + arms timer
	if !sv.IsScrollbarVisible() {
		t.Fatal("auto scrollbar should be visible right after activity")
	}
	// Wait for the timer to fire and hide it (generous bound).
	deadline := time.Now().Add(2 * time.Second)
	for sv.IsScrollbarVisible() {
		if time.Now().After(deadline) {
			t.Fatal("transient scrollbar never auto-hid")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if renders.Load() < 2 {
		t.Errorf("render callback count = %d, want >=2 (scrollBy + timer hide)", renders.Load())
	}
}

// TestScrollViewDisposeStopsTimer proves Dispose cancels a pending hide so no
// callback fires afterward.
func TestScrollViewDisposeStopsTimer(t *testing.T) {
	var renders atomic.Int64
	sv := NewScrollView(&stubComponent{lines: []string{"x"}}, ScrollViewOptions{
		Scrollbar:            "auto",
		ScrollbarHideDelayMs: new(50),
	})
	sv.UpdateLayout(100, 10, func() { renders.Add(1) })
	sv.ScrollBy(5)
	before := renders.Load()
	sv.Dispose()
	time.Sleep(120 * time.Millisecond)
	if got := renders.Load(); got != before {
		t.Errorf("render fired after Dispose: %d != %d", got, before)
	}
}
