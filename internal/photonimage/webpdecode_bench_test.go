// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"fmt"
	"testing"

	xwebp "golang.org/x/image/webp"
)

// webpBenchLossy is a lossy file of the size of a photo or a screenshot: a normal loop filter, segments, one skip probability, partitions.
func webpBenchLossy(width, height int) []byte {
	rng := newWebPRNG(41)
	g := randomWebPLossy(rng, width, height, 77)
	g.segments, g.updateMap, g.updateData, g.absolute = true, true, true, true
	g.quant = [4]int{20, 30, 40, 50}
	g.quantUpdate = [4]bool{true, true, true, true}
	g.lf = [4]int{20, 24, 28, 32}
	g.lfUpdate = [4]bool{true, true, true, true}
	g.simple, g.level, g.sharpness, g.lfAdjust = false, 24, 2, false
	g.partitionsLog2, g.skipProb, g.tokenUpdatePct = 2, 60, 0
	mbs := ((width + 15) / 16) * ((height + 15) / 16)
	g.headerExtra, g.partitionBytes = mbs*14, mbs*30/4
	return webpLossyFile(g)
}

// webpBenchLossless is a lossless file with the transforms, color cache and meta prefix codes of an encoder.
func webpBenchLossless(width, height int) []byte {
	rng := newWebPRNG(43)
	g := randomWebPLossless(rng, 0)
	g.transforms = []int{2, 0, 1}
	g.predBits, g.colorBits, g.cacheBits, g.metaBits, g.numGroups = 4, 5, 8, 5, 6
	g.backrefPct, g.cachePct, g.maxLength, g.hostileDist = 25, 20, 40, 0
	return webpRIFF(webpChunkBytes("VP8L", g.chunk(width, height, true)))
}

func BenchmarkDecodeWebP(b *testing.B) {
	for _, size := range [][2]int{{1920, 1080}, {1280, 800}} {
		for _, kind := range []string{"lossy", "lossless"} {
			var data []byte
			if kind == "lossy" {
				data = webpBenchLossy(size[0], size[1])
			} else {
				data = webpBenchLossless(size[0], size[1])
			}
			name := fmt.Sprintf("%s-%dx%d", kind, size[0], size[1])
			b.Run("photonimage/"+name, func(b *testing.B) {
				b.SetBytes(int64(size[0] * size[1] * 4))
				b.ReportAllocs()
				for range b.N {
					if _, err := DecodeWebP(data); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("x-image/"+name, func(b *testing.B) {
				b.SetBytes(int64(size[0] * size[1] * 4))
				b.ReportAllocs()
				for range b.N {
					if _, err := xwebp.Decode(bytes.NewReader(data)); err != nil {
						b.Skip(err)
					}
				}
			})
		}
	}
}
