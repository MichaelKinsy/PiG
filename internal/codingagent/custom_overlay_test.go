package codingagent

import "testing"

func TestCustomOverlaySuppressesIdenticalFrames(t *testing.T) {
	invalidations := 0
	overlay := newCustomOverlay(func() { invalidations++ })
	lines := []string{"one", "two"}
	overlay.UpdateLines(lines)
	lines[0] = "mutated"
	overlay.UpdateLines([]string{"one", "two"})
	if invalidations != 1 {
		t.Fatalf("invalidations = %d, want one for identical frames", invalidations)
	}
	if got := overlay.Render(80); len(got) != 2 || got[0] != "one" {
		t.Fatalf("cached lines = %v", got)
	}
}

func BenchmarkCustomOverlayCachedRender(b *testing.B) {
	overlay := newCustomOverlay(func() {})
	overlay.UpdateLines([]string{"alpha", "beta", "gamma"})
	b.ReportAllocs()
	for b.Loop() {
		_ = overlay.Render(120)
	}
}

func BenchmarkCustomOverlayIdenticalFrame(b *testing.B) {
	overlay := newCustomOverlay(func() {})
	lines := []string{"alpha", "beta", "gamma"}
	overlay.UpdateLines(lines)
	b.ReportAllocs()
	for b.Loop() {
		overlay.UpdateLines(lines)
	}
}
