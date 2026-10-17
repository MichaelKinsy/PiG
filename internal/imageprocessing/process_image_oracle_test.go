package imageprocessing

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"reflect"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type processImageCase struct {
	Data       string `json:"data"`
	MimeType   string `json:"mimeType"`
	AutoResize bool   `json:"autoResize"`
	MaxWidth   int    `json:"maxWidth,omitempty"`
	MaxHeight  int    `json:"maxHeight,omitempty"`
}

type processImageOutcome struct {
	OK       bool   `json:"ok"`
	MimeType string `json:"mimeType"`
	Hints    string `json:"hints"`
	Message  string `json:"message"`
}

// TestProcessImageMatchesPi runs image-process.ts processImage and PiG's ProcessImage over 400 seeded inputs: PNG, JPEG, GIF, BMP, TIFF and WebP bytes (some larger than the resize
// limits), garbage and empty bytes; declared types with parameters, case and spaces ("IMAGE/JPEG; q=1", " image/jpg "), unsupported types (image/bmp, image/tiff, image/x-icon, application/octet-stream,
// the empty string) and types that do not match the bytes; automatic resizing on and off with small or default limits. Whether an image is returned, its mime type, the
// conversion and dimension hints (in order) and the omission message must match. Not compared: the returned bytes (Photon's encoders differ from Go's).
func TestProcessImageMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261126, 9))
	pick := func(xs ...string) string { return xs[rng.IntN(len(xs))] }
	webp, err := base64.StdEncoding.DecodeString("UklGRjwAAABXRUJQVlA4IDAAAADQAQCdASoBAAEAAgA0JaACdLoB+AADsAD+8MQL/yC5YXXI1/8gP+QH/ID/+PIAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	picture := func(width, height int) *image.NRGBA {
		img := image.NewNRGBA(image.Rect(0, 0, width, height))
		for y := range height {
			for x := range width {
				img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 3), G: uint8(y * 5), B: uint8(x ^ y), A: 255})
			}
		}
		return img
	}
	var cases []processImageCase
	for range 400 {
		width, height := 1+rng.IntN(120), 1+rng.IntN(120)
		if rng.IntN(5) == 0 {
			width = 2001 + rng.IntN(600)
		}
		img := picture(width, height)
		var encoded bytes.Buffer
		switch rng.IntN(9) {
		case 0, 1:
			_ = png.Encode(&encoded, img)
		case 2:
			_ = jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 80})
		case 3:
			_ = gif.Encode(&encoded, img, nil)
		case 4:
			_ = bmp.Encode(&encoded, img)
		case 5:
			_ = tiff.Encode(&encoded, img, nil)
		case 6:
			encoded.Write(webp)
		case 7:
			encoded.WriteString(pick("not an image", "\x89PNG\r\n\x1a\ntruncated", "\xff\xd8\xff\xe0broken", "BM"))
		default:
		}
		c := processImageCase{
			Data:       base64.StdEncoding.EncodeToString(encoded.Bytes()),
			MimeType:   pick("image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp", "IMAGE/PNG; charset=binary", " image/jpg ", "image/bmp", "image/tiff", "image/x-icon", "application/octet-stream", "", "image/svg+xml", "image/BMP;x=1"),
			AutoResize: rng.IntN(3) > 0,
		}
		if rng.IntN(3) == 0 {
			c.MaxWidth = 1 + rng.IntN(100)
		}
		if rng.IntN(3) == 0 {
			c.MaxHeight = 1 + rng.IntN(100)
		}
		cases = append(cases, c)
	}

	var want []processImageOutcome
	pioracle.Run(t, `
const { processImage } = await load("pi-coding-agent/utils/image-process.js");
const out = [];
for (const c of input) {
	const resizeOptions = {};
	if (c.maxWidth) resizeOptions.maxWidth = c.maxWidth;
	if (c.maxHeight) resizeOptions.maxHeight = c.maxHeight;
	const r = await processImage(new Uint8Array(Buffer.from(c.data, "base64")), c.mimeType, { autoResizeImages: c.autoResize, resizeOptions });
	out.push(r.ok ? { ok: true, mimeType: r.mimeType, hints: r.hints.join("\n"), message: "" } : { ok: false, mimeType: "", hints: "", message: r.message });
}
emit(out);`, cases, &want)

	failures := 0
	for i, c := range cases {
		raw, _ := base64.StdEncoding.DecodeString(c.Data)
		_, mime, hints, err := ProcessImage(raw, c.MimeType, c.AutoResize, &ai.ModelImageResizeOptions{MaxWidth: c.MaxWidth, MaxHeight: c.MaxHeight})
		got := processImageOutcome{OK: err == nil, MimeType: mime, Hints: hints}
		if err != nil {
			got.Message = err.Error()
		}
		if !reflect.DeepEqual(got, want[i]) {
			failures++
			if failures <= 8 {
				t.Errorf("case %d (%s, %d bytes, resize %v, %dx%d, head %q)\n Go %+v\n Pi %+v", i, c.MimeType, len(raw), c.AutoResize, c.MaxWidth, c.MaxHeight, string(raw[:min(8, len(raw))]), got, want[i])
			}
		}
	}
	t.Logf("%d cases, %d failures", len(cases), failures)
}
