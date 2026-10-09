//go:build windows || darwin

package nativeplatform

import (
	"image"
	"image/color"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
)

// nativeImageFixture is a 3x2 opaque image with a distinct color per pixel, so a swapped channel, a flipped row or a wrong stride
// changes the decoded result.
func nativeImageFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	colors := []color.NRGBA{
		{R: 255, G: 0, B: 0, A: 255}, {R: 0, G: 255, B: 0, A: 255}, {R: 0, G: 0, B: 255, A: 255},
		{R: 10, G: 20, B: 30, A: 255}, {R: 200, G: 150, B: 100, A: 255}, {R: 1, G: 2, B: 3, A: 255},
	}
	for i, c := range colors {
		img.SetNRGBA(i%3, i/3, c)
	}
	return img
}

// An image the desktop session puts on the clipboard reads back through the native helper (native-platform.ts getImage): PNG
// data on macOS, PNG or DIB data converted to BMP on Windows. The helper's bytes decode to the same pixels in the same rows.
func TestNativeClipboardImageRoundTrip(t *testing.T) {
	if !nativeClipboardWritable() {
		t.Skip("set PI_TEST_NATIVE_CLIPBOARD=1 to overwrite the system clipboard")
	}
	want := nativeImageFixture()
	putImageOnDesktopClipboard(t, want)
	clipboard := GetNativeClipboard()
	if clipboard == nil || clipboard.GetImage == nil {
		t.Fatalf("clipboard = %+v", clipboard)
	}
	data, available, err := clipboard.GetImage(t.Context())
	if err != nil || !available || len(data) == 0 {
		t.Fatalf("GetImage = %d bytes, available %v, %v", len(data), available, err)
	}
	decoded, err := imageprocessing.DecodeImage(data)
	if err != nil {
		t.Fatalf("decode the clipboard image: %v", err)
	}
	if decoded.Bounds().Dx() != 3 || decoded.Bounds().Dy() != 2 {
		t.Fatalf("size = %v, want 3x2", decoded.Bounds())
	}
	for y := range 2 {
		for x := range 3 {
			r, g, b, a := decoded.At(x, y).RGBA()
			w := want.NRGBAAt(x, y)
			if uint8(r>>8) != w.R || uint8(g>>8) != w.G || uint8(b>>8) != w.B || a != 0xffff {
				t.Errorf("pixel (%d,%d) = %d,%d,%d,%d, want %v", x, y, r>>8, g>>8, b>>8, a>>8, w)
			}
		}
	}
}
