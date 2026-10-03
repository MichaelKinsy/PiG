package imageprocessing

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// bitfieldBMP builds a BMP file around a DIB with the given header size. A BITMAPINFOHEADER with BI_BITFIELDS carries its three masks after the header; larger headers carry them inside.
func bitfieldBMP(headerSize uint32, depth, compression uint16, masks []uint32, width, height int32, pixels []uint32) []byte {
	external := 0
	if headerSize == 40 && compression == 3 {
		external = 12
	}
	rows := int(height)
	if rows < 0 {
		rows = -rows
	}
	stride := (int(width)*int(depth) + 31) / 32 * 4
	dib := make([]byte, int(headerSize)+external+stride*rows)
	binary.LittleEndian.PutUint32(dib, headerSize)
	binary.LittleEndian.PutUint32(dib[4:], uint32(width))
	binary.LittleEndian.PutUint32(dib[8:], uint32(height))
	binary.LittleEndian.PutUint16(dib[12:], 1)
	binary.LittleEndian.PutUint16(dib[14:], depth)
	binary.LittleEndian.PutUint32(dib[16:], uint32(compression))
	binary.LittleEndian.PutUint32(dib[20:], uint32(stride*rows))
	for i, mask := range masks {
		binary.LittleEndian.PutUint32(dib[40+i*4:], mask)
	}
	pixelStart := int(headerSize) + external
	for i, pixel := range pixels {
		row, column := i/int(width), i%int(width)
		if depth == 16 {
			binary.LittleEndian.PutUint16(dib[pixelStart+row*stride+column*2:], uint16(pixel))
		} else {
			binary.LittleEndian.PutUint32(dib[pixelStart+row*stride+column*4:], pixel)
		}
	}
	bmp := make([]byte, 14+len(dib))
	copy(bmp, "BM")
	binary.LittleEndian.PutUint32(bmp[2:], uint32(len(bmp)))
	binary.LittleEndian.PutUint32(bmp[10:], uint32(14+pixelStart))
	copy(bmp[14:], dib)
	return bmp
}

func rgbaHex(img image.Image) string {
	var got []byte
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			got = append(got, c.R, c.G, c.B, c.A)
		}
	}
	return hex.EncodeToString(got)
}

// Each want is Photon 0.3.4's get_raw_pixels() (RGBA, top row first) from PhotonImage.new_from_byteslice on the same bytes, measured with the pinned @silvia-odwyer/photon-node 0.3.4 WASM; "" means Photon throws, so Pi's convertImageBytesToPng returns null. Photon decodes with the image 0.24.9 crate.
func TestDecodeImageMatchesPhotonBitfieldBMP(t *testing.T) {
	rgb := []uint32{0xff0000, 0xff00, 0xff}
	tenBit := []uint32{0x3ff00000, 0x000ffc00, 0x000003ff}
	argb := []uint32{0xff0000, 0xff00, 0xff, 0xff000000}
	for _, tc := range []struct {
		name string
		bmp  []byte
		want string
	}{
		{"32-bit BI_BITFIELDS INFOHEADER ignores the unmasked byte", bitfieldBMP(40, 32, 3, rgb, 2, 1, []uint32{0x80102030, 0x00405060}), "102030ff405060ff"},
		// The image crate keeps the top eight bits of a wider channel instead of rounding: 0x3fc becomes 0xff and 0x003 becomes 0, where rounding gives 0xfe and 1.
		{"32-bit 10-bit channels truncate to eight bits", bitfieldBMP(40, 32, 3, tenBit, 6, 1, []uint32{0x3fc, 0x3fb, 0x201, 0x1ff, 0x003, 0x3ff}), "0000ffff0000feff000080ff00007fff000000ff0000ffff"},
		{"16-bit 7-bit channel replicates its top bit", bitfieldBMP(40, 16, 3, []uint32{0x7f, 0x380, 0x3c00}, 4, 1, []uint32{0x40, 0x3f, 0x01, 0x7f}), "810000ff7e0000ff020000ffff0000ff"},
		{"16-bit 3-bit and 4-bit channels", bitfieldBMP(40, 16, 3, []uint32{0x7, 0x78, 0x80}, 3, 1, []uint32{0x3 | 0x5<<3, 0x5 | 0x9<<3, 0x7 | 0xf<<3 | 0x80}), "6d5500ffb69900ffffffffff"},
		{"BITMAPV2INFOHEADER has no alpha mask", setBMPHeaderSize(bitfieldBMP(124, 32, 3, argb, 2, 1, []uint32{0x80102030, 0x00405060}), 52), "102030ff405060ff"},
		{"BITMAPV3INFOHEADER alpha mask", setBMPHeaderSize(bitfieldBMP(124, 32, 3, argb, 2, 1, []uint32{0x80102030, 0x00405060}), 56), "1020308040506000"},
		{"16-bit BITMAPV3INFOHEADER alpha mask", setBMPHeaderSize(bitfieldBMP(124, 16, 3, []uint32{0x0f00, 0x00f0, 0x000f, 0xf000}, 2, 1, []uint32{0x8f00, 0x00f0}), 56), "ff00008800ff0000"},
		{"non-contiguous colour mask", bitfieldBMP(40, 32, 3, []uint32{0xf0f00000, 0xff00, 0xff}, 2, 1, []uint32{0xffffffff, 0x10100000}), ""},
		{"non-contiguous alpha mask", bitfieldBMP(124, 32, 3, []uint32{0xff0000, 0xff00, 0xff, 0xf0f00000}, 2, 1, []uint32{0x80102030, 0x00405060}), ""},
		{"16-bit colour mask past bit 15", bitfieldBMP(40, 16, 3, []uint32{0xf8000, 0x07e0, 0x001f}, 2, 1, []uint32{0xffff, 0x07e0}), ""},
		{"16-bit alpha mask past bit 15", bitfieldBMP(124, 16, 3, []uint32{0x0f00, 0x00f0, 0x000f, 0xf0000}, 2, 1, []uint32{0x8f00, 0x00f0}), ""},
		{"zero colour mask", bitfieldBMP(40, 32, 3, []uint32{0, 0xff00, 0xff}, 2, 1, []uint32{0xffffffff, 0x1234}), ""},
		{"16-bit BI_RGB under a BITMAPCOREHEADER size", setBMPHeaderSize(bitfieldBMP(124, 16, 0, nil, 2, 1, []uint32{0x7c00, 0x8001}), 12), ""},
		{"16-bit BI_RGB under an OS/2 2.x header size", setBMPHeaderSize(bitfieldBMP(124, 16, 0, nil, 2, 1, []uint32{0x7c00, 0x8001}), 64), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := DecodeImage(tc.bmp)
			got := ""
			if err == nil {
				got = rgbaHex(decoded)
			}
			if got != tc.want {
				t.Fatalf("pixels=%q error=%v, want %q", got, err, tc.want)
			}
		})
	}
}

