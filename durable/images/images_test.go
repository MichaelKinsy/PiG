package images_test

// pi: packages/durable/src/images/exif.ts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	_ "golang.org/x/image/bmp"

	"github.com/MichaelKinsy/PiG/durable/images"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

// The cases of packages/durable/test/tools-images.test.ts that exercise the processor, "read of images, with the Photon image
// processor" and "EXIF orientation". Photon's encoders are not reproduced, so the assertions are on what the model gets:
// format, dimensions, the resized and convertedFrom records, the limits, and pixels.

type rgb [3]uint8

func rgbaOf(width, height int, set func(x, y int) color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, set(x, y))
		}
	}
	return img
}

// pixels is a width by height image: red, with a green top-left pixel.
func pixels(width, height int) *image.NRGBA {
	return rgbaOf(width, height, func(x, y int) color.NRGBA {
		if x == 0 && y == 0 {
			return color.NRGBA{0, 255, 0, 255}
		}
		return color.NRGBA{255, 0, 0, 255}
	})
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// noisyPNG is noise, which neither PNG nor JPEG compresses well, so the byte limit decides.
func noisyPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	seed := uint32(1)
	return encodePNG(t, rgbaOf(width, height, func(_, _ int) color.NRGBA {
		var c [3]uint8
		for i := range c {
			seed = seed*1103515245 + 12345
			c[i] = uint8(seed >> 24)
		}
		return color.NRGBA{c[0], c[1], c[2], 255}
	}))
}

// bmp is a 24-bit BMP, which providers do not take inline.
func bmp(width, height int) []byte {
	row := (width*3 + 3) / 4 * 4
	out := make([]byte, 54+row*height)
	copy(out, "BM")
	binary.LittleEndian.PutUint32(out[2:], uint32(len(out)))
	binary.LittleEndian.PutUint32(out[10:], 54)
	binary.LittleEndian.PutUint32(out[14:], 40)
	binary.LittleEndian.PutUint32(out[18:], uint32(width))
	binary.LittleEndian.PutUint32(out[22:], uint32(height))
	binary.LittleEndian.PutUint16(out[26:], 1)
	binary.LittleEndian.PutUint16(out[28:], 24)
	binary.LittleEndian.PutUint32(out[34:], uint32(row*height))
	for y := range height {
		for x := range width {
			copy(out[54+y*row+x*3:], []byte{0, 0, 255})
		}
	}
	return out
}

const oneByOneGIF = "R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"

func gif() []byte {
	data, _ := base64.StdEncoding.DecodeString(oneByOneGIF)
	return data
}

// tiff is a TIFF header whose first directory holds only Orientation = orientation.
func tiff(orientation int, little bool) []byte {
	order := binary.ByteOrder(binary.BigEndian)
	out := []byte{0x4d, 0x4d}
	if little {
		order, out = binary.LittleEndian, []byte{0x49, 0x49}
	}
	out = append(out, make([]byte, 24)...)
	order.PutUint16(out[2:], 42)
	order.PutUint32(out[4:], 8)
	order.PutUint16(out[8:], 1)
	order.PutUint16(out[10:], 0x0112)
	order.PutUint16(out[12:], 3)
	order.PutUint32(out[14:], 1)
	order.PutUint16(out[18:], uint16(orientation))
	return out
}

var exifHeader = []byte{'E', 'x', 'i', 'f', 0, 0}

// withOrientation is jpeg with an APP1 segment whose EXIF says orientation, after an APP0 segment when app0.
func withOrientation(jpegBytes []byte, orientation int, little, app0 bool) []byte {
	exif := append(append([]byte{}, exifHeader...), tiff(orientation, little)...)
	app1 := append([]byte{0xff, 0xe1, byte((len(exif) + 2) >> 8), byte((len(exif) + 2) & 0xff)}, exif...)
	var segment0 []byte
	if app0 {
		segment0 = []byte{0xff, 0xe0, 0, 16, 0x4a, 0x46, 0x49, 0x46, 0, 1, 1, 0, 0, 1, 0, 1, 0, 0}
	}
	out := append([]byte{}, jpegBytes[:2]...)
	out = append(out, segment0...)
	out = append(out, app1...)
	return append(out, jpegBytes[2:]...)
}

