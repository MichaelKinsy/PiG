package imageprocessing

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type resizeStrategyCase struct {
	Data      string `json:"data"`
	MimeType  string `json:"mimeType"`
	MaxWidth  int    `json:"maxWidth,omitempty"`
	MaxHeight int    `json:"maxHeight,omitempty"`
	MaxBytes  int    `json:"maxBytes,omitempty"`
	Quality   int    `json:"jpegQuality,omitempty"`
}

type resizeStrategyResult struct {
	Null           bool   `json:"null"`
	Data           string `json:"data"`
	MimeType       string `json:"mimeType"`
	OriginalWidth  int    `json:"originalWidth"`
	OriginalHeight int    `json:"originalHeight"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	WasResized     bool   `json:"wasResized"`
}

// TestResizeImageDimensionStrategyMatchesPi runs image-resize-core.ts resizeImageInProcess (Photon) and PiG's ResizeImage over 200 seeded PNGs and option sets. Dimensions from 1 to
// 2,600 (narrow strips, squares, images just over or under each limit), maxWidth and maxHeight from 1 to 3,000, the default and a very large maxBytes, a one-byte maxBytes that nothing
// satisfies, and an unset or explicit mime type. The original and resulting size, whether the image was resized, whether Pi returns null, and the mime type must match, and the
// returned image must decode to the size it reports. Not compared: the encoded bytes (Photon's PNG/JPEG encoders differ from Go's) and which candidate wins a tight maxBytes, and the 0.75 shrink steps (reached only when every candidate misses maxBytes), which depend on those encoders.
func TestResizeImageDimensionStrategyMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261125, 7))
	pickInt := func(xs ...int) int { return xs[rng.IntN(len(xs))] }
	dim := func() int {
		switch rng.IntN(4) {
		case 0:
			return pickInt(1, 2, 3, 7, 100, 1999, 2000, 2001, 2599)
		case 1:
			return 1 + rng.IntN(2600)
		}
		return 50 + rng.IntN(300)
	}
	var cases []resizeStrategyCase
	for range 200 {
		width, height := dim(), dim()
		img := image.NewNRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y += 1 + height/40 {
			for x := 0; x < width; x += 1 + width/40 {
				img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255})
			}
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, img); err != nil {
			t.Fatal(err)
		}
		c := resizeStrategyCase{Data: base64.StdEncoding.EncodeToString(encoded.Bytes()), MimeType: []string{"image/png", "", "image/webp"}[rng.IntN(3)]}
		if rng.IntN(2) == 0 {
			c.MaxWidth = pickInt(1, 2, 7, 50, 99, 100, 640, 1000, 2000, 3000)
		}
		if rng.IntN(2) == 0 {
			c.MaxHeight = pickInt(1, 2, 7, 50, 99, 100, 640, 1000, 2000, 3000)
		}
		switch rng.IntN(5) {
		case 0:
			c.MaxBytes = 1
		case 1:
			c.MaxBytes = 1 << 40
		}
		if rng.IntN(3) == 0 {
			c.Quality = pickInt(1, 40, 85, 100)
		}
		cases = append(cases, c)
	}

	var want []resizeStrategyResult
	pioracle.Run(t, `
const { resizeImageInProcess } = await load("pi-coding-agent/utils/image-resize-core.js");
const out = [];
for (const c of input) {
	const options = {};
	for (const key of ["maxWidth", "maxHeight", "maxBytes", "jpegQuality"]) if (c[key]) options[key] = c[key];
	const result = await resizeImageInProcess(new Uint8Array(Buffer.from(c.data, "base64")), c.mimeType, options);
	out.push(result === null ? { null: true } : { null: false, ...result });
}
emit(out);`, cases, &want)

	failures := 0
	for i, c := range cases {
		raw, _ := base64.StdEncoding.DecodeString(c.Data)
		got, err := ResizeImage(raw, c.MimeType, &ai.ModelImageResizeOptions{MaxWidth: c.MaxWidth, MaxHeight: c.MaxHeight, MaxBytes: c.MaxBytes, JPEGQuality: c.Quality})
		gotResult := resizeStrategyResult{Null: err != nil}
		if err == nil {
			gotResult = resizeStrategyResult{MimeType: got.MimeType, OriginalWidth: got.OriginalWidth, OriginalHeight: got.OriginalHeight, Width: got.Width, Height: got.Height, WasResized: got.WasResized}
			if config, _, decodeErr := image.DecodeConfig(bytes.NewReader(got.Bytes())); decodeErr != nil || config.Width != got.Width || config.Height != got.Height {
				t.Errorf("case %d: PiG's image decodes to %+v (%v), not the %dx%d it reports", i, config, decodeErr, got.Width, got.Height)
			}
		}
		expected := want[i]
		if !expected.Null {
			if pdata, decodeErr := base64.StdEncoding.DecodeString(expected.Data); decodeErr != nil || len(pdata) == 0 {
				t.Fatalf("case %d: Pi image does not decode", i)
			}
		}
		expected.Data = ""
		if gotResult != expected {
			failures++
			if failures <= 8 {
				t.Errorf("case %d (%+v, %d bytes)\n Go %+v\n Pi %+v", i, resizeStrategyCase{MimeType: c.MimeType, MaxWidth: c.MaxWidth, MaxHeight: c.MaxHeight, MaxBytes: c.MaxBytes, Quality: c.Quality}, len(raw), gotResult, expected)
			}
		}
	}
	t.Logf("%d cases, %d failures", len(cases), failures)
}
