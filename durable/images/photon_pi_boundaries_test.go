package images_test

// pi: packages/durable/src/images/index.ts

import (
	"encoding/base64"
	"image"
	"image/color"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/tools"
)

// Pi's index.ts decides, for each image, whether it passes through, how it is turned, what size it is given, which
// encoding fits and how far it shrinks. photon_pi_oracle_test.go compares PiG with Pi (the real createPhotonImages,
// Photon 0.3.3 under Node) on a corpus of ordinary images. The cases here sit on the boundaries of those decisions:
// a size or byte count exactly at its limit, a size that rounds at one half, a size that rounds to zero, an image of
// one pixel across, a shrink that must keep going past the first step, and each JPEG quality of the ladder. Every
// expectation is Pi's own answer for the same bytes and limits.

func TestPhotonImagesMatchPiAtTheLimitBoundaries(t *testing.T) {
	def := tools.DEFAULT_IMAGE_LIMITS
	pngOf := func(width, height int) []byte { return encodePNG(t, pixels(width, height)) }
	exactBytes := func(data []byte) tools.ImageLimits {
		limits := def
		limits.MaxBytes = int(tools.Base64Length(int64(len(data))))
		return limits
	}
	jpegSource := encodeJPEG(t, pixels(64, 64))
	photoPNG := pngOf(64, 64)
	corpus := []oracleCase{
		// An image exactly at a limit is within it: Pi returns the bytes as they are.
		{name: "png exactly as wide as the limit", data: pngOf(2000, 100), mime: "image/png", limits: def},
		{name: "png exactly as tall as the limit", data: pngOf(100, 2000), mime: "image/png", limits: def},
		{name: "png exactly at the byte limit", data: photoPNG, mime: "image/png", limits: exactBytes(photoPNG)},
		{name: "jpeg exactly at the byte limit", data: jpegSource, mime: "image/jpeg", limits: exactBytes(jpegSource)},
		{name: "gif exactly at the byte limit", data: gif(), mime: "image/gif", limits: exactBytes(gif())},
		// One past a limit is not.
		{name: "png one pixel wider than the limit", data: pngOf(2001, 100), mime: "image/png", limits: def, tolerance: 2},
		{name: "png one pixel taller than the limit", data: pngOf(100, 2001), mime: "image/png", limits: def, tolerance: 2},
		// A size that rounds half up (4000x1001 at scale 1/2 is 500.5 high) and sizes that round down.
		{name: "png whose resized height is a half", data: pngOf(4000, 1001), mime: "image/png", limits: def, tolerance: 2},
		{name: "png whose resized height is just under a half", data: pngOf(3001, 1000), mime: "image/png", limits: def, tolerance: 2},
		{name: "png whose resized height is a hair under a half", data: pngOf(2003, 334), mime: "image/png", limits: def, tolerance: 2},
		{name: "png whose resized width is just over a half", data: pngOf(1001, 4002), mime: "image/png", limits: def, tolerance: 2},
		// A side that rounds to zero is one pixel, and a side that does not change is left as it is.
		{name: "png one pixel wide and far taller than the limit", data: pngOf(1, 10000), mime: "image/png", limits: def, tolerance: 2},
		{name: "png one pixel tall and far wider than the limit", data: pngOf(10000, 1), mime: "image/png", limits: def, tolerance: 2},
		{name: "png one pixel wide and a little taller than the limit", data: pngOf(1, 3000), mime: "image/png", limits: def, tolerance: 2},
		{name: "png one pixel tall and a little wider than the limit", data: pngOf(3000, 1), mime: "image/png", limits: def, tolerance: 2},
		// A limit tighter on one axis only.
		{name: "png wide for a narrow limit and short for a tall one", data: pngOf(300, 200), mime: "image/png", limits: tools.ImageLimits{MaxWidth: 150, MaxHeight: 2000, MaxBytes: def.MaxBytes}, tolerance: 2},
		{name: "png tall for a short limit and narrow for a wide one", data: pngOf(300, 200), mime: "image/png", limits: tools.ImageLimits{MaxWidth: 2000, MaxHeight: 100, MaxBytes: def.MaxBytes}, tolerance: 2},
	}
	comparePhotonToPi(t, corpus)
}