func decode(t *testing.T, data string) image.Image {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the prepared image does not decode: %v", err)
	}
	return img
}

func prepare(t *testing.T, data []byte, mime string, limits tools.ImageLimits) *tools.PreparedImage {
	t.Helper()
	prepared, err := images.CreatePhotonImages().Prepare(context.Background(), data, mime, limits)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func TestAnImageThatNeedsNoChangeIsReturnedAsItIs(t *testing.T) {
	data := encodePNG(t, pixels(8, 6))
	got := prepare(t, data, "image/png", tools.DEFAULT_IMAGE_LIMITS)
	if got == nil || got.Data != base64.StdEncoding.EncodeToString(data) || got.MimeType != "image/png" || got.Resized != nil || got.ConvertedFrom != "" {
		t.Fatalf("prepared = %+v", got)
	}
	if got := prepare(t, gif(), "image/gif", tools.DEFAULT_IMAGE_LIMITS); got == nil || got.MimeType != "image/gif" || got.Data != oneByOneGIF {
		t.Fatalf("gif = %+v", got)
	}
}

func TestAnImageWiderThanTheLimitIsScaledDownAndTheResizeIsRecorded(t *testing.T) {
	got := prepare(t, encodePNG(t, pixels(4000, 1000)), "image/png", tools.DEFAULT_IMAGE_LIMITS)
	if got == nil || got.Resized == nil || *got.Resized != (tools.ImageResize{From: tools.ImageSize{Width: 4000, Height: 1000}, To: tools.ImageSize{Width: 2000, Height: 500}}) {
		t.Fatalf("prepared = %+v", got)
	}
	if b := decode(t, got.Data).Bounds(); b.Dx() != 2000 || b.Dy() != 500 {
		t.Fatalf("decoded size = %v", b)
	}
}

func TestAPNGTooLargeForTheByteLimitBecomesAJPEGAtItsSizeThenShrinks(t *testing.T) {
	got := prepare(t, noisyPNG(t, 1100, 1100), "image/png", tools.DEFAULT_IMAGE_LIMITS)
	if got == nil || got.MimeType != "image/jpeg" || got.ConvertedFrom != "image/png" || got.Resized != nil {
		t.Fatalf("prepared = %+v", got)
	}
	if len(got.Data) > tools.DEFAULT_IMAGE_LIMITS.MaxBytes {
		t.Fatalf("base64 length %d exceeds the limit", len(got.Data))
	}
	if b := decode(t, got.Data).Bounds(); b.Dx() != 1100 || b.Dy() != 1100 {
		t.Fatalf("decoded size = %v", b)
	}
	tight := tools.DEFAULT_IMAGE_LIMITS
	tight.MaxBytes = 200_000
	shrunk := prepare(t, noisyPNG(t, 2000, 2000), "image/png", tight)
	if shrunk == nil || len(shrunk.Data) > tight.MaxBytes || shrunk.Resized == nil || shrunk.Resized.From != (tools.ImageSize{Width: 2000, Height: 2000}) || shrunk.Resized.To.Width >= 2000 {
		t.Fatalf("shrunk = %+v", shrunk)
	}
	// Each retry takes 3/4 of the side, rounded down, from the size before it.
	onChain := false
	for side := 2000; side >= 1; side = side * 3 / 4 {
		if shrunk.Resized.To == (tools.ImageSize{Width: side, Height: side}) {
			onChain = true
		}
	}
	if !onChain {
		t.Fatalf("the shrunk size %+v is not 2000 reduced by 3/4 steps", shrunk.Resized.To)
	}
}

func TestAnExtremeAspectRatioKeepsAtLeastOnePixelOnEachSide(t *testing.T) {
	got := prepare(t, encodePNG(t, pixels(6000, 2)), "image/png", tools.DEFAULT_IMAGE_LIMITS)
	if got == nil || got.Resized == nil || *got.Resized != (tools.ImageResize{From: tools.ImageSize{Width: 6000, Height: 2}, To: tools.ImageSize{Width: 2000, Height: 1}}) {
		t.Fatalf("prepared = %+v", got)
	}
}

func TestPrepareGivesUpWhenNotEvenOnePixelFitsTheByteLimit(t *testing.T) {
	limits := tools.DEFAULT_IMAGE_LIMITS
	limits.MaxBytes = 8
	if got := prepare(t, encodePNG(t, pixels(4, 4)), "image/png", limits); got != nil {
		t.Fatalf("prepared = %+v, want none", got)
	}
}

func TestAJPEGThatMustBeReEncodedStaysAJPEG(t *testing.T) {
	got := prepare(t, withOrientation(encodeJPEG(t, pixels(40, 30)), 3, false, false), "image/jpeg", tools.DEFAULT_IMAGE_LIMITS)
	if got == nil || got.MimeType != "image/jpeg" || got.ConvertedFrom != "" {
		t.Fatalf("prepared = %+v", got)
	}
}

func TestAFormatProvidersDoNotTakeInlineBecomesPNG(t *testing.T) {
	got := prepare(t, bmp(4, 4), "image/bmp", tools.DEFAULT_IMAGE_LIMITS)
	if got == nil || got.MimeType != "image/png" || got.ConvertedFrom != "image/bmp" {
		t.Fatalf("prepared = %+v", got)
	}
}

func TestBytesThatCannotBeDecodedAreNotAnImage(t *testing.T) {
	broken := encodePNG(t, pixels(4, 4))[:40]
	if got := prepare(t, broken, "image/png", tools.DEFAULT_IMAGE_LIMITS); got != nil {
		t.Fatalf("prepared = %+v, want none", got)
	}
}

func TestAnImageThatDecodesIsCheckedBeforeItIsReturnedAsItIs(t *testing.T) {
	// A damaged file is an error, not an image the provider rejects: the header says 1x1 but there is no pixel data.
	data := gif()[:20]
	if got := prepare(t, data, "image/gif", tools.DEFAULT_IMAGE_LIMITS); got != nil {
		t.Fatalf("prepared = %+v, want none", got)
	}
}

func TestPrepareStopsWhenItsContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := images.CreatePhotonImages().Prepare(ctx, noisyPNG(t, 300, 300), "image/png", tools.ImageLimits{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 1000}); err == nil {
		t.Fatal("a cancelled context did not stop the preparation")
	}
}