func setBMPHeaderSize(bmp []byte, size uint32) []byte {
	binary.LittleEndian.PutUint32(bmp[14:], size)
	return bmp
}

func TestDecodeBitfieldBMPRejectsMalformed(t *testing.T) {
	good := bitfieldBMP(40, 32, 3, []uint32{0xff0000, 0xff00, 0xff}, 2, 2, nil)
	if _, err := decodeBitfieldBMP(bytes.Clone(good)); err != nil {
		t.Fatalf("unmodified bitmap: %v", err)
	}
	for name, mutate := range map[string]func([]byte) []byte{
		"truncated pixels":       func(b []byte) []byte { return b[:len(b)-1] },
		"truncated masks":        func(b []byte) []byte { return b[:14+40+8] },
		"zero colour mask":       func(b []byte) []byte { binary.LittleEndian.PutUint32(b[14+40:], 0); return b },
		"zero width":             func(b []byte) []byte { binary.LittleEndian.PutUint32(b[18:], 0); return b },
		"huge height":            func(b []byte) []byte { binary.LittleEndian.PutUint32(b[22:], 0x7fffffff); return b },
		"pixel offset in header": func(b []byte) []byte { binary.LittleEndian.PutUint32(b[10:], 20); return b },
		"unknown header size":    func(b []byte) []byte { binary.LittleEndian.PutUint32(b[14:], 64); return b },
		"RLE compression":        func(b []byte) []byte { binary.LittleEndian.PutUint32(b[30:], 2); return b },
		"two planes":             func(b []byte) []byte { binary.LittleEndian.PutUint16(b[26:], 2); return b },
	} {
		t.Run(name, func(t *testing.T) {
			if decoded, err := decodeBitfieldBMP(mutate(bytes.Clone(good))); err == nil {
				t.Fatalf("decoded %v", decoded.Bounds())
			}
		})
	}
}

// Pi's read tool and tool-result normalization convert image/bmp through convertImageBytesToPng, the same Photon decode as clipboard paste, so a 16-bit bitmap file reaches the model as PNG.
// upstream: packages/coding-agent/src/utils/image-process.ts:normalizeImage
func TestNormalizeToolResultImagesConvertsBitfieldBMP(t *testing.T) {
	bmp := bitfieldBMP(40, 16, 3, []uint32{0xf800, 0x07e0, 0x001f}, 4, 1, []uint32{0x0800, 0x4000, 0x07e0, 0x0400})
	result := agent.AgentToolResult{
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "shot"}, ai.ImageContent{MimeType: "image/bmp", Data: base64.StdEncoding.EncodeToString(bmp)}},
	}
	got := NormalizeToolResultImages(result, false)
	if got.Images()[0].MimeType != "image/png" {
		t.Fatalf("output MIME = %q text=%q, want image/png", got.Images()[0].MimeType, got.Text())
	}
	decoded, err := png.Decode(base64.NewDecoder(base64.StdEncoding, bytes.NewReader([]byte(got.Images()[0].Data))))
	if err != nil {
		t.Fatal(err)
	}
	if pixels := rgbaHex(decoded); pixels != "080000ff420000ff00ff00ff008200ff" {
		t.Fatalf("pixels=%s, want Photon's 080000ff420000ff00ff00ff008200ff", pixels)
	}
}
