package codingagent

// pi: packages/coding-agent/src/utils/image-convert.ts

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// convertToPng decodes with Photon (`PhotonImage.new_from_byteslice`, image-convert.ts:encodePng), not with Go's decoders, so the
// PNG it returns for a JPEG or a WebP carries Photon's pixels. This runs Pi's convertToPng on the fixtures of
// TestImageConvertDecodedPixels and requires the same PNG bytes, which fixes the pixels the Go decoders disagree on:
// the 2x1 JPEG of image-processing.test.ts that the EXIF fixture wraps decodes to {254 3 0 255} with Photon and to
// {253 2 0 255} with Go's image/jpeg, and Go's golang.org/x/image/webp is not even registered by the production code.
func TestConvertToPngMatchesPiBytes(t *testing.T) {
	var gifBytes bytes.Buffer
	palette := image.NewPaletted(image.Rect(0, 0, 2, 1), color.Palette{color.Black, color.White})
	palette.SetColorIndex(1, 0, 1)
	if err := gif.Encode(&gifBytes, palette, nil); err != nil {
		t.Fatal(err)
	}
	type fixture struct {
		Name string `json:"name"`
		MIME string `json:"mime"`
		Data string `json:"data"`
	}
	fixtures := []fixture{
		{"jpeg", "image/jpeg", upstreamTinyJPEG},
		{"webp", "image/webp", tinyWebP},
		{"gif", "image/gif", base64.StdEncoding.EncodeToString(gifBytes.Bytes())},
		{"bmp", "image/bmp", base64.StdEncoding.EncodeToString(makeBMPImage(t, 2, 3, color.RGBA{R: 255, A: 255}))},
		{"png sniffed despite the MIME type", "image/jpeg", upstreamTinyPNG},
		{"EXIF after XMP", "image/jpeg", jpegWithXmpBeforeOrientation(t)},
		{"2x1 jpeg", "image/jpeg", upstreamTinyJPEG2x1},
	}
	var want []*struct {
		Data string `json:"data"`
		MIME string `json:"mimeType"`
	}
	pioracle.Run(t, `
const { convertToPng } = await load("pi-coding-agent/utils/image-convert.js");
emit(await Promise.all(input.map((f) => convertToPng(f.data, f.mime))));`, fixtures, &want)
	if len(want) != len(fixtures) {
		t.Fatalf("Pi converted %d fixtures, want %d", len(want), len(fixtures))
	}
	for i, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			got := ConvertToPng(f.Data, f.MIME)
			switch {
			case want[i] == nil && got != nil:
				t.Fatalf("Pi returns null, got %s", got.MimeType)
			case want[i] != nil && got == nil:
				t.Fatal("got nil, Pi converts")
			case want[i] != nil && (got.MimeType != want[i].MIME || got.Data != want[i].Data):
				t.Fatalf("converted PNG differs from Pi's: %d base64 bytes vs %d", len(got.Data), len(want[i].Data))
			}
		})
	}
}
