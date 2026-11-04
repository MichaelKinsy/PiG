// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package imageprocessing

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// pngOracleInputs are the real PNG files of the Photon decoder corpus (PngSuite, image-rs, Go), damaged copies of some of them (cut, with a byte
// changed in the image data, with a checksum wrong), and images with no pixels, which Photon decodes from other formats.
func pngOracleInputs(t *testing.T) []namedImage {
	t.Helper()
	var inputs []namedImage
	files, err := filepath.Glob("../photonimage/testdata/png-real/*/*.png")
	if err != nil || len(files) < 200 {
		t.Fatalf("PNG files: %d, %v", len(files), err)
	}
	damaged := map[string]bool{"basn2c08": true, "basn6a16": true, "basi3p04": true, "tbbn3p08": true, "tbrn2c08": true, "basn0g01": true, "cs3n3p08": true, "ball": true, "basi4a16": true}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(filepath.Dir(file)) + "/" + filepath.Base(file)
		inputs = append(inputs, namedImage{name, data})
		base := filepath.Base(file)
		for stem := range damaged {
			if strings.HasSuffix(strings.TrimSuffix(base, ".png"), stem) {
				for _, cut := range []int{len(data) - 1, len(data) - 12, len(data) / 2, 70} {
					inputs = append(inputs, namedImage{name + "-cut-" + decimal(cut), data[:cut]})
				}
				flipped := slices.Clone(data)
				flipped[len(data)/2] ^= 0x55
				inputs = append(inputs, namedImage{name + "-flipped-middle", flipped})
				badCRC := slices.Clone(data)
				// The checksum of the first chunk after IHDR.
				off := 8 + 12 + int(binary.BigEndian.Uint32(data[8:]))
				badCRC[off+8+int(binary.BigEndian.Uint32(data[off:]))] ^= 1
				inputs = append(inputs, namedImage{name + "-bad-crc-after-ihdr", badCRC})
			}
		}
	}
	return inputs
}

func decimal(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}

// TestProcessImagePNGMatchesPi runs image-process.ts processImage and PiG's ProcessImage over PNG files under declared types of their own, of the
// other inline types and of none: Pi hands PNG bytes to Photon's `new_from_byteslice` through resize or through convertImageBytesToPng, and
// Photon's png crate decodes or refuses them (bad checksums, truncated data, damaged zlib streams). Whether an image is returned, its type, the
// hints, the omission message and every returned byte must be Pi's.
func TestProcessImagePNGMatchesPi(t *testing.T) {
	compareProcessImageWithPi(t, pngOracleInputs(t), []string{"image/png", "image/x-icon", "", "application/octet-stream", "image/jpeg; q=1", "image/gif", "image/webp"})
}

// TestProcessImageWithNoPixelsMatchesPi: an image with a width or a height of zero, from farbfeld and from an interlaced GIF. Photon decodes it
// and the PNG that would be written for it is an exception (`get_bytes` of a zero width), so Pi returns the bytes of an image that is within the
// limits as they are and omits the others, for every declared type.
func TestProcessImageWithNoPixelsMatchesPi(t *testing.T) {
	var inputs []namedImage
	for _, size := range [][2]int{{0, 4}, {4, 0}, {0, 0}, {0, 3000}, {3000, 0}, {0, 70000}, {70000, 0}} {
		inputs = append(inputs, namedImage{"farbfeld-" + decimal(size[0]) + "x" + decimal(size[1]), farbfeldInput(size[0], size[1], 3)[:16]})
	}
	inputs = append(inputs, namedImage{"gif-5x0", gifNoRowsInput(5)}, namedImage{"gif-0x0", gifNoRowsInput(0)})
	compareProcessImageWithPi(t, inputs, []string{"image/png", "image/x-icon", "", "application/octet-stream", "image/jpeg; q=1", "image/gif", "image/webp", "image/bmp"})
}

// gifNoRowsInput is a GIF whose screen and interlaced frame are width x 0.
func gifNoRowsInput(width int) []byte {
	out := []byte("GIF89a")
	out = binary.LittleEndian.AppendUint16(out, uint16(width))
	out = binary.LittleEndian.AppendUint16(out, 0)
	out = append(out, 0x81, 0, 0)
	out = append(out, formatNoise(3, 12)...)
	out = append(out, 0x2C, 0, 0, 0, 0)
	out = binary.LittleEndian.AppendUint16(out, uint16(width))
	out = binary.LittleEndian.AppendUint16(out, 0)
	out = append(out, 0x40, 2, 1, 0x2C, 0, 0x3B)
	return out
}
