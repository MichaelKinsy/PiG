package codingagent

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"image/color"
	"image/png"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// syntheticClipboardBMP builds the BMP the Windows native clipboard reader returns for a CF_DIB/CF_DIBV5 payload: a 14-byte file header over the DIB, with the pixel offset past the header and any external bitfield masks.
func syntheticClipboardBMP(headerSize uint32, depth, compression uint16, masks []uint32, width, height int32, pixels []uint32) []byte {
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

// The expected pixels are Photon 0.3.4's get_raw_pixels() (RGBA, top row first) for the same bytes, the decoder Pi's convertToPng uses on the Windows native clipboard result. The BI_BITFIELDS rows are the CF_DIB shape of Print Screen and GDI bitmaps.
func TestClipboardImageConvertsWindowsBitmapOnlyClipboard(t *testing.T) {
	standard := []uint32{0xff0000, 0xff00, 0xff}
	pixels4 := []uint32{0x00ff0000, 0x0000ff00, 0x000000ff, 0x00ffffff}
	for _, tc := range []struct {
		name   string
		bitmap []byte
		width  int
		want   string
	}{
		{"CF_DIB 32-bit BI_BITFIELDS bottom-up", syntheticClipboardBMP(40, 32, 3, standard, 2, 2, pixels4), 2, "0000ffffffffffffff0000ff00ff00ff"},
		{"CF_DIB 32-bit BI_BITFIELDS top-down", syntheticClipboardBMP(40, 32, 3, standard, 2, -2, pixels4), 2, "ff0000ff00ff00ff0000ffffffffffff"},
		{"CF_DIB 32-bit BI_BITFIELDS RGB-ordered masks", syntheticClipboardBMP(40, 32, 3, []uint32{0xff, 0xff00, 0xff0000}, 2, 1, []uint32{0x04030201, 0xff804020}), 2, "010203ff204080ff"},
		// Photon keeps the top eight bits of a 10-bit channel: 0x3fc becomes 0xff and 0x003 becomes 0, where rounding gives 0xfe and 1.
		{"CF_DIB 32-bit BI_BITFIELDS 10-bit channels", syntheticClipboardBMP(40, 32, 3, []uint32{0x3ff00000, 0x000ffc00, 0x000003ff}, 3, 1, []uint32{0x3ff00000, 0x1ff, 0x3fc | 0x003<<20}), 3, "ff0000ff00007fff0000ffff"},
		{"CF_DIBV5 32-bit BI_BITFIELDS with alpha mask", syntheticClipboardBMP(124, 32, 3, []uint32{0xff, 0xff00, 0xff0000, 0xff000000}, 2, 1, []uint32{0x80030201, 0xff804020}), 2, "01020380204080ff"},
		{"CF_DIB 16-bit BI_BITFIELDS 5-6-5", syntheticClipboardBMP(40, 16, 3, []uint32{0xf800, 0x07e0, 0x001f}, 4, 1, []uint32{0x0800, 0x4000, 0x07e0, 0x0400}), 4, "080000ff420000ff00ff00ff008200ff"},
		{"CF_DIB 16-bit BI_RGB 5-5-5", syntheticClipboardBMP(40, 16, 0, nil, 4, 1, []uint32{0x7c00, 0x03e0, 0x001f, 0xffff}), 4, "ff0000ff00ff00ff0000ffffffffffff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldPlatform, oldNative := clipboardGOOS, getNativeClipboard
			t.Cleanup(func() { clipboardGOOS, getNativeClipboard = oldPlatform, oldNative })
			clipboardGOOS = "windows"
			getNativeClipboard = func() *tui.NativeClipboard {
				return &tui.NativeClipboard{GetImage: func(context.Context) ([]byte, bool, error) { return tc.bitmap, true, nil }}
			}
			data, mime, err := ReadClipboardImageContext(t.Context())
			if err != nil || data == nil || mime != "image/png" {
				t.Fatalf("image=%d bytes MIME=%q error=%v, want a PNG", len(data), mime, err)
			}
			decoded, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			var got []byte
			bounds := decoded.Bounds()
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					c := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
					got = append(got, c.R, c.G, c.B, c.A)
				}
			}
			if hex.EncodeToString(got) != tc.want {
				t.Errorf("pixels=%x, want %s", got, tc.want)
			}
		})
	}
}

// Photon rejects these bitmaps, so Pi's convertToPng returns null and readClipboardImage reports no image.
func TestClipboardImageRejectsBitmapsPhotonRejects(t *testing.T) {
	for name, bitmap := range map[string][]byte{
		"non-contiguous colour mask":     syntheticClipboardBMP(40, 32, 3, []uint32{0xf0f00000, 0xff00, 0xff}, 2, 1, []uint32{0xffffffff, 0x10100000}),
		"16-bit colour mask past bit 15": syntheticClipboardBMP(40, 16, 3, []uint32{0xf8000, 0x07e0, 0x001f}, 2, 1, []uint32{0xffff, 0x07e0}),
		"zero colour mask":               syntheticClipboardBMP(40, 32, 3, []uint32{0, 0xff00, 0xff}, 2, 1, []uint32{0xffffffff, 0x1234}),
	} {
		t.Run(name, func(t *testing.T) {
			oldPlatform, oldNative := clipboardGOOS, getNativeClipboard
			t.Cleanup(func() { clipboardGOOS, getNativeClipboard = oldPlatform, oldNative })
			clipboardGOOS = "windows"
			getNativeClipboard = func() *tui.NativeClipboard {
				return &tui.NativeClipboard{GetImage: func(context.Context) ([]byte, bool, error) { return bitmap, true, nil }}
			}
			data, mime, err := ReadClipboardImageContext(t.Context())
			if data != nil || mime != "" || err != nil {
				t.Fatalf("image=%d bytes MIME=%q error=%v, want no image", len(data), mime, err)
			}
		})
	}
}
