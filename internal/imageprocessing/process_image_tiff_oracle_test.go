package imageprocessing

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/image/tiff"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// tiffPicture is a picture of the given colour model: the TIFF writer of golang.org/x/image picks its photometric interpretation and
// samples from the image type.
func tiffPicture(kind string, width, height int) image.Image {
	rect := image.Rect(0, 0, width, height)
	switch kind {
	case "gray":
		img := image.NewGray(rect)
		for i := range img.Pix {
			img.Pix[i] = byte(i*7 + i/width)
		}
		return img
	case "gray16":
		img := image.NewGray16(rect)
		for y := range height {
			for x := range width {
				img.SetGray16(x, y, color.Gray16{Y: uint16(x*401 + y*977)})
			}
		}
		return img
	case "cmyk":
		img := image.NewCMYK(rect)
		for y := range height {
			for x := range width {
				img.SetCMYK(x, y, color.CMYK{C: uint8(x * 5), M: uint8(y * 7), Y: uint8(x ^ y), K: uint8(x + y)})
			}
		}
		return img
	case "paletted":
		img := image.NewPaletted(rect, color.Palette{color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 255, 0, 255}, color.NRGBA{0, 0, 255, 255}})
		for i := range img.Pix {
			img.Pix[i] = byte(i % 3)
		}
		return img
	case "rgba64":
		img := image.NewRGBA64(rect)
		for y := range height {
			for x := range width {
				img.SetRGBA64(x, y, color.RGBA64{R: uint16(x * 700), G: uint16(y * 900), B: uint16(x * y), A: 0xFFFF})
			}
		}
		return img
	case "nrgba":
		img := image.NewNRGBA(rect)
		for y := range height {
			for x := range width {
				img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 3), G: uint8(y * 5), B: uint8(x ^ y), A: uint8(200 + x%50)})
			}
		}
		return img
	}
	img := image.NewRGBA(rect)
	for y := range height {
		for x := range width {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 3), G: uint8(y * 5), B: uint8(x ^ y), A: 255})
		}
	}
	return img
}

// TestProcessImageTIFFMatchesPi runs image-process.ts processImage and PiG's ProcessImage over TIFF files: Pi hands any bytes whose
// declared type is not PNG, JPEG, GIF or WebP to Photon, which decodes TIFF (image 0.24.9 with tiff 0.9.1), and it hands the bytes of
// the four inline types to Photon as well (the format is guessed from the bytes). The files are written by golang.org/x/image/tiff
// (uncompressed and Deflate, with and without the predictor, gray, 16-bit, RGBA, NRGBA, CMYK and palette images, some larger than
// the resize limits) and read from testdata/tiff (LZW, PackBits, JPEG, tiles, planar and big-endian files and files Photon refuses).
// Whether an image is returned, its type, the hints, the omission message and the returned bytes must be Pi's.
func TestProcessImageTIFFMatchesPi(t *testing.T) {
	type input struct {
		name string
		data []byte
	}
	var inputs []input
	for _, kind := range []string{"gray", "gray16", "rgba", "nrgba", "rgba64", "cmyk", "paletted"} {
		for _, size := range [][2]int{{40, 30}, {2300, 1700}} {
			if kind != "gray" && size[0] > 100 {
				continue
			}
			for _, opts := range []*tiff.Options{nil, {Compression: tiff.Deflate}, {Compression: tiff.Deflate, Predictor: true}, {Compression: tiff.Uncompressed, Predictor: true}} {
				if size[0] > 100 && (opts == nil || !opts.Predictor || opts.Compression != tiff.Deflate) {
					continue // one big file is enough to exercise the resize
				}
				var encoded bytes.Buffer
				if err := tiff.Encode(&encoded, tiffPicture(kind, size[0], size[1]), opts); err != nil {
					t.Fatal(err)
				}
				name := kind
				if opts != nil {
					name += "-opts"
				}
				inputs = append(inputs, input{name, encoded.Bytes()})
			}
		}
	}
	files, err := filepath.Glob("testdata/tiff/*.tif")
	if err != nil || len(files) == 0 {
		t.Fatalf("no TIFF files in testdata/tiff: %v", err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, input{filepath.Base(file), data})
	}
	inputs = append(inputs, input{"truncated", inputs[len(inputs)-1].data[:100]}, input{"header only", []byte("II*\x00\x08\x00\x00\x00")})

	var cases []processImageCase
	var names []string
	for _, in := range inputs {
		for _, mime := range []string{"image/tiff", "image/png", "", "application/octet-stream", "image/jpeg; q=1"} {
			for _, resize := range []bool{true, false} {
				for _, limit := range []int{0, 16} {
					cases = append(cases, processImageCase{Data: base64.StdEncoding.EncodeToString(in.data), MimeType: mime, AutoResize: resize, MaxWidth: limit, MaxHeight: limit})
					names = append(names, in.name)
				}
			}
		}
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

	decoded := 0
	failures := 0
	for i, c := range cases {
		raw, _ := base64.StdEncoding.DecodeString(c.Data)
		data, mime, hints, err := ProcessImage(raw, c.MimeType, c.AutoResize, &ai.ModelImageResizeOptions{MaxWidth: c.MaxWidth, MaxHeight: c.MaxHeight})
		got := processImageOutcome{OK: err == nil, MimeType: mime, Hints: hints}
		if err != nil {
			got.Message = err.Error()
		} else {
			sum := sha256.Sum256(data)
			got.SHA256 = hex.EncodeToString(sum[:])
			decoded++
		}
		if !reflect.DeepEqual(got, want[i]) {
			failures++
			if failures <= 20 {
				t.Errorf("case %d (%s, declared %q, resize %v, limit %d)\n Go %+v\n Pi %+v", i, names[i], c.MimeType, c.AutoResize, c.MaxWidth, got, want[i])
			}
		}
	}
	t.Logf("%d cases, %d returned an image, %d failures", len(cases), decoded, failures)
	if decoded == 0 {
		t.Error("no case returns an image: the inputs do not exercise the decoder")
	}
}
