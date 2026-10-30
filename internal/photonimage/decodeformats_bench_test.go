// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"fmt"
	"testing"
)

// BenchmarkDecodeFormats is the decoders of ICO, PNM, DDS, Radiance HDR and farbfeld on a 1920x1080 picture (256x256 for ICO, the size of
// its largest entry). The throughput is the size of the file.
func BenchmarkDecodeFormats(b *testing.B) {
	const width, height = 1920, 1080
	rows := make([][]hdrPixel, height)
	for y := range rows {
		rows[y] = hdrSafeFirst(hdrNoise(uint32(y), width, 118, 140))
	}
	ico := newDIB(256, 256, 32)
	ico.pixels = icoNoise(1, 256*256*4)
	ico.mask = dibMask(256, 256, 2)
	farbfeldPixels := farbfeldNoise(3, width*height*4)
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"ico-bmp32-256x256", icoSingle(256, 256, 32, ico.bytes())},
		{"pnm-p6-1920x1080", append([]byte(fmt.Sprintf("P6\n%d %d\n255\n", width, height)), pnmNoise(4, width*height*3)...)},
		{"pnm-p3-ascii-640x360", append([]byte("P3\n640 360\n255\n"), pnmASCII(func() []int {
			v := make([]int, 640*360*3)
			for i, n := range pnmNoise(5, len(v)) {
				v[i] = int(n)
			}
			return v
		}(), 12)...)},
		{"dds-dxt5-1920x1080", newDDSHeader(width, height, "DXT5").file(ddsNoiseBlocks(6, "DXT5", width, height))},
		{"dds-dxt1-1920x1080", newDDSHeader(width, height, "DXT1").file(ddsNoiseBlocks(7, "DXT1", width, height))},
		{"hdr-new-rle-1920x1080", hdrNewRLE(hdrHeader("FORMAT=32-bit_rle_rgbe\n", width, height), rows...)},
		{"farbfeld-1920x1080", farbfeldFile(width, height, farbfeldPixels)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			if _, err := Decode(tc.data); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.data)))
			for b.Loop() {
				if _, err := Decode(tc.data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
