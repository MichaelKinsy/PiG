package codingagent

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
)

// The upstream image-processing.test.ts fixtures exercise Photon decoding and
// EXIF orientation. Decode the entire returned PNG, not just its IHDR header.
func TestImageConvertDecodedPixels(t *testing.T) {
	t.Parallel()
	var gifBytes bytes.Buffer
	pal := image.NewPaletted(image.Rect(0, 0, 2, 1), color.Palette{color.Black, color.White})
	pal.SetColorIndex(1, 0, 1)
	if err := gif.Encode(&gifBytes, pal, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mime, data string
		oriented         bool
	}{
		{"jpeg", "image/jpeg", upstreamTinyJPEG, false},
		{"webp", "image/webp", tinyWebP, false},
		{"gif", "image/gif", base64.StdEncoding.EncodeToString(gifBytes.Bytes()), false},
		{"bmp", "image/bmp", base64.StdEncoding.EncodeToString(makeBMPImage(t, 2, 3, color.RGBA{R: 255, A: 255})), false},
		{"sniff PNG despite MIME", "image/jpeg", upstreamTinyPNG, false},
		{"EXIF after XMP", "image/jpeg", jpegWithXmpBeforeOrientation(t), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, err := base64.StdEncoding.DecodeString(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			original := bytes.Clone(input)
			src, _, err := image.Decode(bytes.NewReader(input))
			if err != nil {
				t.Fatal(err)
			}
			converted := ConvertToPng(tc.data, tc.mime)
			if converted == nil || converted.MimeType != "image/png" {
				t.Fatalf("ConvertToPng = %+v, want image/png", converted)
			}
			encoded, err := base64.StdEncoding.DecodeString(converted.Data)
			if err != nil {
				t.Fatal(err)
			}
			out, err := png.Decode(bytes.NewReader(encoded))
			if err != nil {
				t.Fatalf("output PNG does not decode: %v", err)
			}
			w, h := src.Bounds().Dx(), src.Bounds().Dy()
			if tc.oriented {
				w, h = 1, 2 // upstream orientation-6 2x1 fixture rotates clockwise
				if src.At(0, 0) == src.At(1, 0) {
					t.Fatal("orientation fixture needs distinguishable pixels")
				}
			}
			if out.Bounds() != image.Rect(0, 0, w, h) {
				t.Fatalf("bounds = %v, want %dx%d", out.Bounds(), w, h)
			}
			for y := range h {
				for x := range w {
					sx, sy := x, y
					if tc.oriented {
						sx, sy = y, 0 // source left -> top, source right -> bottom
					}
					want := color.NRGBAModel.Convert(src.At(sx, sy))
					if got := color.NRGBAModel.Convert(out.At(x, y)); got != want {
						t.Errorf("pixel (%d,%d) = %v, want %v", x, y, got, want)
					}
				}
			}
			// Exercise the byte API used by mapped production consumers too.
			if raw := ConvertImageBytesToPng(input); !bytes.Equal(raw, encoded) {
				t.Fatal("byte and base64 conversion APIs disagree")
			}
			if !bytes.Equal(input, original) {
				t.Fatal("conversion mutated caller-owned input")
			}
		})
	}
}
