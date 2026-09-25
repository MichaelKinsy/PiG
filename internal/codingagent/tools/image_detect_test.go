package tools

import (
	"encoding/binary"
	"testing"
)

func TestSupportedImageMime(t *testing.T) {
	validPNG := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0, 0, 0, 13, 'I', 'H', 'D', 'R',
		0, 0, 0, 1, 0, 0, 0, 1, 8, 2, 0, 0, 0,
		0, 0, 0, 0,
	}
	animatedPNG := append(append([]byte{}, validPNG...), []byte{0, 0, 0, 0, 'a', 'c', 'T', 'L', 0, 0, 0, 0}...)
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"PNG", validPNG, "image/png"},
		{"JPEG", []byte{0xff, 0xd8, 0xff, 0xe0}, "image/jpeg"},
		{"JPEG-LS unsupported", []byte{0xff, 0xd8, 0xff, 0xf7}, ""},
		{"WebP", []byte("RIFF\x00\x00\x00\x00WEBP"), "image/webp"},
		{"GIF87a", []byte("GIF87a\x00"), "image/gif"},
		{"GIF89a", []byte("GIF89a\x00"), "image/gif"},
		{"GIF-prefixed text", []byte("GIF is not an image\n"), ""},
		{"truncated GIF signature", []byte("GIF89"), ""},
		{"BMP", bmpHeader(58, 54, 40, 1, 24), "image/bmp"},
		{"BMP core header", bmpHeader(0, 26, 12, 1, 8), "image/bmp"},
		{"BM-prefixed text", []byte("BMAD method notes: keep this file as text\n"), ""},
		{"BMP bad bpp", bmpHeader(58, 54, 40, 1, 7), ""},
		{"BMP bad planes", bmpHeader(58, 54, 40, 2, 24), ""},
		{"BMP pixel offset past file", bmpHeader(58, 60, 40, 1, 24), ""},
		{"animated PNG unsupported", animatedPNG, ""},
		{"unknown binary", []byte{0x00, 0x01, 0x02}, ""},
		{"empty", []byte{}, ""},
		{"text", []byte("hello world"), ""},
		{"PDF", []byte("%PDF-1.4"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SupportedImageMime(tc.data)
			if got != tc.want {
				t.Errorf("SupportedImageMime = %q, want %q", got, tc.want)
			}
		})
	}
}

// bmpHeader builds a BMP file/DIB header with the fields upstream isBmp checks.
func bmpHeader(fileSize, pixelOffset, dibSize uint32, planes, bpp uint16) []byte {
	b := make([]byte, 30)
	copy(b, "BM")
	binary.LittleEndian.PutUint32(b[2:], fileSize)
	binary.LittleEndian.PutUint32(b[10:], pixelOffset)
	binary.LittleEndian.PutUint32(b[14:], dibSize)
	if dibSize == 12 {
		binary.LittleEndian.PutUint16(b[22:], planes)
		binary.LittleEndian.PutUint16(b[24:], bpp)
	} else {
		binary.LittleEndian.PutUint16(b[26:], planes)
		binary.LittleEndian.PutUint16(b[28:], bpp)
	}
	return b
}
