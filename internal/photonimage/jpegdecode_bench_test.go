// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"image/jpeg"
	"sync"
	"testing"
)

// jdbInputs are the inputs of BenchmarkDecodeJPEG, built once: a 12 MP photograph (4:2:0 and 4:4:4 baseline, 4:2:0 progressive), and
// a screenshot-sized one.
var jdbInputs = sync.OnceValue(func() map[string][]byte {
	var b jdcBuilder
	inputs := map[string][]byte{}
	build := func(name, kind string, w, h int, sampling string, progressive bool) {
		s := jdcSamplingSpec(w, h, sampling, jdcYCbCrIDs)
		s.fillImage(b.image(kind, w, h), "ycbcr")
		s.scans = jdcSeqInterleaved(3)
		if progressive {
			s.sof = 0xC2
			s.scans = jdcProgScript(3, 0, true)
		}
		inputs[name] = s.build()
	}
	build("12mp-420", "photo", 4000, 3000, "420", false)
	build("12mp-444", "photo", 4000, 3000, "444", false)
	build("12mp-420-progressive", "photo", 4000, 3000, "420", true)
	build("screenshot-1920x1080-444", "screenshot", 1920, 1080, "444", false)
	build("screenshot-2559x1439-420", "screenshot", 2559, 1439, "420", false)
	return inputs
})

func BenchmarkDecodeJPEG(b *testing.B) {
	inputs := jdbInputs()
	for _, name := range []string{"12mp-420", "12mp-444", "12mp-420-progressive", "screenshot-1920x1080-444", "screenshot-2559x1439-420"} {
		data := inputs[name]
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for range b.N {
				if _, err := DecodeJPEG(data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDecodeJPEGGo is Go's image/jpeg on the same baseline inputs (the pixels differ from Photon's; this is the speed to compare
// with).
func BenchmarkDecodeJPEGGo(b *testing.B) {
	inputs := jdbInputs()
	for _, name := range []string{"12mp-420", "12mp-444", "12mp-420-progressive", "screenshot-1920x1080-444", "screenshot-2559x1439-420"} {
		data := inputs[name]
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for range b.N {
				if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
