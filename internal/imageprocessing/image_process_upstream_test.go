package imageprocessing

// pi: packages/coding-agent/src/utils/image-process.ts

import (
	"encoding/binary"
	"slices"
	"strings"
	"testing"
)

// tinyBMP1x1Red24bpp is createTinyBmp1x1Red24bpp of upstream test/image-process.test.ts: a 58-byte BMP of one 24-bit pixel, with the pixel's
// blue byte left at 0 and its red byte 0xff, exactly as the test writes it.
func tinyBMP1x1Red24bpp() []byte {
	buffer := make([]byte, 58)
	copy(buffer, "BM")
	binary.LittleEndian.PutUint32(buffer[2:], uint32(len(buffer)))
	binary.LittleEndian.PutUint32(buffer[10:], 54)
	binary.LittleEndian.PutUint32(buffer[14:], 40)
	binary.LittleEndian.PutUint32(buffer[18:], 1)
	binary.LittleEndian.PutUint32(buffer[22:], 1)
	binary.LittleEndian.PutUint16(buffer[26:], 1)
	binary.LittleEndian.PutUint16(buffer[28:], 24)
	binary.LittleEndian.PutUint32(buffer[30:], 0)
	binary.LittleEndian.PutUint32(buffer[34:], 4)
	buffer[56] = 0xff
	return buffer
}

func expectPNGMagic(t *testing.T, data []byte) {
	t.Helper()
	if len(data) < 4 || data[0] != 0x89 || data[1] != 0x50 || data[2] != 0x4e || data[3] != 0x47 {
		t.Fatalf("not a PNG: % x", data[:min(4, len(data))])
	}
}

// Upstream "detects the complete %s signature" (GIF87a, GIF89a) and "detects BMP files from magic bytes".
func TestImageProcessPipelineDetectsSignatures(t *testing.T) {
	for _, signature := range []string{"GIF87a", "GIF89a"} {
		if got := DetectSupportedImageMimeType([]byte(signature)); got != "image/gif" {
			t.Errorf("DetectSupportedImageMimeType(%q) = %q, want image/gif", signature, got)
		}
	}
	if got := DetectSupportedImageMimeType(tinyBMP1x1Red24bpp()); got != "image/bmp" {
		t.Errorf("BMP detected as %q, want image/bmp", got)
	}
}

// Upstream "converts BMP files to PNG attachments when auto-resize is disabled" and "converts BMP files before auto-resizing".
func TestImageProcessPipelineConvertsBMPToPNG(t *testing.T) {
	for _, autoResize := range []bool{false, true} {
		data, mimeType, hints, err := ProcessImage(tinyBMP1x1Red24bpp(), "image/bmp", autoResize, nil)
		if err != nil {
			t.Fatalf("autoResize=%v: %v", autoResize, err)
		}
		if mimeType != "image/png" {
			t.Errorf("autoResize=%v: mime type %q, want image/png", autoResize, mimeType)
		}
		if !slices.Contains(strings.Split(hints, "\n"), "[Image converted from image/bmp to image/png.]") {
			t.Errorf("autoResize=%v: hints %q lack the conversion hint", autoResize, hints)
		}
		expectPNGMagic(t, data)
	}
}
