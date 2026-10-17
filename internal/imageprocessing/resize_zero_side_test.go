package imageprocessing

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"testing"
)

// image-resize-core.ts:105-125, :156: an image whose scaled side rounds to zero (8000x1 under the 2000 limit scales its height to round(0.25) = 0) reaches Photon's resize with a zero
// side, which throws, and resizeImageInProcess returns null. PiG reports no image instead of clamping the side to one pixel.
func TestResizeImageReturnsNoImageWhenAScaledSideRoundsToZero(t *testing.T) {
	for _, size := range [][2]int{{8000, 1}, {1, 8000}, {4001, 1}} {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, size[0], size[1]))); err != nil {
			t.Fatal(err)
		}
		if result, err := ResizeImage(encoded.Bytes(), "image/png", nil); !errors.Is(err, ErrImageTooLarge) || result.Data != "" {
			t.Errorf("%dx%d: result %+v, err %v; want no image", size[0], size[1], result, err)
		}
	}
	// 4000x1 scales to 2000x1 (round(0.5) = 1, JavaScript rounds the tie up) and stays an image.
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 4000, 1))); err != nil {
		t.Fatal(err)
	}
	if result, err := ResizeImage(encoded.Bytes(), "image/png", nil); err != nil || result.Width != 2000 || result.Height != 1 {
		t.Errorf("4000x1: result %+v, err %v; want 2000x1", result, err)
	}
}
