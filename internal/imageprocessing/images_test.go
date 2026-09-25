package imageprocessing

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"

	"golang.org/x/image/bmp"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// makePNGImage builds a w×h RGBA image filled with `c` and encodes
// it as PNG.
func makePNGImage(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func makeJPEGImage(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func decodeBoundsPNG(t *testing.T, b []byte) (w, h int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	r := img.Bounds()
	return r.Dx(), r.Dy()
}

func decodeBoundsJPEG(t *testing.T, b []byte) (w, h int) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	r := img.Bounds()
	return r.Dx(), r.Dy()
}

func TestNormalizeToolResultImagesResizesBeforeHistory(t *testing.T) {
	input := makePNGImage(t, 2200, 1100, color.RGBA{10, 20, 30, 255})
	result := agent.AgentToolResult{
		Content: "screenshot",
		Images: []ai.ImageContent{{
			MimeType: "image/png",
			Data:     base64.StdEncoding.EncodeToString(input),
		}},
	}
	normalized := NormalizeToolResultImages(result, true)
	if len(normalized.Images) != 1 {
		t.Fatalf("normalized images = %d", len(normalized.Images))
	}
	decoded, err := base64.StdEncoding.DecodeString(normalized.Images[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	image, _, err := image.Decode(bytes.NewReader(decoded))
	if err != nil {
		t.Fatal(err)
	}
	if width := image.Bounds().Dx(); width > MaxLongestSide {
		t.Fatalf("normalized width = %d, want <= %d", width, MaxLongestSide)
	}
	if !strings.Contains(normalized.Content, "original 2200x1100, displayed at 2000x1000") {
		t.Fatalf("normalization hint missing: %q", normalized.Content)
	}
	if strings.Contains(normalized.Content, "converted from") {
		t.Fatalf("supported PNG resize must not report a format conversion: %q", normalized.Content)
	}

	unchanged := NormalizeToolResultImages(result, false)
	if unchanged.Images[0].Data != result.Images[0].Data || unchanged.Content != result.Content {
		t.Fatal("disabled normalization changed the tool result")
	}
}

// TestNormalizeToolResultImagesConvertsWhenAutoResizeDisabled pins upstream
// processImage(autoResizeImages:false) and the test "converts unsupported image
// formats even when auto-resize is disabled": normalizeImage still runs, so a
// BMP is converted to PNG and reports the conversion; only the resize is
// skipped. A supported format with resize disabled passes through untouched.
func TestNormalizeToolResultImagesConvertsWhenAutoResizeDisabled(t *testing.T) {
	bmpBytes := makeBMPImage(t, 6, 6, color.RGBA{200, 50, 50, 255})
	result := agent.AgentToolResult{
		Content: "shot",
		Images: []ai.ImageContent{{
			MimeType: "image/bmp",
			Data:     base64.StdEncoding.EncodeToString(bmpBytes),
		}},
	}
	got := NormalizeToolResultImages(result, false)
	if got.Images[0].MimeType != "image/png" {
		t.Fatalf("autoResize=false: output MIME = %q, want image/png", got.Images[0].MimeType)
	}
	if got.Images[0].Data == result.Images[0].Data {
		t.Fatal("autoResize=false: BMP bytes were not converted")
	}
	if !strings.Contains(got.Content, "[Image converted from image/bmp to image/png.]") {
		t.Fatalf("autoResize=false: conversion hint missing from %q", got.Content)
	}
}

// TestNormalizeToolResultImagesEmitsConversionHint pins upstream
// image-process.ts: normalizing an unsupported still-image format (BMP) to a
// supported one converts the bytes and appends the conversion hint
// "[Image converted from image/bmp to image/png.]" to the tool result, mirroring
// processImage's convertedHint. Supported inputs re-encoded only for size do
// not report a conversion.
func TestNormalizeToolResultImagesEmitsConversionHint(t *testing.T) {
	bmpBytes := makeBMPImage(t, 6, 6, color.RGBA{200, 50, 50, 255})
	result := agent.AgentToolResult{
		Content: "shot",
		Images: []ai.ImageContent{{
			MimeType: "image/bmp",
			Data:     base64.StdEncoding.EncodeToString(bmpBytes),
		}},
	}
	got := NormalizeToolResultImages(result, true)
	if got.Images[0].MimeType != "image/png" {
		t.Fatalf("output MIME = %q, want image/png", got.Images[0].MimeType)
	}
	if !strings.Contains(got.Content, "[Image converted from image/bmp to image/png.]") {
		t.Fatalf("conversion hint missing from %q", got.Content)
	}
}

// TestImageConversionHint pins the processImage convertedFrom semantics: only an
// unsupported declared input MIME that changed format reports a conversion.
func TestImageConversionHint(t *testing.T) {
	cases := []struct {
		in, out, want string
	}{
		{"image/bmp", "image/png", "[Image converted from image/bmp to image/png.]"},
		{"image/bmp; charset=binary", "image/png", "[Image converted from image/bmp to image/png.]"},
		{"IMAGE/BMP", "image/png", "[Image converted from image/bmp to image/png.]"},
		{"image/png", "image/jpeg", ""}, // supported input re-encoded: no conversion
		{"image/png", "image/png", ""},  // unchanged
		{"image/gif", "image/png", ""},  // supported input passes through upstream
		{"", "image/png", ""},           // no declared input MIME
	}
	for _, tc := range cases {
		if got := imageConversionHint(tc.in, tc.out); got != tc.want {
			t.Errorf("imageConversionHint(%q, %q) = %q, want %q", tc.in, tc.out, got, tc.want)
		}
	}
}

func TestResizeBelowThresholdNoop(t *testing.T) {
	// 800×600 solid-red PNG (no alpha) should round-trip with no change.
	in := makePNGImage(t, 800, 600, color.RGBA{255, 0, 0, 255})
	out, mime, err := ResizeImageForLLM(in)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if mime != "image/png" {
		t.Errorf("mime: %q want image/png", mime)
	}
	if !bytes.Equal(out, in) {
		// Acceptable if dimensions match and bytes are simply
		// re-encoded; but for the no-resize/no-overflow case we
		// expect the original bytes back per the explicit short-circuit.
		w, h := decodeBoundsPNG(t, out)
		if w != 800 || h != 600 {
			t.Errorf("dimensions changed: got %dx%d want 800x600", w, h)
		}
		t.Errorf("expected byte-equal round-trip on tiny input; len(in)=%d len(out)=%d", len(in), len(out))
	}
}

func TestResizeOver2048Resizes(t *testing.T) {
	// 4096×2048 → longest side must be 2048 in output.
	// Use JPEG input to avoid OOM (4096×4096 PNG is huge).
	in := makeJPEGImage(t, 4096, 2048, color.RGBA{0, 128, 0, 255})
	out, mime, err := ResizeImageForLLM(in)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	var w, h int
	switch mime {
	case "image/png":
		w, h = decodeBoundsPNG(t, out)
	case "image/jpeg":
		w, h = decodeBoundsJPEG(t, out)
	default:
		t.Fatalf("unexpected mime: %q", mime)
	}
	longest := max(h, w)
	if longest != MaxLongestSide {
		t.Errorf("longest side %d want %d (mime=%s w=%d h=%d)", longest, MaxLongestSide, mime, w, h)
	}
	// Aspect ratio preserved within 1px.
	wantH := MaxLongestSide / 2
	if h < wantH-2 || h > wantH+2 {
		t.Errorf("aspect not preserved: got %dx%d want ~%dx%d", w, h, MaxLongestSide, wantH)
	}
}

func TestResizeFitsUnderThresholdForCompressibleInput(t *testing.T) {
	// Smooth gradient: highly compressible. 4000×4000 PNG is huge but
	// the 2048×2048 JPEG output should comfortably fit under 1 MB.
	img := image.NewRGBA(image.Rect(0, 0, 4000, 4000))
	for y := range 4000 {
		for x := range 4000 {
			img.Set(x, y, color.RGBA{uint8(x / 16), uint8(y / 16), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	in := buf.Bytes()
	if len(in) <= MaxEncodedBytes {
		t.Skipf("gradient PNG fit at source (%d <= %d); test irrelevant", len(in), MaxEncodedBytes)
	}
	out, mime, err := ResizeImageForLLM(in)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if len(out) > MaxEncodedBytes {
		t.Errorf("compressible input must fit under %d bytes; got %d (mime=%s)", MaxEncodedBytes, len(out), mime)
	}
}

func TestResizeReencodesIfOverThreshold(t *testing.T) {
	// Oversized noisy input should be resized to the configured bounds and the
	// resulting base64 payload must fit under the upstream byte limit. Depending
	// on the image content, either PNG or JPEG may be the first candidate under
	// the threshold.
	img := image.NewRGBA(image.Rect(0, 0, 3000, 3000))
	for y := range 3000 {
		for x := range 3000 {
			img.Set(x, y, color.RGBA{
				R: uint8((x * 7) ^ (y * 13)),
				G: uint8((x * 31) ^ (y * 17)),
				B: uint8((x * 53) ^ (y * 29)),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	in := buf.Bytes()

	out, mime, err := ResizeImageForLLM(in)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if encodedSizeBase64(out) >= MaxEncodedBytes {
		t.Fatalf("encoded size %d must fit under %d", encodedSizeBase64(out), MaxEncodedBytes)
	}
	var w, h int
	switch mime {
	case "image/png":
		w, h = decodeBoundsPNG(t, out)
	case "image/jpeg":
		w, h = decodeBoundsJPEG(t, out)
	default:
		t.Fatalf("unexpected mime %q", mime)
	}
	if longest := max(h, w); longest != MaxLongestSide {
		t.Errorf("longest %d want %d", longest, MaxLongestSide)
	}
}

func TestResizePreservesAlpha(t *testing.T) {
	// 1000×1000 PNG with alpha: longest side fits, but force resize
	// by making it 3000×3000. Output MUST be PNG (not JPEG) per
	// chunk-d.md gotcha #610.
	img := image.NewRGBA(image.Rect(0, 0, 3000, 3000))
	// Half-transparent green.
	for y := range 3000 {
		for x := range 3000 {
			img.Set(x, y, color.RGBA{0, 255, 0, 128})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	in := buf.Bytes()

	out, mime, err := ResizeImageForLLM(in)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if mime != "image/png" {
		t.Errorf("PNG-with-alpha must stay PNG; got %s", mime)
	}
	// Decode and check alpha is preserved.
	decoded, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !imageHasAlpha(decoded) {
		t.Errorf("alpha channel lost in re-encode")
	}
}

// TestExifOrientationApplied pins the no-EXIF path: a plain JPEG below the
// resize threshold keeps its dimensions 1:1 and is returned untransformed.
func TestExifOrientationApplied(t *testing.T) {
	in := makeJPEGImage(t, 100, 200, color.RGBA{255, 0, 255, 255})
	out, mime, err := ResizeImageForLLM(in)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	var w, h int
	switch mime {
	case "image/png":
		w, h = decodeBoundsPNG(t, out)
	case "image/jpeg":
		w, h = decodeBoundsJPEG(t, out)
	}
	// Below threshold + no resize → dimensions preserved 1:1.
	if w != 100 || h != 200 {
		t.Errorf("dimensions changed unexpectedly: got %dx%d want 100x200 (mime=%s)", w, h, mime)
	}
}

// labelledImage builds a w×h NRGBA whose pixel (x,y) carries the index
// y*w+x in its red channel, so a transform can be read back exactly.
func labelledImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(y*w + x), G: 7, B: 9, A: 255})
		}
	}
	return img
}

// labelGrid reads the labels of img back as rows of ints.
func labelGrid(t *testing.T, img image.Image) [][]int {
	t.Helper()
	b := img.Bounds()
	out := make([][]int, 0, b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := make([]int, 0, b.Dx())
		for x := b.Min.X; x < b.Max.X; x++ {
			r, _, _, _ := img.At(x, y).RGBA()
			row = append(row, int(r>>8))
		}
		out = append(out, row)
	}
	return out
}

// TestFixOrientationAllCases pins every EXIF orientation transform against the
// standard EXIF orientation table. The source is the 2×3 grid
//
//	0 1
//	2 3
//	4 5
//
// and each expected grid is the upright image the EXIF specification defines
// for that stored orientation. The grids were also read back from
// disintegration/imaging v1.6.2, the previous implementation, so this table
// pins the replacement to the exact prior behavior.
func TestFixOrientationAllCases(t *testing.T) {
	src := labelledImage(2, 3)
	cases := []struct {
		name string
		o    exifOrientation
		want [][]int
	}{
		{"0 out of range", exifOrientation(0), [][]int{{0, 1}, {2, 3}, {4, 5}}},
		{"1 normal", orientationNormal, [][]int{{0, 1}, {2, 3}, {4, 5}}},
		{"2 mirror horizontal", orientationFlipH, [][]int{{1, 0}, {3, 2}, {5, 4}}},
		{"3 rotate 180", orientationRotate180, [][]int{{5, 4}, {3, 2}, {1, 0}}},
		{"4 mirror vertical", orientationFlipV, [][]int{{4, 5}, {2, 3}, {0, 1}}},
		{"5 transpose", orientationTranspose, [][]int{{0, 2, 4}, {1, 3, 5}}},
		{"6 rotate 90 cw stored", orientationRotate270, [][]int{{4, 2, 0}, {5, 3, 1}}},
		{"7 transverse", orientationTransverse, [][]int{{5, 3, 1}, {4, 2, 0}}},
		{"8 rotate 270 cw stored", orientationRotate90, [][]int{{1, 3, 5}, {0, 2, 4}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := labelGrid(t, fixOrientation(src, tc.o))
			if len(got) != len(tc.want) {
				t.Fatalf("height = %d, want %d (grid %v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if len(got[i]) != len(tc.want[i]) {
					t.Fatalf("row %d width = %d, want %d", i, len(got[i]), len(tc.want[i]))
				}
				for j := range got[i] {
					if got[i][j] != tc.want[i][j] {
						t.Fatalf("grid = %v, want %v", got, tc.want)
					}
				}
			}
		})
	}
}

// jpegWithExifOrientation splices a minimal APP1/EXIF segment carrying the
// given orientation tag immediately after the SOI marker of a real JPEG.
func jpegWithExifOrientation(t *testing.T, w, h int, orientation uint16) []byte {
	t.Helper()
	base := makeJPEGImage(t, w, h, color.RGBA{20, 200, 60, 255})
	if base[0] != 0xff || base[1] != 0xd8 {
		t.Fatalf("encoder did not emit SOI: %x", base[:2])
	}

	// TIFF block: big-endian header, one IFD entry (orientation, SHORT, 1).
	tiff := []byte{
		0x4d, 0x4d, // byte order: big endian
		0x00, 0x2a, // TIFF magic
		0x00, 0x00, 0x00, 0x08, // offset of IFD0
		0x00, 0x01, // tag count
		0x01, 0x12, // tag: Orientation
		0x00, 0x03, // type: SHORT
		0x00, 0x00, 0x00, 0x01, // count: 1
		byte(orientation >> 8), byte(orientation), 0x00, 0x00, // value, padded
		0x00, 0x00, 0x00, 0x00, // next IFD: none
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	size := len(payload) + 2

	var out bytes.Buffer
	out.Write(base[:2])
	out.Write([]byte{0xff, 0xe1, byte(size >> 8), byte(size)})
	out.Write(payload)
	out.Write(base[2:])
	return out.Bytes()
}

// TestReadExifOrientationParsesAllValues pins the EXIF tag reader across every
// valid orientation, the out-of-range values the tag reader must reject, and
// the inputs that carry no readable tag at all.
func TestReadExifOrientationParsesAllValues(t *testing.T) {
	for v := uint16(1); v <= 8; v++ {
		data := jpegWithExifOrientation(t, 4, 6, v)
		if got := getExifOrientation(data); got != exifOrientation(v) {
			t.Errorf("orientation %d: read %d", v, got)
		}
	}
	for _, v := range []uint16{0, 9, 65535} {
		data := jpegWithExifOrientation(t, 4, 6, v)
		if got := getExifOrientation(data); got != orientationNormal {
			t.Errorf("out-of-range orientation %d: read %d, want normal", v, got)
		}
	}

	noExif := makeJPEGImage(t, 4, 6, color.RGBA{1, 2, 3, 255})
	if got := getExifOrientation(noExif); got != orientationNormal {
		t.Errorf("plain JPEG: read %d, want normal", got)
	}
	notJPEG := makePNGImage(t, 4, 6, color.RGBA{1, 2, 3, 255})
	if got := getExifOrientation(notJPEG); got != orientationNormal {
		t.Errorf("PNG: read %d, want normal", got)
	}
	// Truncated APP1: the segment header promises data the stream does not have.
	truncated := jpegWithExifOrientation(t, 4, 6, 6)
	if got := getExifOrientation(truncated[:8]); got != orientationNormal {
		t.Errorf("truncated EXIF: read %d, want normal", got)
	}
	if got := getExifOrientation(nil); got != orientationNormal {
		t.Errorf("empty input: read %d, want normal", got)
	}
}

// TestDecodeAutoOrientedAppliesExif pins the end-to-end decode path: a JPEG
// stored 4×6 with a quarter-turn orientation tag decodes upright as 6×4, and an
// unrotated or unreadable tag leaves the stored dimensions alone.
func TestDecodeAutoOrientedAppliesExif(t *testing.T) {
	cases := []struct {
		orientation  uint16
		wantW, wantH int
	}{
		{1, 4, 6}, {2, 4, 6}, {3, 4, 6}, {4, 4, 6},
		{5, 6, 4}, {6, 6, 4}, {7, 6, 4}, {8, 6, 4},
		{0, 4, 6}, {9, 4, 6},
	}
	for _, tc := range cases {
		data := jpegWithExifOrientation(t, 4, 6, tc.orientation)
		img, err := decodeAutoOriented(data)
		if err != nil {
			t.Fatalf("orientation %d: decode: %v", tc.orientation, err)
		}
		if w, h := img.Bounds().Dx(), img.Bounds().Dy(); w != tc.wantW || h != tc.wantH {
			t.Errorf("orientation %d: %dx%d, want %dx%d", tc.orientation, w, h, tc.wantW, tc.wantH)
		}
	}

	if _, err := decodeAutoOriented([]byte("not an image")); err == nil {
		t.Error("decodeAutoOriented accepted non-image bytes")
	}
}

// TestResizeImageDimensions pins the resampler's output geometry, including the
// degenerate 1-pixel targets the shrink loop in prepareImageForLLM walks down to.
func TestDecodeSupportedWebP(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(
		"UklGRjwAAABXRUJQVlA4IDAAAADQAQCdASoBAAEAAgA0JaACdLoB+AADsAD+8MQL/yC5YXXI1/8gP+QH/ID/+PIAAAA=",
	)
	if err != nil {
		t.Fatal(err)
	}
	img, err := decodeAutoOriented(data)
	if err != nil {
		t.Fatalf("decode WebP: %v", err)
	}
	if got := img.Bounds().Size(); got != (image.Point{X: 1, Y: 1}) {
		t.Fatalf("WebP size = %v, want 1x1", got)
	}
}

func TestResizeImageDimensions(t *testing.T) {
	src := labelledImage(9, 6)
	cases := [][2]int{{3, 2}, {18, 12}, {9, 6}, {1, 1}, {1, 6}, {9, 1}}
	for _, tc := range cases {
		got := resizeImage(src, tc[0], tc[1])
		if w, h := got.Bounds().Dx(), got.Bounds().Dy(); w != tc[0] || h != tc[1] {
			t.Errorf("resizeImage(%d,%d) = %dx%d", tc[0], tc[1], w, h)
		}
	}
}

func TestDetectFormat(t *testing.T) {
	cases := map[string][]byte{
		"png":  {0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3},
		"jpeg": {0xff, 0xd8, 0xff, 0xe0, 0, 0, 0, 0},
		"webp": {'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'E', 'B', 'P', 0},
		"gif":  []byte("GIF89a..."),
		"bmp":  []byte{'B', 'M', 1, 2},
		"":     {0, 1, 2, 3},
	}
	for want, in := range cases {
		if got := detectFormat(in); got != want {
			t.Errorf("detectFormat(%v) = %q want %q", in[:min(len(in), 6)], got, want)
		}
	}
}

func TestDetectSupportedImageMimeType(t *testing.T) {
	validPNG := append(append([]byte{}, pngSignature...), []byte{
		0, 0, 0, 13, 'I', 'H', 'D', 'R',
		0, 0, 0, 1, 0, 0, 0, 1, 8, 2, 0, 0, 0,
		0, 0, 0, 0,
	}...)
	animatedPNG := append(append([]byte{}, validPNG...), []byte{
		0, 0, 0, 0, 'a', 'c', 'T', 'L',
		0, 0, 0, 0,
	}...)
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "jpeg", data: []byte{0xff, 0xd8, 0xff, 0xe0}, want: "image/jpeg"},
		{name: "jpeg jpegls unsupported", data: []byte{0xff, 0xd8, 0xff, 0xf7}, want: ""},
		{name: "png", data: validPNG, want: "image/png"},
		{name: "animated png unsupported", data: animatedPNG, want: ""},
		{name: "gif", data: []byte("GIF89a..."), want: "image/gif"},
		{name: "gif-prefixed text", data: []byte("GIF is not an image\n"), want: ""},
		{name: "truncated gif signature", data: []byte("GIF89"), want: ""},
		{name: "webp", data: []byte("RIFF\x00\x00\x00\x00WEBP"), want: "image/webp"},
		{name: "bmp", data: []byte{'B', 'M', 58, 0, 0, 0, 0, 0, 0, 0, 54, 0, 0, 0, 40, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 24, 0}, want: "image/bmp"},
		{name: "BM-prefixed text", data: []byte("BMAD method notes: keep this file as text\n"), want: ""},
		{name: "unknown", data: []byte{1, 2, 3, 4}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectSupportedImageMimeType(tt.data); got != tt.want {
				t.Fatalf("DetectSupportedImageMimeType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectSupportedImageMimeTypeFromFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sample.gif"
	if err := os.WriteFile(path, []byte("GIF89a\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectSupportedImageMimeTypeFromFile(path); got != "image/gif" {
		t.Fatalf("DetectSupportedImageMimeTypeFromFile() = %q, want %q", got, "image/gif")
	}
	if got := DetectSupportedImageMimeTypeFromFile(dir + "/missing"); got != "" {
		t.Fatalf("DetectSupportedImageMimeTypeFromFile(missing) = %q, want empty", got)
	}
}

func TestImageHasAlphaDetection(t *testing.T) {
	// Opaque RGBA → no alpha.
	opaque := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := range 10 {
		for x := range 10 {
			opaque.Set(x, y, color.RGBA{1, 2, 3, 255})
		}
	}
	if imageHasAlpha(opaque) {
		t.Errorf("fully-opaque image reported as having alpha")
	}

	// One transparent pixel → has alpha.
	withA := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := range 10 {
		for x := range 10 {
			withA.Set(x, y, color.RGBA{1, 2, 3, 255})
		}
	}
	withA.Set(5, 5, color.RGBA{1, 2, 3, 128})
	if !imageHasAlpha(withA) {
		t.Errorf("partially-transparent image not detected")
	}

	// Opaque NRGBA.
	nrgba := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	for y := range 10 {
		for x := range 10 {
			nrgba.Set(x, y, color.NRGBA{1, 2, 3, 255})
		}
	}
	if imageHasAlpha(nrgba) {
		t.Errorf("opaque NRGBA reported as having alpha")
	}
}

func makeBMPImage(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := bmp.Encode(&buf, img); err != nil {
		t.Fatalf("encode bmp: %v", err)
	}
	return buf.Bytes()
}

func TestNormalizeToolResultImagesUsesProfileAndRetainsFailures(t *testing.T) {
	data := makePNGImage(t, 80, 40, color.RGBA{255, 0, 0, 255})
	original := ai.ImageContent{Data: base64.StdEncoding.EncodeToString(data), MimeType: "image/png"}
	result := agent.AgentToolResult{Content: "read", Images: []ai.ImageContent{original}}
	got := NormalizeToolResultImagesWithOptions(result, true, &ai.ModelImageResizeOptions{MaxWidth: 20})
	if len(got.Images) != 1 || !strings.Contains(got.Content, "displayed at 20x10") {
		t.Fatalf("normalized=%#v", got)
	}
	failed := NormalizeToolResultImagesWithOptions(result, true, &ai.ModelImageResizeOptions{MaxBytes: 1})
	if len(failed.Images) != 1 || failed.Images[0] != original || failed.Content != "read" {
		t.Fatalf("failed processing did not retain original=%#v", failed)
	}
	if result.Images[0] != original || result.Content != "read" {
		t.Fatal("normalizer mutated caller")
	}
}

func TestToolImageInputLimitsAcceptNodeBase64(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(makePNGImage(t, 40, 20, color.RGBA{255, 0, 0, 255}))
	wrapped := " \t" + data[:10] + "#" + data[10:]
	result := NormalizeToolResultImagesWithOptions(agent.AgentToolResult{Content: "tool", Images: []ai.ImageContent{{Data: wrapped, MimeType: "image/png"}}}, true, &ai.ModelImageResizeOptions{MaxWidth: 10})
	if len(result.Images) != 1 || !strings.Contains(result.Content, "displayed at 10x5") {
		t.Fatalf("Node-compatible tool attachment not resized: %#v", result)
	}
	decoded, err := base64.StdEncoding.DecodeString(result.Images[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || config.Width != 10 || config.Height != 5 {
		t.Fatalf("resized dimensions=%+v err=%v", config, err)
	}
}
