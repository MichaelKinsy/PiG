package imageprocessing

import "testing"

// carriedJPEGOverPNG reports whether PiG's JPEG answer for the image raw resized to width x height is the one Pi e7a0df27 (#11551, D217)
// gives where the pinned Pi gives the PNG: PiG's own PNG of that size fits maxBytes and its JPEG at quality is smaller than it. The pinned
// Pi (image-resize-core.ts at b3a29f11) returns the PNG whenever it fits, PiG returns the smaller of the two, so the Pi oracles compare
// every other field of such an image and leave the mime type to this predicate. A zero quality or maxBytes is the default.
func carriedJPEGOverPNG(t *testing.T, raw []byte, width, height, quality, maxBytes int) bool {
	t.Helper()
	if quality == 0 {
		quality = JPEGQuality
	}
	if maxBytes == 0 {
		maxBytes = MaxEncodedBytes
	}
	src, err := decodeAutoOriented(raw)
	if err != nil || width < 1 || height < 1 {
		return false
	}
	resized := resizeImage(src, width, height)
	pngBytes, err := encodePNG(resized)
	if err != nil {
		t.Fatal(err)
	}
	jpegBytes, err := encodeJPEG(resized, quality)
	if err != nil {
		t.Fatal(err)
	}
	return encodedSizeBase64(pngBytes) < maxBytes && encodedSizeBase64(jpegBytes) < encodedSizeBase64(pngBytes)
}
