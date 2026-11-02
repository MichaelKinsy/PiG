// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"strconv"
	"testing"
)

// TestDecodeGIFMatchesPhoton compares Decode with Photon on the corpus of gifdecode_corpus_test.go (image 0.24.9's GIF decoder over the gif
// crate 0.13.1 and weezl 0.1.8; see comparePhotonCorpus).
func TestDecodeGIFMatchesPhoton(t *testing.T) {
	comparePhotonCorpus(t, "gif", ".gif", gifCorpus())
}

// goGIFFirstFrame is what Go's image/gif makes of the first frame of a file, placed on a screen of the logical size as Photon's decoder
// places it (the frame's pixels, transparent black elsewhere); ok is false when Go refuses the file.
func goGIFFirstFrame(data []byte) (*image.NRGBA, bool) {
	config, err := gif.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	frame, err := gif.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	screen := image.NewNRGBA(image.Rect(0, 0, config.Width, config.Height))
	bounds := frame.Bounds().Intersect(screen.Bounds())
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			screen.SetNRGBA(x, y, color.NRGBAModel.Convert(frame.At(x, y)).(color.NRGBA))
		}
	}
	return screen, true
}

// TestGoGIFDecoderDiffersFromPhoton is why GIF files are not decoded by image/gif: on the corpus it accepts files Photon refuses, refuses files
// Photon decodes and gives other pixels for files both decode. The counts are logged; the test fails when the decoder turns out to be the same on
// the whole corpus. Files whose screen has more than 2^28 pixels are not given to Go's decoder, which allocates the screen's image.
func TestGoGIFDecoderDiffersFromPhoton(t *testing.T) {
	rows := readPhotonTable(t, "gif")
	var total, skipped, accepts, refuses, pixels int
	for _, c := range gifCorpus() {
		row, ok := rows[c.name]
		if !ok {
			t.Fatalf("%s: not in the table of Photon's results", c.name)
		}
		total++
		data := c.bytes()
		if len(data) >= 10 && (int(data[6])|int(data[7])<<8)*(int(data[8])|int(data[9])<<8) > 1<<28 {
			skipped++
			continue
		}
		screen, ok := goGIFFirstFrame(data)
		photonDecodes := row.width != "error"
		switch {
		case ok && !photonDecodes:
			accepts++
		case !ok && photonDecodes:
			refuses++
		case ok:
			size := screen.Rect.Size()
			if sha256Hex(screen.Pix) != row.pixels || row.width != strconv.Itoa(size.X) || row.height != strconv.Itoa(size.Y) {
				pixels++
			}
		}
	}
	t.Logf("image/gif on %d files (%d not given to it): accepts %d that Photon refuses, refuses %d that Photon decodes, other pixels for %d", total, skipped, accepts, refuses, pixels)
	if accepts+refuses+pixels == 0 {
		t.Fatal("image/gif equals Photon on the whole corpus")
	}
}
