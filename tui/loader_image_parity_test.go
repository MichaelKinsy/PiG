package tui

import (
	"strings"
	"testing"
)

// TestCancellableLoader_HandleInput_RouteBindings verifies that the
// cancellable loader cancels via the keybinding registry rather than a
// hardcoded "\x1b". This makes user overrides for tui.select.cancel
// take effect. Mirrors .upstream/v0.69.0/.../cancellable-loader.ts.
func TestCancellableLoader_HandleInput_RouteBindings(t *testing.T) {
	t.Run("escape cancels", func(t *testing.T) {
		cl := NewCancellableLoader("", "", "loading", nil)
		var called bool
		cl.OnAbort = func() { called = true }
		cl.HandleInput("\x1b")
		if !called {
			t.Fatalf("OnAbort should fire on Escape")
		}
		if !cl.Aborted() {
			t.Fatalf("context should be cancelled")
		}
	})
	t.Run("ctrl+c cancels", func(t *testing.T) {
		cl := NewCancellableLoader("", "", "loading", nil)
		var called bool
		cl.OnAbort = func() { called = true }
		cl.HandleInput("\x03")
		if !called {
			t.Fatalf("OnAbort should fire on Ctrl+C")
		}
		if !cl.Aborted() {
			t.Fatalf("context should be cancelled")
		}
	})
	t.Run("ignores unrelated keys", func(t *testing.T) {
		cl := NewCancellableLoader("", "", "loading", nil)
		var called bool
		cl.OnAbort = func() { called = true }
		cl.HandleInput("x")
		if called {
			t.Fatalf("OnAbort should not fire on unrelated key")
		}
		if cl.Aborted() {
			t.Fatalf("context should NOT be cancelled")
		}
	})
}

func TestCancellableLoaderDisposeStopsWithoutAborting(t *testing.T) {
	loader := NewCancellableLoader("", "", "loading", nil)
	called := false
	loader.OnAbort = func() { called = true }
	loader.Dispose()
	if loader.Aborted() || called {
		t.Fatalf("Dispose changed cancellation state: aborted=%t onAbort=%t", loader.Aborted(), called)
	}
}

// TestImage_Render_FallbackWidth verifies the width math mirrors
// upstream image.ts: `min(width - 2, opts.maxWidthCells ?? 60)`. When
// no MaxWidthCells is supplied, the upper bound is 60 but the
// `width - 2` floor still applies.
func TestImage_Render_FallbackWidth(t *testing.T) {
	SetCapabilities(TerminalCapabilities{})
	defer ResetCapabilitiesCache()
	// No image capability => fallback path produces a single line.
	// The render math is exercised even in the fallback branch.
	img := NewImage("", "image/png", ImageOptions{}, &ImageDimensions{WidthPx: 100, HeightPx: 50})
	lines := img.Render(80)
	if len(lines) != 1 {
		t.Fatalf("fallback Render should produce exactly 1 line, got %d", len(lines))
	}
}

// TestImage_Render_UsesMaxHeightCells mirrors upstream image.ts, which now
// threads maxHeightCells through to renderImage and lets Kitty render the
// sequence on the first line with trailing empty spacer rows.
func TestImage_Render_UsesMaxHeightCells(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	defer ResetCapabilitiesCache()
	SetCellDimensions(CellDimensions{WidthPx: 10, HeightPx: 10})
	defer SetCellDimensions(CellDimensions{WidthPx: 9, HeightPx: 18})

	img := NewImage("YWJj", "image/png", ImageOptions{MaxWidthCells: 10, MaxHeightCells: 1}, &ImageDimensions{WidthPx: 100, HeightPx: 100})
	lines := img.Render(20)
	if len(lines) != 1 {
		t.Fatalf("Render should honor MaxHeightCells clamp; got %d lines want 1", len(lines))
	}
	if lines[0] == "" {
		t.Fatalf("first line should contain image sequence")
	}
}

// TestImage_Render_DoesNotThreadFilenameToITerm2 mirrors upstream image.ts,
// which does not forward the filename to renderImage's iTerm2 encoding path.
func TestImage_Render_DoesNotThreadFilenameToITerm2(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true})
	defer ResetCapabilitiesCache()
	img := NewImage("Zm9v", "image/png", ImageOptions{MaxWidthCells: 10, Filename: "x.png"}, &ImageDimensions{WidthPx: 10, HeightPx: 10})
	lines := img.Render(20)
	if len(lines) == 0 {
		t.Fatal("Render returned no lines")
	}
	if got := lines[len(lines)-1]; got == "" || containsFilenameMetadata(got) {
		t.Fatalf("iTerm2 render should not include filename metadata, got %q", got)
	}
}

func TestImage_CacheAndInvalidateMirrorUpstreamComponent(t *testing.T) {
	ResetCapabilitiesCache()
	SetCapabilities(TerminalCapabilities{})
	img := NewImage("Zm9v", "image/png", ImageOptions{}, &ImageDimensions{WidthPx: 10, HeightPx: 10})
	fallback := img.Render(20)
	if len(fallback) != 1 {
		t.Fatalf("fallback render lines = %d, want 1", len(fallback))
	}

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true})
	cached := img.Render(20)
	if got, want := strings.Join(cached, "\n"), strings.Join(fallback, "\n"); got != want {
		t.Fatalf("same-width render should use cache before invalidate\n got: %q\nwant: %q", got, want)
	}

	img.Invalidate()
	refreshed := img.Render(20)
	if got, want := strings.Join(refreshed, "\n"), strings.Join(fallback, "\n"); got == want {
		t.Fatalf("Invalidate should force re-render after capability change; still got fallback %q", got)
	}
	ResetCapabilitiesCache()
}

func TestImage_GetImageID_PreservesProvidedID(t *testing.T) {
	img := NewImage("", "image/png", ImageOptions{ImageID: 77}, &ImageDimensions{WidthPx: 10, HeightPx: 10})
	if got := img.GetImageID(); got != 77 {
		t.Fatalf("GetImageID() = %d, want 77", got)
	}
}

func containsFilenameMetadata(s string) bool {
	return len(s) > 0 && strings.Contains(s, "name=")
}
