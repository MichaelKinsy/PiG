package images_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/tools"
)

// The formats Photon decodes and Go's standard library does not (ICO, DDS, PNM, Radiance HDR, farbfeld): a file whose declared type is
// one providers take inline is returned as it is when Photon decodes it, any other type makes it a PNG, and a file Photon refuses is not
// prepared. The inputs are written here: noise in each layout, some larger than the limit, some damaged.

func formatNoise(seed uint32, count int) []byte {
	state := seed*2654435761 + 9
	out := make([]byte, count)
	for i := range out {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		out[i] = byte(state >> 11)
	}
	return out
}

func farbfeldFile(width, height int, seed uint32) []byte {
	out := []byte("farbfeld")
	out = binary.BigEndian.AppendUint32(out, uint32(width))
	out = binary.BigEndian.AppendUint32(out, uint32(height))
	return append(out, formatNoise(seed, width*height*8)...)
}

func hdrFile(width, height int, seed uint32) []byte {
	out := []byte(fmt.Sprintf("#?RADIANCE\nFORMAT=32-bit_rle_rgbe\n\n-Y %d +X %d\n", height, width))
	noise := formatNoise(seed, width*height*4)
	for y := range height {
		out = append(out, 2, 2, byte(width>>8), byte(width))
		for component := range 4 {
			for start := 0; start < width; start += 128 {
				n := min(128, width-start)
				out = append(out, byte(n))
				for x := start; x < start+n; x++ {
					v := noise[(y*width+x)*4+component]
					if component == 3 {
						v = 118 + v%24
					}
					out = append(out, v)
				}
			}
		}
	}
	return out
}

func ddsFile(width, height int, fourCC string, seed uint32) []byte {
	out := []byte("DDS ")
	for _, v := range []uint32{124, 0x1 | 0x2 | 0x4 | 0x1000, uint32(height), uint32(width), 0, 0, 0} {
		out = binary.LittleEndian.AppendUint32(out, v)
	}
	out = append(out, make([]byte, 44)...)
	out = binary.LittleEndian.AppendUint32(out, 32)
	out = binary.LittleEndian.AppendUint32(out, 0x4)
	out = append(out, fourCC...)
	out = append(out, make([]byte, 20)...)
	out = binary.LittleEndian.AppendUint32(out, 0x1000)
	out = append(out, make([]byte, 16)...)
	block := 16
	if fourCC == "DXT1" {
		block = 8
	}
	return append(out, formatNoise(seed, width/4*(height/4)*block)...)
}

func icoWith(width, height int, entryImage []byte) []byte {
	out := []byte{0, 0, 1, 0, 1, 0, byte(width), byte(height), 0, 0, 1, 0, 32, 0}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(entryImage)))
	out = binary.LittleEndian.AppendUint32(out, 22)
	return append(out, entryImage...)
}

func icoBitmapFile(width, height int, seed uint32) []byte {
	dib := binary.LittleEndian.AppendUint32(nil, 40)
	dib = binary.LittleEndian.AppendUint32(dib, uint32(width))
	dib = binary.LittleEndian.AppendUint32(dib, uint32(2*height))
	dib = binary.LittleEndian.AppendUint16(dib, 1)
	dib = binary.LittleEndian.AppendUint16(dib, 32)
	dib = append(dib, make([]byte, 24)...)
	dib = append(dib, formatNoise(seed, width*height*4)...)
	dib = append(dib, formatNoise(seed+1, (width+31)/32*4*height)...)
	return icoWith(width, height, dib)
}

func icoPNGFile(width, height int, seed uint32) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	copy(img.Pix, formatNoise(seed, len(img.Pix)))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return icoWith(width, height, buf.Bytes())
}

func formatOracleCases(def tools.ImageLimits) []oracleCase {
	files := []struct {
		name string
		data []byte
	}{
		{"farbfeld", farbfeldFile(23, 11, 1)},
		{"farbfeld wider than the limit", farbfeldFile(2600, 400, 2)},
		{"hdr", hdrFile(37, 19, 3)},
		{"hdr wider than the limit", hdrFile(2600, 300, 4)},
		{"dds dxt1", ddsFile(16, 16, "DXT1", 5)},
		{"dds dxt3", ddsFile(64, 32, "DXT3", 6)},
		{"dds dxt5 wider than the limit", ddsFile(2560, 256, "DXT5", 7)},
		{"ico bitmap", icoBitmapFile(32, 32, 8)},
		{"ico png", icoPNGFile(48, 48, 9)},
		{"pnm p3", append([]byte("P3\n2 2\n255\n"), []byte("255 0 0 0 255 0 0 0 255 9 9 9\n")...)},
		{"pnm p5 16 bit", append([]byte("P5\n20 10\n65535\n"), formatNoise(10, 400)...)},
		{"pnm p6 wider than the limit", append([]byte("P6\n2600 300\n255\n"), formatNoise(11, 2600*300*3)...)},
		{"truncated hdr", hdrFile(37, 19, 3)[:300]},
		{"truncated dds", ddsFile(16, 16, "DXT1", 5)[:140]},
		{"truncated ico", icoBitmapFile(16, 16, 8)[:100]},
		{"truncated farbfeld", farbfeldFile(17, 9, 1)[:100]},
		{"truncated pnm", append([]byte("P6\n8 8\n255\n"), formatNoise(12, 100)...)},
		{"dds bc4", ddsFile(8, 8, "ATI1", 13)},
	}
	var cases []oracleCase
	for _, f := range files {
		for _, mime := range []string{"image/png", "image/x-icon", "image/jpeg"} {
			cases = append(cases,
				oracleCase{name: f.name + " as " + mime, data: f.data, mime: mime, limits: def},
				oracleCase{name: f.name + " as " + mime + " beyond a small limit", data: f.data, mime: mime, limits: tools.ImageLimits{MaxWidth: 16, MaxHeight: 16, MaxBytes: def.MaxBytes}})
		}
	}
	cases = append(cases, emptyImageOracleCases(def)...)
	return cases
}

