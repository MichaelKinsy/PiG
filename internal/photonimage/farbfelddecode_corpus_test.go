// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"encoding/binary"
	"fmt"
)

// farbfeldFile writes a farbfeld file: the magic, the big endian width and height and big endian 16-bit RGBA samples.
func farbfeldFile(width, height uint32, samples []uint16) []byte {
	out := make([]byte, 0, 16+2*len(samples))
	out = append(out, "farbfeld"...)
	out = binary.BigEndian.AppendUint32(out, width)
	out = binary.BigEndian.AppendUint32(out, height)
	for _, s := range samples {
		out = binary.BigEndian.AppendUint16(out, s)
	}
	return out
}

// farbfeldNoise is count 16-bit samples of a xorshift sequence.
func farbfeldNoise(seed uint32, count int) []uint16 {
	state := seed*2654435761 + 1
	samples := make([]uint16, count)
	for i := range samples {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		samples[i] = uint16(state >> 8)
	}
	return samples
}

// farbfeldCorpus is the files whose decoding the test compares with Photon: every sample value that rounds differently in the 16- to
// 8-bit conversion, the sizes, empty images, trailing data, every truncation of a small file, damaged headers and the 512 MiB limit.
func farbfeldCorpus() []formatCase {
	var b formatCorpusBuilder

	// Sizes with noise.
	for _, size := range [][2]uint32{{1, 1}, {2, 1}, {1, 2}, {3, 2}, {17, 5}, {64, 64}, {255, 3}, {256, 2}, {1000, 3}} {
		b.add(fmt.Sprintf("noise-%dx%d", size[0], size[1]), farbfeldFile(size[0], size[1], farbfeldNoise(size[0]*31+size[1], int(size[0]*size[1]*4))))
	}

	// Each 16-bit value of one channel, the other channels fixed: the conversion `(v + 128) / 257` is exact at the rounding edges and the
	// whole range is compared (65536 pixels per file, one file per channel).
	for channel := range 4 {
		samples := make([]uint16, 4*65536)
		for v := range 65536 {
			for c := range 4 {
				switch c {
				case channel:
					samples[4*v+c] = uint16(v)
				default:
					samples[4*v+c] = uint16(v * 7 * (c + 1))
				}
			}
		}
		b.add(fmt.Sprintf("ramp-channel%d", channel), farbfeldFile(256, 256, samples))
	}
	b.add("extremes", farbfeldFile(4, 2, []uint16{
		0, 0, 0, 0, 0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF, 0x7F, 0x80, 0x81, 0xFF,
		0x100, 0x101, 0x17F, 0x180, 0x7FFF, 0x8000, 0x8001, 0xFEFE, 0xFEFF, 0xFF00, 0xFF01, 0x7FFE,
		0x1, 0x2, 0x3, 0x4, 0xFFFE, 0xFFFD, 0xFF7F, 0xFF80,
	}))

	// Empty images: Photon makes a PhotonImage of width or height zero.
	b.add("zero-0x0", farbfeldFile(0, 0, nil))
	b.add("zero-0x5", farbfeldFile(0, 5, nil))
	b.add("zero-5x0", farbfeldFile(5, 0, nil))
	b.add("zero-0x5-with-data", farbfeldFile(0, 5, farbfeldNoise(1, 40)))
	b.add("zero-4294967295x0", farbfeldFile(0xFFFFFFFF, 0, nil))
	b.add("zero-0x4294967295", farbfeldFile(0, 0xFFFFFFFF, nil))

	// Trailing data after the pixels is not read.
	body := farbfeldNoise(5, 3*2*4)
	b.add("trailing-byte", append(farbfeldFile(3, 2, body), 0x7))
	b.add("trailing-sample", append(farbfeldFile(3, 2, body), 1, 2, 3, 4, 5, 6, 7, 8, 9))

	// Every truncation of a small file, whole samples and half samples.
	whole := farbfeldFile(2, 2, farbfeldNoise(9, 16))
	for n := range len(whole) {
		b.add(fmt.Sprintf("truncated-%03d", n), whole[:n:n])
	}

	// Magic and header damage. Only "farbfeld" is a farbfeld to image's guess_format; the rest of the signature is the whole check.
	b.add("magic-only", []byte("farbfeld"))
	b.add("magic-and-one-dimension", append([]byte("farbfeld"), 0, 0, 0, 1))
	b.add("magic-and-short-height", append([]byte("farbfeld"), 0, 0, 0, 1, 0, 0, 0))
	b.add("magic-prefix-only", []byte("farbfel"))
	b.add("magic-capitalised", append([]byte("Farbfeld"), farbfeldFile(1, 1, []uint16{1, 2, 3, 4})[8:]...))
	damaged := farbfeldFile(1, 1, []uint16{1, 2, 3, 4})
	damaged[7] = 'D'
	b.add("magic-wrong-last-byte", damaged)
	b.add("magic-with-prefix-space", append([]byte(" "), farbfeldFile(1, 1, []uint16{1, 2, 3, 4})...))

	// Dimensions the 512 MiB limit of image's `Limits::default` decides: total_bytes = width * height * 8 may not exceed 2^29, which is
	// reserved before the decoder reads a sample.
	b.add("limit-over-by-one-row", farbfeldFile(1<<26, 2, nil))
	b.add("limit-over-8193x8192", farbfeldFile(8193, 8192, nil))
	b.add("limit-over-4294967295x4294967295", farbfeldFile(0xFFFFFFFF, 0xFFFFFFFF, nil))
	b.add("limit-over-4294967295x1", farbfeldFile(0xFFFFFFFF, 1, nil))
	b.add("limit-exact-1x67108864-no-data", farbfeldFile(1, 1<<26, nil))
	b.add("limit-exact-8192x8192-no-data", farbfeldFile(8192, 8192, nil))
	b.add("limit-exact-16384x4096-no-data", farbfeldFile(16384, 4096, nil))
	b.add("limit-exact-16384x4096-short-data", farbfeldFile(16384, 4096, farbfeldNoise(3, 4096)))
	b.addLazy("limit-exact-16384x4096", func() []byte {
		out := farbfeldFile(16384, 4096, nil)
		out = append(out, make([]byte, 1<<29)...)
		for i := 16; i < len(out); i += 4099 {
			out[i] = byte(i >> 7)
		}
		return out
	})
	b.addLazy("limit-just-under-16384x4095", func() []byte {
		out := farbfeldFile(16384, 4095, nil)
		out = append(out, make([]byte, 16384*4095*8)...)
		for i := 16; i < len(out); i += 8209 {
			out[i] = byte(i >> 9)
		}
		return out
	})
	return b.cases
}
