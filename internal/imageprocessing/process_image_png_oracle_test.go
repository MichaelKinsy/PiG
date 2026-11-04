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

// imageSweepEnv selects the extent of the PNG sweep: "full" runs every input under every declared type and limit, as the nightly workflow does
// (make test-oracle-sweeps-nightly); anything else runs the covering subset below.
const imageSweepEnv = "PIG_IMAGE_ORACLE_SWEEP"

// pngDeclaredTypes are the types a PNG file is declared under: its own, the other inline types, types Pi converts from, and none.
var pngDeclaredTypes = []string{"image/png", "image/x-icon", "", "application/octet-stream", "image/jpeg; q=1", "image/gif", "image/webp"}

// pngDeclaredTypeInputs are the inputs the covering subset runs under every declared type, with and without resizing and under both limits: an
// 8-bit true colour image and three damaged copies of it, an interlaced palette image, a 16-bit image with alpha, a one-bit grey image with a
// byte changed, an image over the default limit, a 150x103 image (its second side is a fraction when it is resized to 16), and a file with
// a damaged zlib stream.
var pngDeclaredTypeInputs = []string{
	"pngsuite/basn2c08.png", "pngsuite/basn2c08.png-cut-70", "pngsuite/basn2c08.png-flipped-middle", "pngsuite/basn2c08.png-bad-crc-after-ihdr",
	"pngsuite/basi3p04.png", "image-rs/16bpc-basn6a16.png", "pngsuite/basn0g01.png-flipped-middle",
	"image-rs/bugfixes-debug_triangle_corners_widescreen.png", "go/image-video-001.png", "go/invalid-zlib.png",
}

// pngCoverageKey is what a PNG input adds to the covering subset: its colour type, bit depth and interlacing from the header, the size class
// against the limits the sweep uses (16 and 2000), whether it is square (a resize of any other shape rounds the second side), and the damage the
// input carries. Two inputs with the same key take the same branches of
// the pipeline, so the first of them stands for the rest.
func pngCoverageKey(in namedImage) string {
	damage := "intact"
	switch {
	case strings.HasSuffix(in.name, "-cut-70"):
		damage = "cut-in-header"
	case strings.Contains(in.name, "-cut-"):
		damage = "cut-in-data"
	case strings.HasSuffix(in.name, "-flipped-middle"):
		damage = "flipped"
	case strings.HasSuffix(in.name, "-bad-crc-after-ihdr"):
		damage = "bad-crc"
	case strings.Contains(in.name, "/invalid-"):
		damage = "invalid"
	}
	if len(in.data) < 29 {
		return damage + " no-header"
	}
	width, height := binary.BigEndian.Uint32(in.data[16:]), binary.BigEndian.Uint32(in.data[20:])
	size := "tiny"
	if side := max(width, height); side > 2000 {
		size = "over-default-limit"
	} else if side > 16 {
		size = "over-16"
	}
	shape := "square"
	if width > height {
		shape = "landscape"
	} else if width < height {
		shape = "portrait"
	}
	return damage + " " + size + " " + shape + " depth " + decimal(int(in.data[24])) + " type " + decimal(int(in.data[25])) + " interlace " + decimal(int(in.data[28]))
}

// pngSweep is the covering subset of the sweep: each of the inputs of pngDeclaredTypeInputs under every declared type, and the first input of
// each pngCoverageKey under the PNG type, resized by default and to 16 and left alone, and as a type Pi converts from, resized by default and
// left alone. Pi's cost is its resizing calls, so these are a tenth of the full sweep's.
func pngSweep(t *testing.T, inputs []namedImage) oracleSweep {
	t.Helper()
	var sweep oracleSweep
	byName := make(map[string]namedImage, len(inputs))
	for _, in := range inputs {
		byName[in.name] = in
	}
	for _, name := range pngDeclaredTypeInputs {
		in, ok := byName[name]
		if !ok {
			t.Fatalf("the covering subset names %s, which the corpus does not have", name)
		}
		sweep.addProduct(in, pngDeclaredTypes)
	}
	seen := map[string]bool{}
	for _, in := range inputs {
		key := pngCoverageKey(in)
		if seen[key] {
			continue
		}
		seen[key] = true
		sweep.add(in, "image/png", true, 0)
		sweep.add(in, "image/png", true, 16)
		sweep.add(in, "image/png", false, 0)
		sweep.add(in, "image/x-icon", true, 0)
		sweep.add(in, "image/x-icon", false, 0)
	}
	return sweep
}

// TestProcessImagePNGMatchesPi runs image-process.ts processImage and PiG's ProcessImage over PNG files under declared types of their own, of the
// other inline types and of none: Pi hands PNG bytes to Photon's `new_from_byteslice` through resize or through convertImageBytesToPng, and
// Photon's png crate decodes or refuses them (bad checksums, truncated data, damaged zlib streams). Whether an image is returned, its type, the
// hints, the omission message and every returned byte must be Pi's. Photon's decoder itself is compared on every file by
// photonimage.TestDecodePNGMatchesPhoton, so this test sweeps the pipeline around it. Each run takes the covering subset of pngSweep; with
// PIG_IMAGE_ORACLE_SWEEP=full it takes every input under every declared type, which is 7952 cases and 150 s on 4 CPUs, of which Pi's resizing
// is 97%: the nightly workflow runs that.
func TestProcessImagePNGMatchesPi(t *testing.T) {
	inputs := pngOracleInputs(t)
	if os.Getenv(imageSweepEnv) == "full" {
		compareProcessImageWithPi(t, inputs, pngDeclaredTypes)
		return
	}
	compareProcessImageCasesWithPi(t, pngSweep(t, inputs))
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