// gifNoRows is a GIF whose screen and interlaced frame are width x 0: Photon decodes it to an image with no pixels.
func gifNoRows(width int) []byte {
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

// emptyImageOracleCases are images with a width or a height of zero, which Photon decodes. An image with no pixels is returned as it is under a
// type providers take inline (once it decodes). Under any other type Pi encodes it: the target size is at least 1x1 (`Math.max(1, Math.round(0))`,
// and `Math.min(1, maxWidth / 0)` is 1), so `resize` makes pixels of it (transparent black) and the PNG or JPEG of those is written; the PNG
// of the image itself would be an exception (`get_bytes` of a zero width or height), but it is never asked for. A byte limit nothing fits
// leaves the image unprepared at 1x1, as for any image. The farbfeld files have their header only.
func emptyImageOracleCases(def tools.ImageLimits) []oracleCase {
	var cases []oracleCase
	files := []struct {
		name string
		data []byte
	}{
		{"farbfeld 0x4", farbfeldFile(0, 4, 14)[:16]},
		{"farbfeld 4x0", farbfeldFile(4, 0, 14)[:16]},
		{"farbfeld 0x0", farbfeldFile(0, 0, 14)[:16]},
		{"farbfeld 0x3000", farbfeldFile(0, 3000, 14)[:16]},
		{"farbfeld 3000x0", farbfeldFile(3000, 0, 14)[:16]},
		{"interlaced gif 5x0", gifNoRows(5)},
		{"interlaced gif 0x0", gifNoRows(0)},
	}
	limits := []struct {
		name string
		l    tools.ImageLimits
	}{
		{"", def},
		{" with a limit of 2x2", tools.ImageLimits{MaxWidth: 2, MaxHeight: 2, MaxBytes: def.MaxBytes}},
		{" with a limit of 8 bytes", tools.ImageLimits{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 8}},
		{" with a limit of 100 bytes", tools.ImageLimits{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 100}},
		{" with a limit of 200 bytes", tools.ImageLimits{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 200}},
	}
	for _, f := range files {
		for _, mime := range []string{"image/x-icon", "application/octet-stream", "image/bmp", "image/gif", "image/png", "image/jpeg", "image/webp"} {
			for _, l := range limits {
				cases = append(cases, oracleCase{name: f.name + " as " + mime + l.name, data: f.data, mime: mime, limits: l.l})
			}
		}
	}
	return cases
}

// pngOracleCases are the real PNG files of the Photon decoder corpus (PngSuite, image-rs, Go) under declared types of their own and of others, with
// the default limits and with a limit of 16 pixels (which resizes them), and damaged copies of some of them: Photon's png crate decodes or refuses
// them, and the pixels, the resize and the encoders are the ports of Photon's, byte for byte.
func pngOracleCases(t *testing.T, def tools.ImageLimits) []oracleCase {
	t.Helper()
	files, err := filepath.Glob("../../internal/photonimage/testdata/png-real/*/*.png")
	if err != nil || len(files) < 200 {
		t.Fatalf("PNG files: %d, %v", len(files), err)
	}
	damaged := map[string]bool{"basn2c08": true, "basn6a16": true, "basi3p04": true, "tbbn3p08": true, "tbrn2c08": true, "basn0g01": true, "cs3n3p08": true, "ball": true, "basi4a16": true}
	type input struct {
		name string
		data []byte
	}
	var inputs []input
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(filepath.Dir(file)) + "/" + filepath.Base(file)
		inputs = append(inputs, input{name, data})
		for stem := range damaged {
			if !strings.HasSuffix(strings.TrimSuffix(filepath.Base(file), ".png"), stem) {
				continue
			}
			for _, cut := range []int{len(data) - 1, len(data) - 12, len(data) / 2, 70} {
				inputs = append(inputs, input{fmt.Sprintf("%s cut at %d", name, cut), data[:cut]})
			}
			flipped := slices.Clone(data)
			flipped[len(data)/2] ^= 0x55
			inputs = append(inputs, input{name + " with a byte changed", flipped})
		}
	}
	var cases []oracleCase
	for _, in := range inputs {
		for _, mime := range []string{"image/png", "image/x-icon", "application/octet-stream"} {
			cases = append(cases,
				oracleCase{name: in.name + " as " + mime, data: in.data, mime: mime, limits: def},
				oracleCase{name: in.name + " as " + mime + " beyond a small limit", data: in.data, mime: mime, limits: tools.ImageLimits{MaxWidth: 16, MaxHeight: 16, MaxBytes: def.MaxBytes}})
		}
	}
	return cases
}

func TestPhotonImagesMatchPiOnPNGFiles(t *testing.T) {
	comparePhotonToPi(t, pngOracleCases(t, tools.DEFAULT_IMAGE_LIMITS))
}