func TestEXIFOrientationTurnsTheImageUpright(t *testing.T) {
	R, G, B, W, K, Y := rgb{255, 0, 0}, rgb{0, 255, 0}, rgb{0, 0, 255}, rgb{255, 255, 255}, rgb{0, 0, 0}, rgb{255, 255, 0}
	stored := [][]rgb{{R, G, B}, {W, K, Y}}
	mirror := func(grid [][]rgb) [][]rgb {
		out := make([][]rgb, len(grid))
		for i, row := range grid {
			out[i] = make([]rgb, len(row))
			for j := range row {
				out[i][len(row)-1-j] = row[j]
			}
		}
		return out
	}
	// A quarter turn clockwise: the left column, read bottom to top, becomes the top row.
	turn := func(grid [][]rgb) [][]rgb {
		out := make([][]rgb, len(grid[0]))
		for x := range grid[0] {
			out[x] = make([]rgb, len(grid))
			for y := range grid {
				out[x][len(grid)-1-y] = grid[y][x]
			}
		}
		return out
	}
	upright := map[int][][]rgb{
		1: stored,
		2: mirror(stored),
		3: turn(turn(stored)),
		4: turn(turn(mirror(stored))),
		5: turn(turn(turn(mirror(stored)))),
		6: turn(stored),
		7: turn(mirror(stored)),
		8: turn(turn(turn(stored))),
	}
	const block = 16
	source := rgbaOf(len(stored[0])*block, len(stored)*block, func(x, y int) color.NRGBA {
		c := stored[y/block][x/block]
		return color.NRGBA{c[0], c[1], c[2], 255}
	})
	for orientation := 1; orientation <= 8; orientation++ {
		got := prepare(t, withOrientation(encodeJPEG(t, source), orientation, false, false), "image/jpeg", tools.DEFAULT_IMAGE_LIMITS)
		if got == nil {
			t.Fatalf("orientation %d: no image", orientation)
		}
		img := decode(t, got.Data)
		expected := upright[orientation]
		if b := img.Bounds(); b.Dx() != len(expected[0])*block || b.Dy() != len(expected)*block {
			t.Fatalf("orientation %d: size %v", orientation, b)
		}
		for by, row := range expected {
			for bx, want := range row {
				r, g, b, _ := img.At(bx*block+block/2, by*block+block/2).RGBA()
				round := func(channel uint32) uint8 {
					if channel>>8 > 127 {
						return 255
					}
					return 0
				}
				if seen := (rgb{round(r), round(g), round(b)}); seen != want {
					t.Errorf("orientation %d block (%d,%d) = %v, want %v", orientation, bx, by, seen, want)
				}
			}
		}
	}
}

