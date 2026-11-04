// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package imageprocessing

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// formatNoise is count bytes of a xorshift sequence.
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

func farbfeldInput(width, height int, seed uint32) []byte {
	out := []byte("farbfeld")
	out = binary.BigEndian.AppendUint32(out, uint32(width))
	out = binary.BigEndian.AppendUint32(out, uint32(height))
	return append(out, formatNoise(seed, width*height*8)...)
}

// hdrInput writes an RGBE image with new-style run length encoded scanlines of literal blocks.
func hdrInput(width, height int, seed uint32) []byte {
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
						v = 118 + v%24 // exponents that give visible values
					}
					out = append(out, v)
				}
			}
		}
	}
	return out
}

func ddsInput(width, height int, fourCC string, seed uint32) []byte {
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

// icoBitmapInput is an ICO file of one 32-bit bitmap with an AND mask.
func icoBitmapInput(width, height int, seed uint32) []byte {
	dib := binary.LittleEndian.AppendUint32(nil, 40)
	dib = binary.LittleEndian.AppendUint32(dib, uint32(width))
	dib = binary.LittleEndian.AppendUint32(dib, uint32(2*height))
	dib = binary.LittleEndian.AppendUint16(dib, 1)
	dib = binary.LittleEndian.AppendUint16(dib, 32)
	dib = append(dib, make([]byte, 24)...)
	dib = append(dib, formatNoise(seed, width*height*4)...)
	dib = append(dib, formatNoise(seed+1, (width+31)/32*4*height)...)
	return icoOf(width, height, dib)
}

func icoOf(width, height int, image []byte) []byte {
	out := []byte{0, 0, 1, 0, 1, 0, byte(width), byte(height), 0, 0, 1, 0, 32, 0}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(image)))
	out = binary.LittleEndian.AppendUint32(out, 22)
	return append(out, image...)
}

func icoPNGInput(width, height int, seed uint32) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	copy(img.Pix, formatNoise(seed, len(img.Pix)))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return icoOf(width, height, buf.Bytes())
}

// TestProcessImageDecodedFormatsMatchPi runs image-process.ts processImage and PiG's ProcessImage over the formats Photon decodes and Go's
// standard library does not (ICO, DDS, PNM, Radiance HDR and farbfeld), under declared types of their own, of the four inline types and of
// none: Pi hands the bytes of anything but PNG, JPEG, GIF and WebP to Photon through convertImageBytesToPng, and the bytes of those four to
// Photon's resize, which guesses the format from the bytes. Whether an image is returned, its type, the hints, the omission message and the
// returned bytes must be Pi's. Some inputs are larger than the resize limits, some are damaged.
func TestProcessImageDecodedFormatsMatchPi(t *testing.T) {
	var inputs []namedImage
	add := func(name string, data []byte) { inputs = append(inputs, namedImage{name, data}) }
	add("farbfeld-17x9", farbfeldInput(17, 9, 1))
	add("farbfeld-2050x120", farbfeldInput(2050, 120, 2))
	add("farbfeld-0x4", farbfeldInput(0, 4, 3)[:16])
	add("hdr-37x19", hdrInput(37, 19, 4))
	add("hdr-300x200", hdrInput(300, 200, 5))
	add("hdr-2050x120", hdrInput(2050, 120, 6))
	add("dds-dxt1-16x16", ddsInput(16, 16, "DXT1", 7))
	add("dds-dxt3-64x32", ddsInput(64, 32, "DXT3", 8))
	add("dds-dxt5-2052x128", ddsInput(2052, 128, "DXT5", 9))
	add("ico-bmp-16x16", icoBitmapInput(16, 16, 10))
	add("ico-bmp-256x256", icoBitmapInput(256, 256, 11))
	add("ico-png-32x32", icoPNGInput(32, 32, 12))
	add("ico-png-300x300", icoPNGInput(300, 300, 13))
	add("pnm-p1", append([]byte("P1\n5 3\n"), []byte("1 0 1 0 1\n0 1 0 1 0\n1 1 0 0 1\n")...))
	add("pnm-p2-maxval-100", append([]byte("P2\n4 2\n100\n"), []byte("0 10 50 100\n7 33 66 99\n")...))
	add("pnm-p3-16bit", append([]byte("P3\n2 1\n65535\n"), []byte("0 65535 1000 40000 5 6\n")...))
	add("pnm-p4", append([]byte("P4\n10 3\n"), formatNoise(14, 6)...))
	add("pnm-p5-8bit", append([]byte("P5\n40 30\n255\n"), formatNoise(15, 1200)...))
	add("pnm-p5-16bit", append([]byte("P5\n20 10\n65535\n"), formatNoise(16, 400)...))
	add("pnm-p6-2050x120", append([]byte("P6\n2050 120\n255\n"), formatNoise(17, 2050*120*3)...))
	add("pnm-p6-maxval-200", append([]byte("P6\n8 8\n200\n"), formatNoise(18, 192)...))
	add("pnm-p7-rgb", append([]byte("P7\nWIDTH 6\nHEIGHT 5\nDEPTH 3\nMAXVAL 255\nTUPLTYPE RGB\nENDHDR\n"), formatNoise(19, 90)...))
	add("pnm-p7-bw", append([]byte("P7\nWIDTH 4\nHEIGHT 2\nDEPTH 1\nMAXVAL 1\nTUPLTYPE BLACKANDWHITE\nENDHDR\n"), 1, 0, 0, 1, 1, 1, 0, 0))
	add("truncated-hdr", hdrInput(37, 19, 4)[:300])
	add("truncated-dds", ddsInput(16, 16, "DXT1", 7)[:140])
	add("truncated-ico", icoBitmapInput(16, 16, 10)[:100])
	add("truncated-farbfeld", farbfeldInput(17, 9, 1)[:100])
	add("truncated-pnm", append([]byte("P6\n8 8\n255\n"), formatNoise(20, 100)...))
	add("dds-bc4", func() []byte { d := ddsInput(8, 8, "ATI1", 21); return d }())

	compareProcessImageWithPi(t, inputs, []string{"image/x-icon", "image/png", "", "application/octet-stream", "image/jpeg; q=1"})
}

