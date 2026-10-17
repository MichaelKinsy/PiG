package tui

import (
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nativeplatform"
)

// cancellable-loader.ts: the tui.select.cancel key aborts the loader's signal and then calls onAbort; any other key does neither.
func TestCancellableLoaderAbortsOnTheCancelKeyOnly(t *testing.T) {
	loader := NewCancellableLoader(nil, nil, nil, "work", nil)
	t.Cleanup(loader.Dispose)
	aborts := 0
	loader.OnAbort = func() { aborts++ }
	loader.HandleInput("x")
	if loader.Aborted() || loader.Signal().Err() != nil || aborts != 0 {
		t.Fatalf("a non-cancel key aborted the loader (aborted=%v aborts=%d)", loader.Aborted(), aborts)
	}
	(*CancellableLoader).HandleInput(loader, "\x1b")
	if !loader.Aborted() || loader.Signal().Err() == nil {
		t.Fatal("the cancel key did not abort the signal")
	}
	if aborts != 1 {
		t.Fatalf("onAbort ran %d times, want once", aborts)
	}
}

// stack.ts HStack: children lay out left to right at their allocated widths, separated by the gap.
func TestHStackLaysChildrenOutLeftToRightWithTheGap(t *testing.T) {
	stack := NewHStack([]StackChild{
		{Component: NewText("ab"), StackEntryOptions: StackEntryOptions{Basis: new(3), Shrink: new(0)}},
		{Component: NewText("cd"), StackEntryOptions: StackEntryOptions{Basis: new(2), Shrink: new(0)}},
	}, StackOptions{Gap: new(2)})
	lines := (*HStack).Render(stack, 10)
	if len(lines) != 1 || strings.TrimRight(stripANSI(lines[0]), " ") != "ab   cd" {
		t.Fatalf("lines = %q, want ab, a gap of two, then cd", lines)
	}
	if got := NewHStack(nil, StackOptions{}).Render(10); len(got) != 0 {
		t.Fatalf("an empty stack rendered %q", got)
	}
}

// image.ts ImageTranscoder: the registered transcoder turns non-PNG data into PNG data for Kitty, and a failure leaves the text fallback.
func TestRegisteredImageTranscoderConvertsForKitty(t *testing.T) {
	withImageCapabilities(t, ImageProtocolKitty)
	calls := 0
	var transcode ImageTranscoder = func(data, mimeType string) (string, bool) {
		calls++
		if data == "jpeg-data" {
			return toolImagePNG, true
		}
		return "", false
	}
	SetImageTranscoder(transcode)
	t.Cleanup(func() { SetImageTranscoder(nil) })
	identity := ImageTheme{FallbackColor: func(s string) string { return s }}
	converted := strings.Join(NewImage("jpeg-data", "image/jpeg", identity, ImageOptions{}, &ImageDimensions{WidthPx: 10, HeightPx: 10}).Render(20), "\n")
	if !strings.Contains(converted, ";"+toolImagePNG) || calls == 0 {
		t.Fatalf("the transcoded image was not drawn: %q (calls %d)", converted, calls)
	}
	failed := strings.Join(NewImage("bad-data", "image/jpeg", identity, ImageOptions{}, &ImageDimensions{WidthPx: 10, HeightPx: 10}).Render(60), "\n")
	if strings.Contains(failed, "\x1b_G") || !strings.Contains(failed, "[Image: [image/jpeg]") {
		t.Fatalf("a failed conversion did not fall back to text: %q", failed)
	}
}

// mouse-region.ts MouseRegionHandler: a region's handler gets the event no nested child handled, and its result is the region's.
func TestMouseRegionHandlerRunsForEventsNoChildHandled(t *testing.T) {
	child := &mouseProbe{lines: []string{"x"}}
	var seen []TuiMouseEvent
	var handler MouseRegionHandler = func(event TuiMouseEvent) *TuiMouseEventResult {
		seen = append(seen, event)
		return &TuiMouseEventResult{Handled: true}
	}
	region := NewMouseRegion(child, handler)
	result := region.HandleMouse(leftMouse(MousePress, 0, 0, 5, 1))
	if result == nil || !result.Handled || len(seen) != 1 {
		t.Fatalf("handler result %+v after %d calls", result, len(seen))
	}
	declining := NewMouseRegion(child, func(TuiMouseEvent) *TuiMouseEventResult { return nil })
	if declining.HandleMouse(leftMouse(MousePress, 0, 0, 5, 1)) != nil {
		t.Fatal("a handler that declines must leave the event unhandled")
	}
	handled := NewMouseRegion(&mouseProbe{lines: []string{"y"}, result: &TuiMouseEventResult{Handled: true}}, handler)
	before := len(seen)
	handled.HandleMouse(leftMouse(MousePress, 0, 0, 5, 1))
	if len(seen) != before {
		t.Fatal("the region handler ran although a nested child handled the event")
	}
}

// stack.ts VStack: children stack top to bottom with the gap as blank rows between them.
func TestVStackStacksChildrenTopToBottomWithTheGap(t *testing.T) {
	stack := NewVStack([]StackChild{{Component: NewText("a")}, {Component: NewText("b")}}, StackOptions{Gap: new(2)})
	lines := (*VStack).Render(stack, 5)
	var text []string
	for _, line := range lines {
		text = append(text, strings.TrimRight(stripANSI(line), " "))
	}
	if strings.Join(text, "|") != "a|||b" {
		t.Fatalf("lines = %q, want a, two blank rows, then b", text)
	}
}

// native-platform.ts getNativeClipboard: the TUI re-export hands back the platform layer's clipboard handle, which exists only where a
// display does (on Linux, DISPLAY).
func TestGetNativeClipboardReturnsThePlatformClipboard(t *testing.T) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("the Linux display rule needs linux on amd64 or arm64")
	}
	t.Setenv("DISPLAY", "")
	if GetNativeClipboard() != nil {
		t.Fatal("a clipboard exists without a display")
	}
	t.Setenv("DISPLAY", ":99")
	got := GetNativeClipboard()
	if got == nil || got != nativeplatform.GetNativeClipboard() {
		t.Fatalf("GetNativeClipboard() = %p, want the platform handle %p", got, nativeplatform.GetNativeClipboard())
	}
}
