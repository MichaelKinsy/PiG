package tui

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nativeplatform"
)

// mouse-region.ts render/invalidate: a region renders its child's rows and invalidates its child.
// Pi: packages/tui/src/components/mouse-region.ts.
func TestMouseRegionDelegatesRenderAndInvalidate(t *testing.T) {
	child := &mouseProbe{lines: []string{"one", "two"}}
	region := NewMouseRegion(child, func(TuiMouseEvent) *TuiMouseEventResult { return nil })
	if got := region.Render(10); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("Render = %q", got)
	}
	child.NeedsRedraw()
	region.Invalidate()
	if !child.IsDirty() {
		t.Fatal("Invalidate did not reach the child")
	}
}

// native-platform: the tui helper is the platform helper, which is absent without a usable display on Linux and for unsupported architectures.
// Pi: packages/tui/src/native-platform.ts getNativeClipboard.
func TestGetNativeClipboardIsThePlatformClipboard(t *testing.T) {
	t.Setenv("DISPLAY", "")
	got, want := GetNativeClipboard(), nativeplatform.GetNativeClipboard()
	if got != want {
		t.Fatalf("GetNativeClipboard = %p, want the platform's %p", got, want)
	}
	if got != nil && got.GetText == nil {
		t.Fatal("a native clipboard cannot read text")
	}
}
