package nativeplatform

import (
	"encoding/binary"
	"math"
)

// dibPixelOffset follows packages/tui/native/win32/src/win32-platform.c:dib_pixel_offset. Palette and external bitfield masks precede the pixels.
func dibPixelOffset(dib []byte) uint64 {
	if len(dib) < 12 {
		return 0
	}
	header := uint64(binary.LittleEndian.Uint32(dib))
	if header == 12 {
		bits := binary.LittleEndian.Uint16(dib[10:])
		var colors uint64
		if bits <= 8 {
			colors = uint64(1) << bits
		}
		offset := header + colors*3
		if offset <= uint64(len(dib)) {
			return offset
		}
		return 0
	}
	if header < 40 || header > uint64(len(dib)) {
		return 0
	}
	bits := binary.LittleEndian.Uint16(dib[14:])
	compression := binary.LittleEndian.Uint32(dib[16:])
	colors := uint64(binary.LittleEndian.Uint32(dib[32:]))
	if colors == 0 && bits <= 8 {
		colors = uint64(1) << bits
	}
	offset := header + colors*4
	if header == 40 && compression == 3 {
		offset += 3 * 4
	}
	if header == 40 && compression == 6 {
		offset += 4 * 4
	}
	if offset > uint64(len(dib)) {
		return 0
	}
	return offset
}

func bitmapFromDIB(dib []byte) []byte {
	const fileHeaderSize = 14
	offset := dibPixelOffset(dib)
	if offset == 0 || uint64(len(dib)) > math.MaxUint32-fileHeaderSize || len(dib) > int(^uint(0)>>1)-fileHeaderSize {
		return nil
	}
	bitmap := make([]byte, len(dib)+fileHeaderSize)
	copy(bitmap, "BM")
	binary.LittleEndian.PutUint32(bitmap[2:], uint32(len(bitmap)))
	binary.LittleEndian.PutUint32(bitmap[10:], uint32(offset)+fileHeaderSize)
	copy(bitmap[fileHeaderSize:], dib)
	return bitmap
}
