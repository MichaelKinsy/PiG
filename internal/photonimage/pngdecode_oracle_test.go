// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"testing"
)

// TestDecodePNGMatchesPhoton compares Decode with Photon on the corpus of pngdecode_corpus_test.go (image 0.24.9's PNG decoder over the png
// crate 0.17.14 and fdeflate 0.3.6; see comparePhotonCorpus).
func TestDecodePNGMatchesPhoton(t *testing.T) {
	comparePhotonCorpus(t, "png", ".png", pngCorpus())
}

// goPNGDecode is what Go's image/png makes of a file, as an NRGBA of the image's size; ok is false when Go refuses the file.
func goPNGDecode(data []byte) (*image.NRGBA, bool) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	out := image.NewNRGBA(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			out.SetNRGBA(x, y, color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA))
		}
	}
	return out, true
}

// TestGoPNGDecoderDiffersFromPhoton is why PNG files are not decoded by image/png: on the corpus it accepts files Photon refuses, refuses files
// Photon decodes and gives other pixels for files both decode. The counts are logged; the test fails when the decoder turns out to be the same on
// the whole corpus. Files whose image has more than 2^26 pixels are not given to Go's decoder, which allocates the image from the header.
func TestGoPNGDecoderDiffersFromPhoton(t *testing.T) {
	rows := readPhotonTable(t, "png")
	var total, skipped, accepts, refuses, pixels, same int
	for _, c := range pngCorpus() {
		row, ok := rows[c.name]
		if !ok {
			t.Fatalf("%s: not in the table of Photon's results", c.name)
		}
		total++
		data := c.bytes()
		if len(data) >= 24 && uint64(binary.BigEndian.Uint32(data[16:]))*uint64(binary.BigEndian.Uint32(data[20:])) > 1<<26 {
			skipped++
			continue
		}
		img, ok := goPNGDecode(data)
		photonDecodes := row.width != "error"
		switch {
		case ok && !photonDecodes:
			accepts++
		case !ok && photonDecodes:
			refuses++
		case ok:
			size := img.Rect.Size()
			if sha256Hex(img.Pix) != row.pixels || row.width != strconv.Itoa(size.X) || row.height != strconv.Itoa(size.Y) {
				pixels++
			} else {
				same++
			}
		default:
			same++
		}
	}
	t.Logf("image/png on %d files (%d not given to it): accepts %d that Photon refuses, refuses %d that Photon decodes, other pixels for %d, the same for %d", total, skipped, accepts, refuses, pixels, same)
	if accepts+refuses+pixels == 0 {
		t.Fatal("image/png equals Photon on the whole corpus")
	}
}
