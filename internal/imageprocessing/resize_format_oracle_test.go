package imageprocessing

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// formatChoiceCasesPerCategory is the size of the corpus per kind of image.
const formatChoiceCasesPerCategory = 24

type formatChoiceCase struct {
	Category string `json:"category"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
	MaxSide  int    `json:"maxSide"`
}

type formatChoiceResult struct {
	Null     bool   `json:"null"`
	MimeType string `json:"mimeType"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// realisticImage draws one picture of a kind that reaches the resizer: a photo (smooth tones, texture, grain, a few strong edges), a
// screenshot (flat panels, rules, rows of glyph-like strokes), or an interface icon sheet with transparency.
func realisticImage(rng *rand.Rand, category string, width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	set := func(x, y int, r, g, b, a float64) {
		o := img.PixOffset(x, y)
		clamp := func(v float64) uint8 { return uint8(math.Max(0, math.Min(255, math.Round(v)))) }
		img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = clamp(r), clamp(g), clamp(b), clamp(a)
	}
	switch category {
	case "photo":
		phase1, phase2, freq := rng.Float64()*6, rng.Float64()*6, 2+rng.Float64()*6
		grain := 2 + rng.Float64()*6
		for y := range height {
			for x := range width {
				fx, fy := float64(x)/float64(width), float64(y)/float64(height)
				base := 110 + 80*math.Sin(freq*fx+phase1)*math.Cos(freq*fy+phase2)
				edge := 0.0
				if (fx-0.5)*(fx-0.5)+(fy-0.45)*(fy-0.45) < 0.06 {
					edge = 45
				}
				n := (rng.Float64() - 0.5) * grain * 2
				set(x, y, base+edge+n+30*fx, base*0.9+edge+n+20*fy, base*0.8+edge+n+40*(1-fx), 255)
			}
		}
	case "screenshot":
		bg := []float64{245, 30, 255}[rng.IntN(3)]
		for y := range height {
			for x := range width {
				set(x, y, bg, bg, bg, 255)
			}
		}
		for range 10 + rng.IntN(20) {
			x0, y0, w, h := rng.IntN(width), rng.IntN(height), 40+rng.IntN(width/3), 20+rng.IntN(height/4)
			r, g, b := rng.Float64()*255, rng.Float64()*255, rng.Float64()*255
			for y := y0; y < min(height, y0+h); y++ {
				for x := x0; x < min(width, x0+w); x++ {
					set(x, y, r, g, b, 255)
				}
			}
		}
		ink := 255 - bg
		for row := 0; row < height-14; row += 18 {
			for x := 4; x < width-8; x += 7 {
				if rng.IntN(5) == 0 {
					continue
				}
				for dy := range 10 {
					for dx := range 4 {
						if rng.IntN(3) > 0 {
							set(x+dx, row+dy, ink, ink, ink, 255)
						}
					}
				}
			}
		}
	default: // "icons"
		for range 6 + rng.IntN(12) {
			cx, cy, radius := rng.IntN(width), rng.IntN(height), 15+rng.IntN(min(width, height)/5)
			r, g, b, a := rng.Float64()*255, rng.Float64()*255, rng.Float64()*255, 90+rng.Float64()*165
			for y := max(0, cy-radius); y < min(height, cy+radius); y++ {
				for x := max(0, cx-radius); x < min(width, cx+radius); x++ {
					if d := math.Hypot(float64(x-cx), float64(y-cy)); d < float64(radius) {
						set(x, y, r, g, b, a*(1-0.4*d/float64(radius)))
					}
				}
			}
		}
	}
	return img
}

// d165FormatChoice lists the cases of TestResizeFormatChoiceMatchesPiOnRealisticImages on which PiG returns the other of PNG and JPEG than Pi,
// because Go's JPEG encoder writes other sizes than Photon's (docs/parity/DIVERGENCES.md D165). The corpus is seeded: an entry is a case
// index, so a case that starts to match, or another one that stops, fails the test.
var d165FormatChoice = map[int]string{}

// TestResizeFormatChoiceMatchesPiOnRealisticImages runs image-resize-core.ts resizeImageInProcess (Photon) and ResizeImage over seeded
// photos (JPEG), screenshots and icon sheets with transparency (PNG), 100 to 2,900 pixels on a side, resized to 2,000 or to a smaller limit
// that most of them exceed. The size and the format of the result must match, the format being Pi's choice of the smaller of the PNG and the
// JPEG candidate (e7a0df27); the cases of d165FormatChoice are the cases where the two JPEG encoders make that choice differ.
func TestResizeFormatChoiceMatchesPiOnRealisticImages(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261130, 11))
	var cases []formatChoiceCase
	for _, category := range []string{"photo", "screenshot", "icons"} {
		for range formatChoiceCasesPerCategory {
			width, height := 100+rng.IntN(2800), 100+rng.IntN(1800)
			if rng.IntN(3) == 0 {
				width, height = height, width
			}
			img := realisticImage(rng, category, width, height)
			var encoded bytes.Buffer
			mimeType := "image/png"
			if category == "photo" {
				mimeType = "image/jpeg"
				if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 90}); err != nil {
					t.Fatal(err)
				}
			} else if err := png.Encode(&encoded, img); err != nil {
				t.Fatal(err)
			}
			maxSide := []int{2000, 2000, 1024, 640}[rng.IntN(4)]
			cases = append(cases, formatChoiceCase{Category: category, Data: base64.StdEncoding.EncodeToString(encoded.Bytes()), MimeType: mimeType, MaxSide: maxSide})
		}
	}
	var want []formatChoiceResult
	pioracle.Run(t, `
const { resizeImageInProcess } = await load("pi-coding-agent/utils/image-resize-core.js");
const out = [];
for (const c of input) {
	const result = await resizeImageInProcess(new Uint8Array(Buffer.from(c.data, "base64")), c.mimeType, { maxWidth: c.maxSide, maxHeight: c.maxSide });
	out.push(result === null ? { null: true } : { null: false, mimeType: result.mimeType, width: result.width, height: result.height });
}
emit(out);`, cases, &want)

	mismatches := map[string][2]int{} // category: cases, format differences
	for i, c := range cases {
		got, err := ResizeImage(decodeBase64(t, c.Data), c.MimeType, &ai.ModelImageResizeOptions{MaxWidth: c.MaxSide, MaxHeight: c.MaxSide})
		gotResult := formatChoiceResult{Null: err != nil}
		if err == nil {
			gotResult = formatChoiceResult{MimeType: got.MimeType, Width: got.Width, Height: got.Height}
		}
		counts := mismatches[c.Category]
		counts[0]++
		if gotResult != want[i] {
			counts[1]++
			if _, cited := d165FormatChoice[i]; !cited {
				t.Errorf("case %d (%s, %s, limit %d)\n Go %+v\n Pi %+v", i, c.Category, c.MimeType, c.MaxSide, gotResult, want[i])
			}
		} else if reason, cited := d165FormatChoice[i]; cited {
			t.Errorf("case %d now matches Pi: remove it from d165FormatChoice (%s)", i, reason)
		}
		mismatches[c.Category] = counts
	}
	for _, category := range []string{"photo", "screenshot", "icons"} {
		t.Logf("%s: %d cases, %d differ from Pi", category, mismatches[category][0], mismatches[category][1])
	}
}
