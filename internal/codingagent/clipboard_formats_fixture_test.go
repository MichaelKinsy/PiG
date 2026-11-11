package codingagent

import (
	"encoding/binary"
	"fmt"
)

// Small files of the formats Photon decodes and Go's standard library does not, for the clipboard normalization test: Pi's convertToPng
// hands the bytes of any other target than PNG, JPEG, GIF and WebP to Photon.

// clipboardFormatFixture writes a file of the format with noise for pixels: ico (3x2, a 32-bit bitmap with an AND mask), dds (8x4,
// DXT1), pnm (3x2, P6), hdr (8x2, new-style run length encoding) and farbfeld (3x2).
func clipboardFormatFixture(kind string) []byte {
	noise := func(count int) []byte {
		out := make([]byte, count)
		state := uint32(2463534242)
		for i := range out {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			out[i] = byte(state >> 11)
		}
		return out
	}
	switch kind {
	case "ico":
		dib := binary.LittleEndian.AppendUint32(nil, 40)
		dib = binary.LittleEndian.AppendUint32(dib, 3)
		dib = binary.LittleEndian.AppendUint32(dib, 4)
		dib = binary.LittleEndian.AppendUint16(dib, 1)
		dib = binary.LittleEndian.AppendUint16(dib, 32)
		dib = append(dib, make([]byte, 24)...)
		dib = append(dib, noise(3*2*4)...)
		dib = append(dib, 0x40, 0, 0, 0, 0x20, 0, 0, 0)
		out := []byte{0, 0, 1, 0, 1, 0, 3, 2, 0, 0, 1, 0, 32, 0}
		out = binary.LittleEndian.AppendUint32(out, uint32(len(dib)))
		out = binary.LittleEndian.AppendUint32(out, 22)
		return append(out, dib...)
	case "dds":
		out := []byte("DDS ")
		for _, v := range []uint32{124, 0x1 | 0x2 | 0x4 | 0x1000, 4, 8, 0, 0, 0} {
			out = binary.LittleEndian.AppendUint32(out, v)
		}
		out = append(out, make([]byte, 44)...)
		out = binary.LittleEndian.AppendUint32(out, 32)
		out = binary.LittleEndian.AppendUint32(out, 0x4)
		out = append(out, "DXT1"...)
		out = append(out, make([]byte, 20)...)
		out = binary.LittleEndian.AppendUint32(out, 0x1000)
		out = append(out, make([]byte, 16)...)
		return append(out, noise(2*8)...)
	case "pnm":
		return append([]byte("P6\n3 2\n255\n"), noise(18)...)
	case "hdr":
		out := []byte("#?RADIANCE\nFORMAT=32-bit_rle_rgbe\n\n-Y 2 +X 8\n")
		samples := noise(8 * 2 * 4)
		for y := range 2 {
			out = append(out, 2, 2, 0, 8)
			for component := range 4 {
				out = append(out, 8)
				for x := range 8 {
					v := samples[(y*8+x)*4+component]
					if component == 3 {
						v = 120 + v%16
					}
					out = append(out, v)
				}
			}
		}
		return out
	case "farbfeld":
		out := []byte("farbfeld")
		out = binary.BigEndian.AppendUint32(out, 3)
		out = binary.BigEndian.AppendUint32(out, 2)
		return append(out, noise(3*2*8)...)
	}
	panic(fmt.Sprintf("no fixture for %q", kind))
}