// namedImage is an input of an oracle test.
type namedImage struct {
	name string
	data []byte
}

// compareProcessImageWithPi runs image-process.ts processImage and PiG's ProcessImage over every input under every declared type, with and
// without resizing, with the default limit and with 16: whether an image is returned, its type, the hints, the omission message and the
// returned bytes must be Pi's.
func compareProcessImageWithPi(t *testing.T, inputs []namedImage, mimes []string) {
	t.Helper()
	var cases []processImageCase
	var names []string
	for _, in := range inputs {
		for _, mime := range mimes {
			for _, resize := range []bool{true, false} {
				for _, limit := range []int{0, 16} {
					cases = append(cases, processImageCase{Data: base64.StdEncoding.EncodeToString(in.data), MimeType: mime, AutoResize: resize, MaxWidth: limit, MaxHeight: limit})
					names = append(names, in.name)
				}
			}
		}
	}

	var want []processImageOutcome
	pioracle.Run(t, `
const { processImage } = await load("pi-coding-agent/utils/image-process.js");
const { createHash } = await import("node:crypto");
const out = [];
for (const c of input) {
	const resizeOptions = {};
	if (c.maxWidth) resizeOptions.maxWidth = c.maxWidth;
	if (c.maxHeight) resizeOptions.maxHeight = c.maxHeight;
	const r = await processImage(new Uint8Array(Buffer.from(c.data, "base64")), c.mimeType, { autoResizeImages: c.autoResize, resizeOptions });
	out.push(r.ok ? { ok: true, mimeType: r.mimeType, hints: r.hints.join("\n"), message: "", sha256: createHash("sha256").update(Buffer.from(r.data, "base64")).digest("hex") } : { ok: false, mimeType: "", hints: "", message: r.message, sha256: "" });
}
emit(out);`, cases, &want)

	decoded := 0
	failures := 0
	for i, c := range cases {
		raw, _ := base64.StdEncoding.DecodeString(c.Data)
		data, mime, hints, err := ProcessImage(raw, c.MimeType, c.AutoResize, &ai.ModelImageResizeOptions{MaxWidth: c.MaxWidth, MaxHeight: c.MaxHeight})
		got := processImageOutcome{OK: err == nil, MimeType: mime, Hints: hints}
		if err != nil {
			got.Message = err.Error()
		} else {
			sum := sha256.Sum256(data)
			got.SHA256 = hex.EncodeToString(sum[:])
			decoded++
		}
		if !reflect.DeepEqual(got, want[i]) {
			failures++
			if failures <= 20 {
				t.Errorf("case %d (%s, declared %q, resize %v, limit %d)\n Go %+v\n Pi %+v", i, names[i], c.MimeType, c.AutoResize, c.MaxWidth, got, want[i])
			}
		}
	}
	t.Logf("%d cases, %d returned an image, %d failures", len(cases), decoded, failures)
	if decoded == 0 {
		t.Error("no case returns an image: the inputs do not exercise the decoder")
	}
}
