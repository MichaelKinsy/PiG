package images

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"testing"
)

// encodePhotonPNG writes the bytes Photon 0.3.3's `get_bytes()` writes (png 0.17.14 Compression::Fast, adaptive filters,
// fdeflate 0.3.6 with the stored fallback). testdata/scene-photon-300x200.png is Photon's own `get_bytes()` (see
// photon_differential_test.go): its pixels encode to it byte for byte. The others are the PNGs Photon wrote for the pixels of
// packages/durable/test/tools-images.test.ts, recorded as their length and SHA-256 with Node and @silvia-odwyer/photon 0.3.3:
//
//	const image = new PhotonImage(rgba, width, height); const png = image.get_bytes();
//	console.log(png.length, createHash("sha256").update(png).digest("hex"));
func TestEncodePhotonPNGWritesPhotonsBytes(t *testing.T) {
	fixture, err := os.ReadFile("testdata/scene-photon-300x200.png")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	scene := image.NewNRGBA(decoded.Bounds())
	draw.Draw(scene, scene.Rect, decoded, image.Point{}, draw.Src)
	if got := encodePhotonPNG(scene); !bytes.Equal(got, fixture) {
		t.Errorf("scene: %d bytes, want Photon's %d", len(got), len(fixture))
	}

	// pixels is tools-images.test.ts pixels: red, with a green top-left pixel.
	pixels := func(width, height int) *image.NRGBA {
		img := image.NewNRGBA(image.Rect(0, 0, width, height))
		for i := 0; i < len(img.Pix); i += 4 {
			copy(img.Pix[i:], []byte{255, 0, 0, 255})
		}
		copy(img.Pix, []byte{0, 255, 0, 255})
		return img
	}
	// noisy is tools-images.test.ts noisyPng's pixels, its generator in JavaScript doubles: Photon stores them uncompressed.
	noisy := func(width, height int) *image.NRGBA {
		img := image.NewNRGBA(image.Rect(0, 0, width, height))
		seed := 1.0
		for index := range img.Pix {
			seed = math.Mod(float64(seed*1103515245)+12345, 4294967296)
			img.Pix[index] = byte(uint32(seed) >> 24)
			if index%4 == 3 {
				img.Pix[index] = 255
			}
		}
		return img
	}
	for _, tc := range []struct {
		name   string
		img    *image.NRGBA
		length int
		sha256 string
	}{
		{"pixels 8x6", pixels(8, 6), 137, "574f50c515b65dbe0f1ea3aa4d9ea9a04efa3749d51531713f2b34d2dba81a1e"},
		{"pixels 4000x1000", pixels(4000, 1000), 79244, "4b206cb1a02ee262bcedba224d02245bb39f4b24aea72afe9dc84f36ebb0301e"},
		{"noisy 1100x1100", noisy(1100, 1100), 4841533, "0ff500396af0d58acc92d2f9c308d18a9afafb15850fca7f3bf59f9e3bdb09c7"},
	} {
		got := encodePhotonPNG(tc.img)
		sum := sha256.Sum256(got)
		if len(got) != tc.length || hex.EncodeToString(sum[:]) != tc.sha256 {
			t.Errorf("%s: %d bytes, SHA-256 %x; want Photon's %d bytes, %s", tc.name, len(got), sum, tc.length, tc.sha256)
		}
	}
}
