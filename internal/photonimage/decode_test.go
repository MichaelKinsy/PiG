// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"

	"golang.org/x/image/bmp"
)

// The 2x2 JPEG and the 1x1 WebP of upstream test/image-processing.test.ts.
const (
	decodeTinyJPEG = "/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAMCAgMCAgMDAwMEAwMEBQgFBQQEBQoHBwYIDAoMDAsKCwsNDhIQDQ4RDgsLEBYQERMUFRUVDA8XGBYUGBIUFRT/2wBDAQMEBAUEBQkFBQkUDQsNFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBT/wAARCAACAAIDAREAAhEBAxEB/8QAFAABAAAAAAAAAAAAAAAAAAAACf/EABQQAQAAAAAAAAAAAAAAAAAAAAD/xAAVAQEBAAAAAAAAAAAAAAAAAAAGCf/EABQRAQAAAAAAAAAAAAAAAAAAAAD/2gAMAwEAAhEDEQA/AD3VTB3/2Q=="
	decodeTinyWebP = "UklGRjwAAABXRUJQVlA4IDAAAADQAQCdASoBAAEAAgA0JaACdLoB+AADsAD+8MQL/yC5YXXI1/8gP+QH/ID/+PIAAAA="
)

func decodeFixture(t *testing.T, encoded string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// samePixels reports whether a and b are the same NRGBA image.
func samePixels(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			if color.NRGBAModel.Convert(a.At(x, y)) != color.NRGBAModel.Convert(b.At(x, y)) {
				return false
			}
		}
	}
	return true
}

// Decode is Photon's `new_from_byteslice`: JPEG and WebP bytes go to the ports of its decoders, whatever decoders the binary registers with the standard library.
func TestDecodeGivesPhotonPixelsForJPEGAndWebP(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   []byte
		photon func([]byte) (*image.NRGBA, error)
	}{
		{"jpeg", decodeFixture(t, decodeTinyJPEG), DecodeJPEG},
		{"webp", decodeFixture(t, decodeTinyWebP), DecodeWebP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, err := tc.photon(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Decode(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			if !samePixels(got, want) {
				t.Fatalf("Decode differs from the Photon decoder: %v vs %v", got.At(0, 0), want.At(0, 0))
			}
		})
	}
}

// Every other format is decoded by the standard library decoders, which Decode registers itself.
func TestDecodeLosslessFormats(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for i := range 6 {
		source.SetNRGBA(i%3, i/3, color.NRGBA{R: uint8(40 * i), G: uint8(255 - 30*i), B: uint8(7 * i), A: 255})
	}
	var pngData, bmpData bytes.Buffer
	if err := png.Encode(&pngData, source); err != nil {
		t.Fatal(err)
	}
	if err := bmp.Encode(&bmpData, source); err != nil {
		t.Fatal(err)
	}
	palette := image.NewPaletted(image.Rect(0, 0, 3, 2), color.Palette{color.Black, color.White})
	palette.SetColorIndex(1, 1, 1)
	var gifData bytes.Buffer
	if err := gif.Encode(&gifData, palette, nil); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		data []byte
		want image.Image
	}{"png": {pngData.Bytes(), source}, "bmp": {bmpData.Bytes(), source}, "gif": {gifData.Bytes(), palette}} {
		t.Run(name, func(t *testing.T) {
			got, err := Decode(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			if !samePixels(got, tc.want) {
				t.Fatalf("pixels differ: %v vs %v", got.At(1, 1), tc.want.At(1, 1))
			}
		})
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	for name, data := range map[string][]byte{"empty": nil, "text": []byte("not an image"), "truncated jpeg": {0xFF, 0xD8, 0xFF, 0xE0}, "truncated webp": []byte("RIFF\x04\x00\x00\x00WEBP")} {
		if img, err := Decode(data); err == nil {
			t.Errorf("%s: Decode = %v, want an error", name, img.Bounds())
		}
	}
}
