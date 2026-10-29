package imageprocessing

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	// SHA256 is the SHA-256 of the returned bytes.
	SHA256 string `json:"sha256"`
}

// TestProcessImageMatchesPi runs image-process.ts processImage and PiG's ProcessImage over 400 seeded inputs: PNG, JPEG, GIF, BMP, TIFF and WebP bytes (some larger than the resize
// limits), garbage and empty bytes; declared types with parameters, case and spaces ("IMAGE/JPEG; q=1", " image/jpg "), unsupported types (image/bmp, image/tiff, image/x-icon, application/octet-stream,
// the empty string) and types that do not match the bytes; automatic resizing on and off with small or default limits. Whether an image is returned, its mime type, the
// conversion and dimension hints (in order) and the omission message must match. The returned bytes must match as well: PiG's PNG and JPEG encoders and its resize are ports of Photon's, except for a JPEG or WebP input, which Go decodes to other pixels (D165).
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
const { createHash } = await import("node:crypto");
const out = [];
for (const c of input) {
	const resizeOptions = {};
	if (c.maxWidth) resizeOptions.maxWidth = c.maxWidth;
	if (c.maxHeight) resizeOptions.maxHeight = c.maxHeight;
	const r = await processImage(new Uint8Array(Buffer.from(c.data, "base64")), c.mimeType, { autoResizeImages: c.autoResize, resizeOptions });
	out.push(r.ok ? { ok: true, mimeType: r.mimeType, hints: r.hints.join("\n"), message: "", sha256: createHash("sha256").update(Buffer.from(r.data, "base64")).digest("hex") } : { ok: false, mimeType: "", hints: "", message: r.message, sha256: "" });
}
emit(out);`, cases, &want)

	failures := 0
	// bySource counts the cases and the failures by the format of the input bytes, and bytesOnly the failures that differ in the
	// returned bytes alone (the mime type, the hints and the omission message are Pi's).
	bySource, bytesOnly := map[string][2]int{}, map[string]int{}
	for i, c := range cases {
		raw, _ := base64.StdEncoding.DecodeString(c.Data)
		data, mime, hints, err := ProcessImage(raw, c.MimeType, c.AutoResize, &ai.ModelImageResizeOptions{MaxWidth: c.MaxWidth, MaxHeight: c.MaxHeight})
		got := processImageOutcome{OK: err == nil, MimeType: mime, Hints: hints}
		if err != nil {
			got.Message = err.Error()
		} else {
			sum := sha256.Sum256(data)
			got.SHA256 = hex.EncodeToString(sum[:])
		}
		source := detectFormat(raw)
		counts := bySource[source]
		counts[0]++
		// D165: Go's JPEG and WebP decoders give other pixels than Photon's, so the bytes a re-encoded JPEG or WebP input comes out as
		// are not Pi's; what is compared for them is the rest of the outcome. Every other input is compared byte for byte.
		if (source == "jpeg" || source == "webp") && !reflect.DeepEqual(got, want[i]) {
			counts[1]++
			withoutBytes := got
			withoutBytes.SHA256 = want[i].SHA256
			if reflect.DeepEqual(withoutBytes, want[i]) {
				bytesOnly[source]++
				bySource[source] = counts
				continue
			}
			counts[1]--
		}
		if !reflect.DeepEqual(got, want[i]) {
			counts[1]++
			withoutBytes := got
			withoutBytes.SHA256 = want[i].SHA256
			if reflect.DeepEqual(withoutBytes, want[i]) {
				bytesOnly[source]++
			}
			failures++
			if counts[1] <= 3 { // the first three of each input format
				t.Errorf("case %d (%s, %d bytes, resize %v, %dx%d, head %q)\n Go %+v\n Pi %+v", i, c.MimeType, len(raw), c.AutoResize, c.MaxWidth, c.MaxHeight, string(raw[:min(8, len(raw))]), got, want[i])
			}
		}
		bySource[source] = counts
	}
	for source, counts := range bySource {
		t.Logf("input %q: %d cases, %d differ from Pi (%d in the bytes alone)", source, counts[0], counts[1], bytesOnly[source])
	}
	t.Logf("%d cases, %d failures", len(cases), failures)
}
