// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"encoding/binary"
	"fmt"
)

// ddsHeader describes a DDS file's headers: the standard header with the DXT pixel format and, when dx10 is set, the extended header.
type ddsHeader struct {
	size, flags         uint32
	height, width       uint32
	pitch, depth, mips  uint32
	pixelFormatSize     uint32
	pixelFormatFlags    uint32
	fourCC              string
	caps, caps2         uint32
	dx10                bool
	dxgiFormat          uint32
	resourceDimension   uint32
	miscFlag, arraySize uint32
	miscFlags2          uint32
	magic               string
}

// newDDSHeader is a valid header for a DXT file of the size.
func newDDSHeader(width, height uint32, fourCC string) ddsHeader {
	return ddsHeader{size: 124, flags: 0x1 | 0x2 | 0x4 | 0x1000, height: height, width: width, pixelFormatSize: 32, pixelFormatFlags: 0x4,
		fourCC: fourCC, caps: 0x1000, magic: "DDS ", resourceDimension: 3, arraySize: 1}
}

func (h ddsHeader) bytes() []byte {
	out := []byte(h.magic)
	for _, v := range []uint32{h.size, h.flags, h.height, h.width, h.pitch, h.depth, h.mips} {
		out = binary.LittleEndian.AppendUint32(out, v)
	}
	out = append(out, make([]byte, 44)...) // dwReserved1[11]
	out = binary.LittleEndian.AppendUint32(out, h.pixelFormatSize)
	out = binary.LittleEndian.AppendUint32(out, h.pixelFormatFlags)
	cc := []byte(h.fourCC + "\x00\x00\x00\x00")[:4]
	out = append(out, cc...)
	for range 5 { // bit count and four masks
		out = binary.LittleEndian.AppendUint32(out, 0)
	}
	out = binary.LittleEndian.AppendUint32(out, h.caps)
	out = binary.LittleEndian.AppendUint32(out, h.caps2)
	out = append(out, make([]byte, 12)...) // dwCaps3, dwCaps4, dwReserved2
	if h.dx10 {
		for _, v := range []uint32{h.dxgiFormat, h.resourceDimension, h.miscFlag, h.arraySize, h.miscFlags2} {
			out = binary.LittleEndian.AppendUint32(out, v)
		}
	}
	return out
}

func (h ddsHeader) file(blocks []byte) []byte { return append(h.bytes(), blocks...) }

// ddsBlockBytes is the size of a block of a variant.
func ddsBlockBytes(fourCC string) int {
	if fourCC == "DXT1" {
		return 8
	}
	return 16
}

// ddsNoise is count bytes of a xorshift sequence.
func ddsNoise(seed uint32, count int) []byte {
	state := seed*2654435761 + 5
	out := make([]byte, count)
	for i := range out {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		out[i] = byte(state >> 9)
	}
	return out
}

// ddsColorBlock is an 8-byte colour block with the endpoints and the indices 0, 1, 2, 3 repeated along every row.
func ddsColorBlock(c0, c1 uint16) []byte {
	return []byte{byte(c0), byte(c0 >> 8), byte(c1), byte(c1 >> 8), 0xE4, 0xE4, 0xE4, 0xE4}
}

// ddsBlock is a block of the variant with the colour endpoints and a default alpha part.
func ddsBlock(fourCC string, c0, c1 uint16, a0, a1 byte) []byte {
	switch fourCC {
	case "DXT1":
		return ddsColorBlock(c0, c1)
	case "DXT3":
		alpha := []byte{0x10, 0x32, 0x54, 0x76, 0x98, 0xBA, 0xDC, 0xFE}
		return append(alpha, ddsColorBlock(c0, c1)...)
	}
	// Alpha indices: pixel i takes index i mod 8 (three bits each, the 16 pixels fill 48 bits).
	var bits uint64
	for i := range 16 {
		bits |= uint64(i%8) << (3 * i)
	}
	out := []byte{a0, a1}
	for i := range 6 {
		out = append(out, byte(bits>>(8*i)))
	}
	return append(out, ddsColorBlock(c0, c1)...)
}

