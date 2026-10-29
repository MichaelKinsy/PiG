package imageprocessing

import (
	"bytes"
	"image"
	"strings"
	"testing"
)

// encoderTieBound is how far apart PiG's PNG and JPEG candidates may be for the choice between them to count as undecided by the
// encoders: Photon's encoders (the image crate: no chroma subsampling in JPEG, its own deflate for PNG) and Go's differ by tens of
// percent in size, so when the two candidates are within this factor of each other, which one is smaller depends on the encoder.
const encoderTieBound = 1.5

// pngJPEGRatio is the size of the PNG candidate over the size of the JPEG candidate (at quality) that PiG makes of the image in raw
// resized to width x height: below one the PNG is smaller. ok is false when raw does not decode.
func pngJPEGRatio(t *testing.T, raw []byte, width, height, quality int) (ratio float64, ok bool) {
	t.Helper()
	src, err := decodeAutoOriented(raw)
	if err != nil || width < 1 || height < 1 {
		return 0, false
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
	return float64(len(pngBytes)) / float64(len(jpegBytes)), true
}

// outputSize is the pixel size of the image PiG returned.
func outputSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return config.Width, config.Height
}

// encoderTie reports whether PiG and Pi may differ on PNG against JPEG for the image raw resized to width x height: both chose one of
// the two formats and the two candidates are within encoderTieBound of each other.
func encoderTie(t *testing.T, raw []byte, width, height, quality int, goMIME, piMIME string) bool {
	t.Helper()
	pair := func(mime string) bool { return mime == "image/png" || mime == "image/jpeg" }
	if goMIME == piMIME || !pair(goMIME) || !pair(piMIME) {
		return false
	}
	ratio, ok := pngJPEGRatio(t, raw, width, height, quality)
	return ok && ratio >= 1/encoderTieBound && ratio <= encoderTieBound
}

// hintsWithoutFormat compares conversion hints of an encoder tie: the hint names the format the image ended up in.
func hintsWithoutFormat(hints string) string {
	return strings.NewReplacer("image/jpeg", "image/*", "image/png", "image/*").Replace(hints)
}

// jpegQualityOf is the JPEG quality a resize uses: the option, or the default when it is zero.
func jpegQualityOf(quality int) int {
	if quality == 0 {
		return JPEGQuality
	}
	return quality
}
