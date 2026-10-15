package images_test

import (
	"encoding/base64"
	"image"
	"math"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/tools"
)

// D165: the processor is native Go, so its encoded bytes and resampled pixels are not Photon's. These tests are the differential
// that locks "behavior identical where observable": for the same input and limits, the prepared image has Photon's format and
// dimensions, the same resized record, and pixels within a small mean error of what Pi's Photon 0.3.3 produced.
//
// testdata/scene.png is a 600x400 synthetic scene (gradients, a disc, a diagonal stripe, a white grid: edges to resample).
// testdata/scene-photon-300x200.png and testdata/scene-photon-300x200-q80.jpg are `get_bytes()` and `get_bytes_jpeg(80)` of
// Pi's `resize(image, 300, 200, SamplingFilter.Lanczos3)` of it, produced with Node and @silvia-odwyer/photon 0.3.3 (the version
// packages/durable pins):
//
//	initSync({ module: readFileSync("photon_rs_bg.wasm") });
//	const scaled = resize(PhotonImage.new_from_byteslice(readFileSync("scene.png")), 300, 200, SamplingFilter.Lanczos3);
//	writeFileSync("scene-photon-300x200.png", scaled.get_bytes());
//	writeFileSync("scene-photon-300x200-q80.jpg", scaled.get_bytes_jpeg(80));
func TestPreparedPixelsStayCloseToWhatPhotonProduces(t *testing.T) {
	source, err := os.ReadFile("testdata/scene.png")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, fixture, mime string
		maxBytes            int
		// maxError is the mean absolute channel error allowed, out of 255: resampling alone for PNG, resampling plus two
		// different JPEG encoders for JPEG.
		maxError float64
		// convertedFrom is set when the PNG is too large for the byte limit and becomes a JPEG.
		convertedFrom string
	}{
		{"png", "testdata/scene-photon-300x200.png", "image/png", tools.DEFAULT_IMAGE_LIMITS.MaxBytes, 1.0, ""},
		{"jpeg", "testdata/scene-photon-300x200-q80.jpg", "image/jpeg", tools.DEFAULT_IMAGE_LIMITS.MaxBytes, 4.5, "image/png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			maxBytes := tc.maxBytes
			if tc.mime == "image/jpeg" {
				// One byte under the PNG's base64, so the PNG does not fit and the first JPEG quality does.
				png := prepare(t, source, "image/png", tools.ImageLimits{MaxWidth: 300, MaxHeight: 300, MaxBytes: tc.maxBytes})
				maxBytes = len(png.Data) - 1
			}
			limits := tools.ImageLimits{MaxWidth: 300, MaxHeight: 300, MaxBytes: maxBytes}
			got := prepare(t, source, "image/png", limits)
			if got == nil || got.MimeType != tc.mime || got.ConvertedFrom != tc.convertedFrom || got.Resized == nil ||
				*got.Resized != (tools.ImageResize{From: tools.ImageSize{Width: 600, Height: 400}, To: tools.ImageSize{Width: 300, Height: 200}}) {
				t.Fatalf("prepared = %+v", got)
			}
			photonBytes, err := os.ReadFile(tc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			ours := decode(t, got.Data)
			theirs := decode(t, base64.StdEncoding.EncodeToString(photonBytes))
			if ours.Bounds() != theirs.Bounds() {
				t.Fatalf("size %v, Photon %v", ours.Bounds(), theirs.Bounds())
			}
			mean := meanChannelError(ours, theirs)
			t.Logf("mean absolute channel error against Photon: %.3f of 255", mean)
			if mean > tc.maxError {
				t.Fatalf("mean absolute channel error %.3f against Photon exceeds %.1f", mean, tc.maxError)
			}
		})
	}
}

func meanChannelError(a, b image.Image) float64 {
	var sum float64
	bounds := a.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r1, g1, b1, _ := a.At(x, y).RGBA()
			r2, g2, b2, _ := b.At(x, y).RGBA()
			sum += math.Abs(float64(r1>>8)-float64(r2>>8)) + math.Abs(float64(g1>>8)-float64(g2>>8)) + math.Abs(float64(b1>>8)-float64(b2>>8))
		}
	}
	return sum / float64(bounds.Dx()*bounds.Dy()*3)
}
