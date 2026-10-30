package images_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"

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
		{"farbfeld with no pixels", farbfeldFile(0, 4, 14)[:16]},
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
	return cases
}
