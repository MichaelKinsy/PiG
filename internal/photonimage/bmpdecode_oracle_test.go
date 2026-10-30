// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"encoding/binary"
	"image"
	"testing"

	"golang.org/x/image/bmp"
)

// TestDecodeBMPMatchesPhoton compares Decode with Photon on the corpus of bmpdecode_corpus_test.go (image 0.24.9's BMP decoder; see
// comparePhotonCorpus).
func TestDecodeBMPMatchesPhoton(t *testing.T) {
	comparePhotonCorpus(t, "bmp", ".bmp", bmpCorpus())
}

// bmpClaimedPixels is the number of pixels the header of a BMP file claims (the core header's 16-bit size or the other headers' signed 32-bit one).
func bmpClaimedPixels(data []byte) uint64 {
	if len(data) < 26 {
		return 0
	}
	if binary.LittleEndian.Uint32(data[14:]) == 12 {
		return uint64(binary.LittleEndian.Uint16(data[18:])) * uint64(binary.LittleEndian.Uint16(data[20:]))
	}
	abs := func(v int32) uint64 {
		if v < 0 {
			return uint64(-int64(v))
		}
		return uint64(v)
	}
	return abs(int32(binary.LittleEndian.Uint32(data[18:]))) * abs(int32(binary.LittleEndian.Uint32(data[22:])))
}

// TestGoBMPDecoderDiffersFromPhoton is why BMP files are not decoded by golang.org/x/image/bmp: on the corpus it accepts files Photon refuses,
// refuses files Photon decodes and gives other pixels for files both decode. The counts are logged; the test fails when the decoder turns out to
// be the same on the whole corpus (then the routing through the port would have no reason). Files that claim more than 2^28 pixels are not
// given to Go's decoder (it allocates its image from the header before it reads a pixel, which takes gigabytes), and Photon refuses all of
// them.
func TestGoBMPDecoderDiffersFromPhoton(t *testing.T) {
	rows := readPhotonTable(t, "bmp")
	var total, skipped, accepts, refuses, pixels int
	for _, c := range bmpCorpus() {
		row, ok := rows[c.name]
		if !ok {
			t.Fatalf("%s: not in the table of Photon's results", c.name)
		}
		total++
		data := c.bytes()
		photonDecodes := row.width != "error"
		if bmpClaimedPixels(data) > 1<<28 {
			skipped++
			if photonDecodes {
				t.Fatalf("%s: Photon decodes a bitmap of more than 2^28 pixels", c.name)
			}
			continue
		}
		img, err := bmp.Decode(bytes.NewReader(data))
		switch {
		case err == nil && !photonDecodes:
			accepts++
		case err != nil && photonDecodes:
			refuses++
		case err == nil:
			nrgba := image.NewNRGBA(img.Bounds())
			for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
				for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
					nrgba.Set(x, y, img.At(x, y))
				}
			}
			if sha256Hex(nrgba.Pix) != row.pixels {
				pixels++
			}
		}
	}
	t.Logf("golang.org/x/image/bmp on %d files (%d not given to it): accepts %d that Photon refuses, refuses %d that Photon decodes, other pixels for %d", total, skipped, accepts, refuses, pixels)
	if accepts+refuses+pixels == 0 {
		t.Fatal("golang.org/x/image/bmp equals Photon on the whole corpus")
	}
}
