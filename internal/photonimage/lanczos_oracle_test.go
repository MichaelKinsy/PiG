package photonimage

import (
	"bytes"
	"encoding/base64"
	"image"
	"math/rand/v2"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type resizeOracleCase struct {
	Pixels string `json:"pixels"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	ToW    int    `json:"toWidth"`
	ToH    int    `json:"toHeight"`
}

// TestResizeLanczos3MatchesPhoton runs Photon's `resize(image, w, h, SamplingFilter.Lanczos3)` (@silvia-odwyer/photon under Node, as Pi
// loads it) and ResizeLanczos3 over 300 images and sizes and compares every byte of the result: noise, ramps (where the weighted sums fall
// on rounding ties), flat areas, alpha, shrinking and enlarging, equal sizes, one-pixel sides.
func TestResizeLanczos3MatchesPhoton(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261129, 3))
	side := func() int {
		switch rng.IntN(4) {
		case 0:
			return 1 + rng.IntN(4)
		case 1:
			return 1 + rng.IntN(300)
		}
		return 20 + rng.IntN(120)
	}
	var cases []resizeOracleCase
	var sources []*image.NRGBA
	for i := range 300 {
		w, h := side(), side()
		toW, toH := side(), side()
		switch i % 7 {
		case 0:
			toW, toH = w, h
		case 1:
			toW, toH = max(1, w/(1+rng.IntN(4))), max(1, h/(1+rng.IntN(4)))
		}
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			for x := range w {
				o := img.PixOffset(x, y)
				switch i % 5 {
				case 0: // noise
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = byte(rng.IntN(256)), byte(rng.IntN(256)), byte(rng.IntN(256)), 255
				case 1: // vertical ramp: constant rows
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = byte(y), byte(y), byte(y), 255
				case 2: // diagonal ramp with alpha
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = byte(x+y), byte(2*x), byte(3*y), byte(255-x)
				case 3: // flat blocks
					v := byte(60 * ((x/7 + y/5) % 4))
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = v, 255-v, 128, 255
				default: // noise with alpha
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = byte(rng.IntN(256)), byte(rng.IntN(256)), byte(rng.IntN(256)), byte(rng.IntN(256))
				}
			}
		}
		sources = append(sources, img)
		cases = append(cases, resizeOracleCase{Pixels: base64.StdEncoding.EncodeToString(img.Pix), Width: w, Height: h, ToW: toW, ToH: toH})
	}
	var want []string
	pioracle.Run(t, `
const { loadPhoton } = await load("pi-coding-agent/utils/photon.js");
const photon = await loadPhoton();
const out = [];
for (const c of input) {
	const image = new photon.PhotonImage(new Uint8Array(Buffer.from(c.pixels, "base64")), c.width, c.height);
	const resized = photon.resize(image, c.toWidth, c.toHeight, photon.SamplingFilter.Lanczos3);
	out.push(Buffer.from(resized.get_raw_pixels()).toString("base64"));
	resized.free();
	image.free();
}
emit(out);`, cases, &want)
	failures := 0
	for i, c := range cases {
		expected, err := base64.StdEncoding.DecodeString(want[i])
		if err != nil {
			t.Fatal(err)
		}
		got := ResizeLanczos3(sources[i], c.ToW, c.ToH)
		if !bytes.Equal(got.Pix, expected) {
			failures++
			differing := 0
			for j := range expected {
				if j < len(got.Pix) && got.Pix[j] != expected[j] {
					differing++
				}
			}
			if failures <= 8 {
				t.Errorf("case %d (%dx%d to %dx%d, pattern %d): %d of %d bytes differ", i, c.Width, c.Height, c.ToW, c.ToH, i%5, differing, len(expected))
			}
		}
	}
	t.Logf("%d cases, %d differ from Photon", len(cases), failures)
}
