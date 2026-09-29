package nativeplatform

import (
	"encoding/binary"
	"slices"
	"testing"
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
