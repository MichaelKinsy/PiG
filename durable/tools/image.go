package tools

import "bytes"

// Ports packages/durable/src/tools/image.ts.

var pngSignature = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// detectSupportedImageMimeType is the MIME type of a supported still image by
// its content, or "" when the bytes are not one. An animated PNG is not still.
func detectSupportedImageMimeType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		if len(data) > 3 && data[3] == 0xf7 {
			return ""
		}
		return "image/jpeg"
	case bytes.HasPrefix(data, pngSignature):
		if isPng(data) && !isAnimatedPng(data) {
			return "image/png"
		}
		return ""
	case startsWithAscii(data, 0, "GIF87a") || startsWithAscii(data, 0, "GIF89a"):
		return "image/gif"
	case startsWithAscii(data, 0, "RIFF") && startsWithAscii(data, 8, "WEBP"):
		return "image/webp"
	case startsWithAscii(data, 0, "BM") && isBmp(data):
		return "image/bmp"
	}
	return ""
}

func isPng(data []byte) bool {
	return len(data) >= 16 && readUint32BE(data, len(pngSignature)) == 13 && startsWithAscii(data, 12, "IHDR")
}

func isAnimatedPng(data []byte) bool {
	offset := len(pngSignature)
	for offset+8 <= len(data) {
		chunkLength := readUint32BE(data, offset)
		chunkTypeOffset := offset + 4
		if startsWithAscii(data, chunkTypeOffset, "acTL") {
			return true
		}
		if startsWithAscii(data, chunkTypeOffset, "IDAT") {
			return false
		}
		nextOffset := int64(offset) + 8 + chunkLength + 4
		if nextOffset <= int64(offset) || nextOffset > int64(len(data)) {
			return false
		}
		offset = int(nextOffset)
	}
	return false
}

func isBmp(data []byte) bool {
	if len(data) < 26 {
		return false
	}
	declaredFileSize := readUint32LE(data, 2)
	pixelDataOffset := readUint32LE(data, 10)
	dibHeaderSize := readUint32LE(data, 14)
	if declaredFileSize != 0 && declaredFileSize < 26 {
		return false
	}
	if pixelDataOffset < 14+dibHeaderSize {
		return false
	}
	if declaredFileSize != 0 && pixelDataOffset >= declaredFileSize {
		return false
	}

	var colorPlanes, bitsPerPixel int
	switch {
	case dibHeaderSize == 12:
		colorPlanes = readUint16LE(data, 22)
		bitsPerPixel = readUint16LE(data, 24)
	case dibHeaderSize >= 40 && dibHeaderSize <= 124:
		if len(data) < 30 {
			return false
		}
		colorPlanes = readUint16LE(data, 26)
		bitsPerPixel = readUint16LE(data, 28)
	default:
		return false
	}
	return colorPlanes == 1 && (bitsPerPixel == 1 || bitsPerPixel == 4 || bitsPerPixel == 8 || bitsPerPixel == 16 || bitsPerPixel == 24 || bitsPerPixel == 32)
}

// byteAt is the byte at offset, or 0 past the end, as an out-of-range read of a
// typed array is undefined and counts as 0 in upstream's arithmetic.
func byteAt(data []byte, offset int) int {
	if offset < 0 || offset >= len(data) {
		return 0
	}
	return int(data[offset])
}

func readUint16LE(data []byte, offset int) int {
	return byteAt(data, offset) + byteAt(data, offset+1)<<8
}

func readUint32BE(data []byte, offset int) int64 {
	return int64(byteAt(data, offset))*0x1000000 + int64(byteAt(data, offset+1))<<16 + int64(byteAt(data, offset+2))<<8 + int64(byteAt(data, offset+3))
}

func readUint32LE(data []byte, offset int) int64 {
	return int64(byteAt(data, offset)) + int64(byteAt(data, offset+1))<<8 + int64(byteAt(data, offset+2))<<16 + int64(byteAt(data, offset+3))*0x1000000
}

func startsWithAscii(data []byte, offset int, text string) bool {
	return len(data) >= offset+len(text) && string(data[offset:offset+len(text)]) == text
}
