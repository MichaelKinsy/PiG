package images_test

import (
	"encoding/base64"
	"image"
	"math"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/tools"
)

// The processor is native Go, but its decoders, its resample and both of its encoders are ports of Photon's, so the prepared
// image is Photon's, byte for byte. This is the differential that locks it: for the
// same input and limits the prepared image has Photon's format, dimensions and resized record, and Photon 0.3.3's own bytes.
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
func TestPreparedImagesAreWhatPhotonProduces(t *testing.T) {
	source, err := os.ReadFile("testdata/scene.png")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, fixture, mime string
		// convertedFrom is set when the PNG is too large for the byte limit and becomes a JPEG.
		convertedFrom string
	}{
		{"png", "testdata/scene-photon-300x200.png", "image/png", ""},
		{"jpeg", "testdata/scene-photon-300x200-q80.jpg", "image/jpeg", "image/png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			maxBytes := tools.DEFAULT_IMAGE_LIMITS.MaxBytes
			if tc.mime == "image/jpeg" {
				// One byte under the PNG's base64, so the PNG does not fit and the first JPEG quality does.
				png := prepare(t, source, "image/png", tools.ImageLimits{MaxWidth: 300, MaxHeight: 300, MaxBytes: maxBytes})
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
			if want := base64.StdEncoding.EncodeToString(photonBytes); got.Data != want {
				t.Fatalf("prepared %d base64 bytes, Photon's are %d", len(got.Data), len(want))
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