func TestExifOrientationReadsJPEGAndWebP(t *testing.T) {
	jpegBytes := encodeJPEG(t, pixels(2, 2))
	for name, tc := range map[string]struct {
		data []byte
		want int
	}{
		"big endian":           {withOrientation(jpegBytes, 6, false, false), 6},
		"little endian":        {withOrientation(jpegBytes, 8, true, false), 8},
		"after an APP0":        {withOrientation(jpegBytes, 5, false, true), 5},
		"out of range":         {withOrientation(jpegBytes, 9, false, false), 1},
		"absent":               {jpegBytes, 1},
		"png":                  {encodePNG(t, pixels(2, 2)), 1},
		"webp without prefix":  {webpWithOrientation(6, false), 6},
		"webp with the prefix": {webpWithOrientation(3, true), 3},
	} {
		if got := images.ExifOrientation(tc.data); got != tc.want {
			t.Errorf("%s: orientation = %d, want %d", name, got, tc.want)
		}
	}
}

// webpWithOrientation is a RIFF WebP with a dummy "VP8 " chunk and an EXIF chunk saying orientation, its TIFF data bare or
// prefixed.
func webpWithOrientation(orientation int, prefixed bool) []byte {
	chunk := func(kind string, data []byte) []byte {
		out := append([]byte(kind), byte(len(data)), byte(len(data)>>8), 0, 0)
		out = append(out, data...)
		if len(data)%2 == 1 {
			out = append(out, 0)
		}
		return out
	}
	var exif []byte
	if prefixed {
		exif = append(exif, exifHeader...)
	}
	exif = append(exif, tiff(orientation, true)...)
	body := append([]byte("WEBP"), chunk("VP8 ", []byte{1, 2, 3})...)
	body = append(body, chunk("EXIF", exif)...)
	return append(append([]byte("RIFF"), byte(len(body)), byte(len(body)>>8), 0, 0), body...)
}

func TestCodingToolsWithTheProcessorReadABMP(t *testing.T) {
	extension := tools.CreateCodingTools(&tools.ReadToolOptions{Images: images.CreatePhotonImages()})
	if got := extension.Tools[0].Description; !bytes.Contains([]byte(got), []byte("(jpg, png, gif, webp, bmp)")) {
		t.Fatalf("read description = %q", got)
	}
}