// A byte limit that shrinks an image of one pixel across keeps shrinking, 3/4 at a time, past the first step: one pixel
// across is not yet one pixel.
func TestPhotonImagesMatchPiWhenAnImageOnePixelAcrossKeepsShrinking(t *testing.T) {
	var corpus []oracleCase
	for _, maxBytes := range []int{5600, 7600} {
		corpus = append(corpus,
			oracleCase{name: "noisy png one pixel wide", data: encodePNG(t, noisy(1, 3000)), mime: "image/png",
				limits: tools.ImageLimits{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: maxBytes}, skipPix: true},
			oracleCase{name: "noisy png one pixel tall", data: encodePNG(t, noisy(3000, 1)), mime: "image/png",
				limits: tools.ImageLimits{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: maxBytes}, skipPix: true},
		)
	}
	comparePhotonToPi(t, corpus)
}

// A JPEG that must be re-encoded is tried at quality 80, 70, 55 and 40, the first whose base64 fits. The quantization
// table in the output says which quality was used. The limits sit between the sizes both encoders give at neighbouring
// qualities, so the same quality fits in Pi and in PiG.
func TestPhotonImagesMatchPiOnTheJPEGQualityLadder(t *testing.T) {
	// Mirrored (EXIF orientation 2), so it is re-encoded rather than returned as it is.
	source := withOrientation(encodeJPEG(t, noisy(300, 300)), 2, false, false)
	var corpus []oracleCase
	for _, maxBytes := range []int{120000, 81000, 66500, 54000} {
		corpus = append(corpus, oracleCase{name: "jpeg re-encoded within " + strconv.Itoa(maxBytes) + " bytes", data: source, mime: "image/jpeg",
			limits: tools.ImageLimits{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: maxBytes}, skipPix: true, quantized: true})
	}
	comparePhotonToPi(t, corpus)
}

// The base64 of an encoding that is exactly as long as the byte limit fits. Photon's PNG is byte-for-byte PiG's, so the
// limit is the length of Pi's own PNG for the image.
func TestPhotonImagesMatchPiWhenAPNGIsExactlyAsLongAsTheByteLimit(t *testing.T) {
	def := tools.DEFAULT_IMAGE_LIMITS
	sources := []oracleCase{
		{name: "bmp", data: bmp(16, 16), mime: "image/bmp", limits: def, tolerance: 0},
		{name: "noisy bmp", data: noisyBMP(24, 24), mime: "image/bmp", limits: def, tolerance: 0},
	}
	first := askPi(t, sources)
	var corpus []oracleCase
	for i, c := range sources {
		if first[i] == nil || first[i].MimeType != "image/png" {
			t.Fatalf("%s: Pi gave %+v, want a PNG", c.name, first[i])
		}
		c.limits.MaxBytes = len(first[i].Data)
		c.name += " whose PNG is exactly the byte limit"
		corpus = append(corpus, c)
	}
	comparePhotonToPi(t, corpus)
}

// noisyBMP is a 24-bit BMP of pseudo-random pixels.
func noisyBMP(width, height int) []byte {
	out := bmp(width, height)
	row := (width*3 + 3) / 4 * 4
	seed := uint32(7)
	for y := range height {
		for x := range width * 3 {
			seed = seed*1103515245 + 12345
			out[54+y*row+x] = byte(seed >> 24)
		}
	}
	return out
}

// jpegQuantizationTable is the first quantization table (DQT) of a base64 JPEG: it tells which quality encoded it.
func jpegQuantizationTable(t *testing.T, data string) []int {
	t.Helper()
	raw := decodeBase64(t, data)
	for i := 2; i+4 <= len(raw) && raw[i] == 0xff; {
		length := int(raw[i+2])<<8 | int(raw[i+3])
		if raw[i+1] == 0xdb {
			table := make([]int, 0, 64)
			// The segment is the length, one byte of precision and table number, then 64 values.
			for _, v := range raw[i+5 : i+5+min(64, length-3)] {
				table = append(table, int(v))
			}
			return table
		}
		i += 2 + length
	}
	t.Fatal("no quantization table")
	return nil
}

func decodeBase64(t *testing.T, data string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// noisy is a width by height image of pseudo-random pixels, which neither PNG nor JPEG compresses well.
func noisy(width, height int) *image.NRGBA {
	seed := uint32(1)
	return rgbaOf(width, height, func(_, _ int) color.NRGBA {
		var c [3]uint8
		for i := range c {
			seed = seed*1103515245 + 12345
			c[i] = uint8(seed >> 24)
		}
		return color.NRGBA{c[0], c[1], c[2], 255}
	})
}
