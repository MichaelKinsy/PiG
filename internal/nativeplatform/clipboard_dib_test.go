package nativeplatform

import (
	"encoding/binary"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
)

func TestClipboardDIBEnvelope(t *testing.T) {
	dib := make([]byte, 44)
	binary.LittleEndian.PutUint32(dib, 40)
	binary.LittleEndian.PutUint32(dib[4:], 1)
	binary.LittleEndian.PutUint32(dib[8:], 1)
	binary.LittleEndian.PutUint16(dib[12:], 1)
	binary.LittleEndian.PutUint16(dib[14:], 24)
	binary.LittleEndian.PutUint32(dib[20:], 4)
	dib[42] = 255
	got := bitmapFromDIB(dib)
	if len(got) != 14+len(dib) || string(got[:2]) != "BM" || binary.LittleEndian.Uint32(got[2:]) != uint32(len(got)) || binary.LittleEndian.Uint32(got[10:]) != 54 || !slices.Equal(got[14:], dib) {
		t.Fatalf("BMP=%x", got)
	}
}

func TestClipboardDIBMetadataBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                string
		header              uint32
		bits                uint16
		compression, colors uint32
		length              int
		offset              uint64
	}{
		{"empty", 0, 0, 0, 0, 0, 0},
		{"short core header", 12, 24, 0, 0, 11, 0},
		{"core RGB", 12, 24, 0, 0, 15, 12},
		{"core palette", 12, 4, 0, 0, 63, 60},
		{"truncated core palette", 12, 4, 0, 0, 59, 0},
		{"invalid header", 13, 24, 0, 0, 44, 0},
		{"oversized header", 124, 32, 0, 0, 44, 0},
		{"RGB", 40, 24, 0, 0, 44, 40},
		{"palette", 40, 4, 0, 0, 108, 104},
		{"explicit palette", 40, 24, 0, 2, 52, 48},
		{"overflowing palette", 40, 24, 0, ^uint32(0), 44, 0},
		{"bitfields", 40, 32, 3, 0, 56, 52},
		{"alpha bitfields", 40, 32, 6, 0, 60, 56},
		{"truncated bitfields", 40, 32, 3, 0, 51, 0},
		{"V5", 124, 32, 3, 0, 128, 124},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.length)
			if len(data) >= 4 {
				binary.LittleEndian.PutUint32(data, tc.header)
			}
			if tc.header == 12 && len(data) >= 12 {
				binary.LittleEndian.PutUint16(data[10:], tc.bits)
			}
			if tc.header >= 40 && len(data) >= 40 {
				binary.LittleEndian.PutUint16(data[14:], tc.bits)
				binary.LittleEndian.PutUint32(data[16:], tc.compression)
				binary.LittleEndian.PutUint32(data[32:], tc.colors)
			}
			if got := dibPixelOffset(data); got != tc.offset {
				t.Fatalf("offset=%d, want %d", got, tc.offset)
			}
			if tc.offset == 0 && bitmapFromDIB(data) != nil {
				t.Fatal("invalid DIB wrapped")
			}
		})
	}
}

// The 174-byte clipboard image Windows' CF_DIBV5 produced in CI was a BITMAPV5HEADER, positive height, 32 bpp, BI_BITFIELDS with the
// masks in the header, pixels at file offset 138 and 12 bytes of allocation padding. Rows are stored bottom-up, so the decoded first
// row is the file's last pixel row.
func TestClipboardDIBV5BitfieldsRowOrder(t *testing.T) {
	for _, tc := range []struct {
		name            string
		red, green, blu uint32
		pixel           func(r, g, b uint8) uint32
	}{
		{"BGRA masks", 0x00FF0000, 0x0000FF00, 0x000000FF, func(r, g, b uint8) uint32 { return 0xFF000000 | uint32(r)<<16 | uint32(g)<<8 | uint32(b) }},
		{"RGBA masks", 0x000000FF, 0x0000FF00, 0x00FF0000, func(r, g, b uint8) uint32 { return 0xFF000000 | uint32(b)<<16 | uint32(g)<<8 | uint32(r) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			top := [3][3]uint8{{255, 0, 0}, {0, 255, 0}, {0, 0, 255}}
			bottom := [3][3]uint8{{10, 20, 30}, {200, 150, 100}, {1, 2, 3}}
			dib := make([]byte, 124+2*3*4+12)
			binary.LittleEndian.PutUint32(dib, 124)
			binary.LittleEndian.PutUint32(dib[4:], 3)
			binary.LittleEndian.PutUint32(dib[8:], 2)
			binary.LittleEndian.PutUint16(dib[12:], 1)
			binary.LittleEndian.PutUint16(dib[14:], 32)
			binary.LittleEndian.PutUint32(dib[16:], 3)
			binary.LittleEndian.PutUint32(dib[20:], 24)
			binary.LittleEndian.PutUint32(dib[40:], tc.red)
			binary.LittleEndian.PutUint32(dib[44:], tc.green)
			binary.LittleEndian.PutUint32(dib[48:], tc.blu)
			binary.LittleEndian.PutUint32(dib[52:], 0xFF000000)
			for x := range 3 {
				binary.LittleEndian.PutUint32(dib[124+x*4:], tc.pixel(bottom[x][0], bottom[x][1], bottom[x][2]))
				binary.LittleEndian.PutUint32(dib[124+12+x*4:], tc.pixel(top[x][0], top[x][1], top[x][2]))
			}
			bitmap := bitmapFromDIB(dib)
			if binary.LittleEndian.Uint32(bitmap[10:]) != 138 {
				t.Fatalf("pixel offset = %d", binary.LittleEndian.Uint32(bitmap[10:]))
			}
			img, err := imageprocessing.DecodeImage(bitmap)
			if err != nil {
				t.Fatal(err)
			}
			for x := range 3 {
				for y, want := range [2][3]uint8{top[x], bottom[x]} {
					r, g, b, a := img.At(x, y).RGBA()
					if uint8(r>>8) != want[0] || uint8(g>>8) != want[1] || uint8(b>>8) != want[2] || a != 0xffff {
						t.Errorf("pixel (%d,%d) = %d,%d,%d,%d, want %v", x, y, r>>8, g>>8, b>>8, a>>8, want)
					}
				}
			}
		})
	}
}