// ddsBlocks is a grid of blocks of noise.
func ddsNoiseBlocks(seed uint32, fourCC string, width, height int) []byte {
	return ddsNoise(seed, (width/4)*(height/4)*ddsBlockBytes(fourCC))
}

// ddsCorpus is the files whose decoding the test compares with Photon: every block interpolation (all 5-bit and 6-bit endpoint pairs, both
// orders, every alpha endpoint pair of DXT5, every DXT3 alpha nibble), the layout of blocks in rows, noise, DX10 headers for every DXGI
// format and each header field the decoder validates, header damage, truncations, sizes (not a multiple of 4, empty) and the limits.
func ddsCorpus() []formatCase {
	var b formatCorpusBuilder
	variants := []string{"DXT1", "DXT3", "DXT5"}

	// Colour endpoints: each channel's pairs in turn (the interpolation is per channel), in both orders of the 16-bit values.
	for _, variant := range variants {
		var red, green, blue []byte
		for a := range 32 {
			for c := range 32 {
				red = append(red, ddsBlock(variant, uint16(a<<11), uint16(c<<11), 200, 100)...)
				blue = append(blue, ddsBlock(variant, uint16(a), uint16(c), 100, 200)...)
			}
		}
		for a := range 64 {
			for c := range 64 {
				green = append(green, ddsBlock(variant, uint16(a<<5), uint16(c<<5), 255, 0)...)
			}
		}
		for name, blocks := range map[string][]byte{"red": red, "green": green, "blue": blue} {
			n := len(blocks) / ddsBlockBytes(variant)
			b.add("endpoints-"+name+"-"+variant, newDDSHeader(uint32(4*n), 4, variant).file(blocks))
			// The same blocks as a grid 64 blocks wide.
			b.add("endpoints-"+name+"-grid-"+variant, newDDSHeader(256, uint32(4*(n/64)), variant).file(blocks[:(n/64)*64*ddsBlockBytes(variant)]))
		}
		// Every 16-bit endpoint c0 against a few c1 (the order of the two decides the interpolation of DXT1).
		for _, c1 := range []uint16{0x0000, 0x0001, 0x07E0, 0x8410, 0xF800, 0xFFFF, 0x1234} {
			var blocks []byte
			for c0 := range 65536 {
				blocks = append(blocks, ddsBlock(variant, uint16(c0), c1, byte(c0), byte(c0>>8))...)
			}
			b.add(fmt.Sprintf("endpoints-c0-all-c1-%04x-%s", c1, variant), newDDSHeader(1024, 4*256, variant).file(blocks))
		}
	}
	// Both endpoints equal, and endpoints one apart (the DXT1 comparison is `color0 > color1`).
	{
		var blocks []byte
		for c := range 65535 {
			blocks = append(blocks, ddsColorBlock(uint16(c), uint16(c))...)
			blocks = append(blocks, ddsColorBlock(uint16(c), uint16(c+1))...)
			blocks = append(blocks, ddsColorBlock(uint16(c+1), uint16(c))...)
		}
		blocks = blocks[:len(blocks)/8/256*256*8]
		b.add("endpoints-neighbours-DXT1", newDDSHeader(1024, uint32(4*(len(blocks)/8/256)), "DXT1").file(blocks))
	}
	// DXT5 alpha: every (alpha0, alpha1) pair against all eight indices; DXT3 alpha: every nibble in every position.
	{
		var blocks []byte
		for a0 := range 256 {
			for a1 := range 256 {
				blocks = append(blocks, ddsBlock("DXT5", 0xF800, 0x001F, byte(a0), byte(a1))...)
			}
		}
		b.add("alpha-dxt5-all-pairs", newDDSHeader(1024, 4*256, "DXT5").file(blocks))
		var dxt3 []byte
		noise := ddsNoise(3, 4096*8)
		for i := range 4096 {
			block := append([]byte(nil), noise[i*8:(i+1)*8]...)
			dxt3 = append(dxt3, append(block, ddsColorBlock(0xF800, 0x001F)...)...)
		}
		b.add("alpha-dxt3-noise", newDDSHeader(256, 4*64, "DXT3").file(dxt3))
		b.add("alpha-dxt3-all-nibbles", newDDSHeader(64, 4, "DXT3").file(func() []byte {
			var out []byte
			for v := range 16 {
				nibbles := make([]byte, 8)
				for i := range nibbles {
					nibbles[i] = byte(v)<<4 | byte(v)
				}
				out = append(out, append(nibbles, ddsColorBlock(0xFFFF, 0)...)...)
			}
			return out
		}()))
	}

	// Noise and layouts.
	for _, variant := range variants {
		for _, size := range [][2]int{{4, 4}, {8, 4}, {4, 8}, {12, 8}, {16, 16}, {64, 32}, {100, 36}, {256, 4}, {4, 256}, {1024, 8}} {
			b.add(fmt.Sprintf("noise-%s-%dx%d", variant, size[0], size[1]), newDDSHeader(uint32(size[0]), uint32(size[1]), variant).file(ddsNoiseBlocks(uint32(size[0]*7+size[1]), variant, size[0], size[1])))
		}
		// Extra data after the first level (mipmaps, trailing bytes) is ignored.
		h := newDDSHeader(16, 16, variant)
		h.mips = 5
		b.add("mipmaps-"+variant, h.file(append(ddsNoiseBlocks(1, variant, 16, 16), ddsNoise(2, 5000)...)))
		b.add("trailing-byte-"+variant, h.file(append(ddsNoiseBlocks(1, variant, 16, 16), 7)))
	}

	// Sizes: a multiple of 4 is required, zero is allowed.
	for _, variant := range variants {
		for _, size := range [][2]uint32{{0, 0}, {0, 4}, {4, 0}, {0, 8}, {0, 100}, {100, 0}, {1, 1}, {2, 4}, {4, 2}, {5, 4}, {4, 5}, {6, 6}, {7, 8}, {3, 3}, {4, 7}, {8, 6},
			{4294967292, 0}, {0, 4294967292}, {4294967295, 0}, {4294967292, 4294967292}, {1073741824, 0}, {1073741820, 0}, {1073741816, 0}, {2147483648, 0}, {0, 1073741824}} {
			b.add(fmt.Sprintf("size-%s-%dx%d", variant, size[0], size[1]), newDDSHeader(size[0], size[1], variant).file(ddsNoise(4, 64)))
		}
	}

	// The standard header.
	base := func() ddsHeader { return newDDSHeader(8, 8, "DXT1") }
	blocksFor := func(h ddsHeader) []byte { return ddsNoiseBlocks(9, h.fourCC, int(h.width), int(h.height)) }
	damaged := func(name string, mutate func(h *ddsHeader)) {
		h := base()
		mutate(&h)
		b.add("header-"+name, h.file(blocksFor(base())))
	}
	damaged("valid", func(h *ddsHeader) {})
	damaged("magic-lowercase", func(h *ddsHeader) { h.magic = "dds " })
	damaged("magic-no-space", func(h *ddsHeader) { h.magic = "DDS\x00" })
	damaged("size-123", func(h *ddsHeader) { h.size = 123 })
	damaged("size-125", func(h *ddsHeader) { h.size = 125 })
	damaged("size-0", func(h *ddsHeader) { h.size = 0 })
	for _, flags := range []uint32{0, 0x1, 0x7, 0x1007, 0x1006, 0x1005, 0x1003, 0x1007 | 0x8, 0x1007 | 0x20000, 0x1007 | 0x80000, 0x1007 | 0x800000, 0x1007 | 0x10, 0x1007 | 0x40, 0x1007 | 0x100000,
		0x1007 | 0x80000000, 0x1007 | 0x2000, 0x1007 | 0x4000, 0x1007 | 0x1000000, 0xFFFFFFFF, 0x1007 | 0x20000 | 0x80000 | 0x800000 | 0x8} {
		flags := flags
		damaged(fmt.Sprintf("flags-%08x", flags), func(h *ddsHeader) { h.flags = flags })
	}
	damaged("pixel-format-size-31", func(h *ddsHeader) { h.pixelFormatSize = 31 })
	damaged("pixel-format-size-33", func(h *ddsHeader) { h.pixelFormatSize = 33 })
	damaged("pixel-format-size-0", func(h *ddsHeader) { h.pixelFormatSize = 0 })
	for _, flags := range []uint32{0, 0x1, 0x2, 0x4, 0x5, 0x6, 0x40, 0x41, 0x44, 0x200, 0x204, 0x20000, 0xFFFFFFFB, 0xFFFFFFFF} {
		flags := flags
		damaged(fmt.Sprintf("pixel-format-flags-%08x", flags), func(h *ddsHeader) { h.pixelFormatFlags = flags })
	}
	for _, cc := range []string{"DXT1", "DXT2", "DXT3", "DXT4", "DXT5", "dxt1", "DXt1", "ATI1", "ATI2", "BC4U", "BC5U", "DX10", "DXT0", "DXT6", "RGBG", "GRGB", "UYVY", "YUY2", "\x00\x00\x00\x00", "DXT\x00", " XT1", "DXT1\x00"} {
		cc := cc
		damaged("fourcc-"+fmt.Sprintf("%x", cc), func(h *ddsHeader) { h.fourCC = cc })
	}
	damaged("caps-zero", func(h *ddsHeader) { h.caps = 0 })
	damaged("caps2-cubemap", func(h *ddsHeader) { h.caps2 = 0xFE00 })
	damaged("mips-100", func(h *ddsHeader) { h.mips = 100 })
	damaged("pitch-garbage", func(h *ddsHeader) { h.pitch = 0xDEADBEEF; h.depth = 0xFFFFFFFF })
	damaged("rgb-pixel-format", func(h *ddsHeader) { h.pixelFormatFlags = 0x41; h.fourCC = "" })

	// DX10 headers.
	dx10 := func(name string, mutate func(h *ddsHeader)) {
		h := base()
		h.fourCC = "DX10"
		h.dx10 = true
		h.dxgiFormat = 71
		mutate(&h)
		variant := "DXT1"
		switch {
		case h.dxgiFormat >= 73 && h.dxgiFormat <= 75:
			variant = "DXT3"
		case h.dxgiFormat >= 76 && h.dxgiFormat <= 78:
			variant = "DXT5"
		}
		b.add("dx10-"+name, h.file(ddsNoiseBlocks(10, variant, int(h.width), int(h.height))))
	}
	for format := uint32(0); format <= 140; format++ {
		format := format
		dx10(fmt.Sprintf("dxgi-%d", format), func(h *ddsHeader) { h.dxgiFormat = format })
	}
	for _, format := range []uint32{255, 256, 1000, 0xFFFFFFFF, 0x80000000} {
		format := format
		dx10(fmt.Sprintf("dxgi-%d", format), func(h *ddsHeader) { h.dxgiFormat = format })
	}
	for dim := uint32(0); dim <= 6; dim++ {
		dim := dim
		dx10(fmt.Sprintf("dimension-%d", dim), func(h *ddsHeader) { h.resourceDimension = dim })
	}
	dx10("dimension-max", func(h *ddsHeader) { h.resourceDimension = 0xFFFFFFFF })
	for _, flag := range []uint32{0, 1, 2, 3, 4, 5, 8, 0x10, 0xFFFFFFFF} {
		flag := flag
		dx10(fmt.Sprintf("misc-flag-%d", flag), func(h *ddsHeader) { h.miscFlag = flag })
	}
	for _, size := range []uint32{0, 1, 2, 6, 0xFFFFFFFF} {
		size := size
		dx10(fmt.Sprintf("array-size-%d-2d", size), func(h *ddsHeader) { h.arraySize = size })
		dx10(fmt.Sprintf("array-size-%d-3d", size), func(h *ddsHeader) { h.arraySize = size; h.resourceDimension = 4 })
		dx10(fmt.Sprintf("array-size-%d-1d", size), func(h *ddsHeader) { h.arraySize = size; h.resourceDimension = 2 })
	}
	for _, flag := range []uint32{0, 1, 2, 3, 4, 5, 6, 0xFFFFFFFF} {
		flag := flag
		dx10(fmt.Sprintf("misc-flags-2-%d", flag), func(h *ddsHeader) { h.miscFlags2 = flag })
	}
	dx10("bc3", func(h *ddsHeader) { h.dxgiFormat = 77 })
	dx10("without-fourcc-flag", func(h *ddsHeader) { h.pixelFormatFlags = 0 })
	// A DX10 header on a file whose FourCC says DXT1 is not read (the 20 bytes are pixel data), and the reverse.
	{
		h := base()
		h.dx10 = true
		h.dxgiFormat = 77
		b.add("dx10-header-without-dx10-fourcc", h.file(ddsNoiseBlocks(11, "DXT1", 8, 8)))
		h = base()
		h.fourCC = "DX10"
		b.add("dx10-fourcc-without-dx10-header", h.file(ddsNoiseBlocks(11, "DXT1", 8, 8)))
	}

	// Truncations: every prefix of a small file of each variant, and of a DX10 one.
	for _, variant := range variants {
		h := newDDSHeader(8, 4, variant)
		file := h.file(ddsNoiseBlocks(21, variant, 8, 4))
		for n := range len(file) {
			b.add(fmt.Sprintf("truncated-%s-%03d", variant, n), file[:n:n])
		}
	}
	{
		h := base()
		h.fourCC = "DX10"
		h.dx10 = true
		h.dxgiFormat = 72
		h.width, h.height = 4, 4
		file := h.file(ddsNoiseBlocks(22, "DXT1", 4, 4))
		for n := range len(file) {
			b.add(fmt.Sprintf("truncated-dx10-%03d", n), file[:n:n])
		}
	}
	// A short second row of blocks.
	{
		h := newDDSHeader(8, 8, "DXT5")
		full := ddsNoiseBlocks(23, "DXT5", 8, 8)
		for _, n := range []int{len(full) - 1, len(full) - 16, len(full) - 17, len(full)/2 + 1, len(full) / 2} {
			b.add(fmt.Sprintf("short-rows-%d", n), h.file(full[:n]))
		}
	}

	// Limits: width * height * bytes per pixel (3 for DXT1, 4 for the others) may not exceed 2^29.
	limit := func(name, variant string, width, height uint32, data bool) {
		h := newDDSHeader(width, height, variant)
		if !data {
			b.add(name, h.file(ddsNoise(5, 256)))
			return
		}
		b.addLazy(name, func() []byte {
			blocks := make([]byte, int(width/4)*int(height/4)*ddsBlockBytes(variant))
			copy(blocks, ddsNoise(6, 4096))
			for i := 4096; i < len(blocks); i += 4099 {
				blocks[i] = byte(i >> 5)
			}
			return h.file(blocks)
		})
	}
	limit("limit-dxt1-over", "DXT1", 16384, 10924, false)
	limit("limit-dxt1-over-with-data", "DXT1", 16384, 10924, true)
	limit("limit-dxt1-exact-ish", "DXT1", 16384, 10920, true)
	limit("limit-dxt5-exact", "DXT5", 16384, 8192, true)
	limit("limit-dxt5-over", "DXT5", 16384, 8196, true)
	limit("limit-dxt5-over-header-only", "DXT5", 16384, 8196, false)
	limit("limit-dxt3-over", "DXT3", 65536, 2052, false)
	limit("limit-dxt3-exact-no-data", "DXT3", 65536, 2048, false)
	limit("limit-dxt1-u32", "DXT1", 4294967292, 4294967292, false)
	limit("limit-dxt1-wide", "DXT1", 4294967292, 4, false)
	limit("limit-dxt5-wide", "DXT5", 536870912, 4, false)
	limit("limit-dxt5-wide-over", "DXT5", 536870916, 4, false)
	limit("limit-dxt1-wide-exact-no-data", "DXT1", 178956968, 4, false)
	limit("limit-dxt1-wide-over", "DXT1", 178956972, 4, false)
	return b.cases
}
