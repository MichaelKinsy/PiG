package tools

import (
	"encoding/binary"
	"testing"
)

// pi: packages/durable/src/tools/image.ts isBmp (:98-118), isAnimatedPng and isAnimatedPngOf (:37-69), HEADER_BYTES and BLOCK_BYTES.
// bmpHeader lays out the 26..30 bytes isBmp reads: "BM", the declared file size at 2, the pixel data offset at 10, the DIB header size at 14, then the colour planes and bits per pixel at
// 22/24 for the 12-byte OS/2 header or 26/28 for the Windows headers.
func bmpHeader(length int, fileSize, pixelOffset, dibSize uint32, planes, bitsPerPixel uint16) []byte {
	data := make([]byte, max(length, 30))
	copy(data, "BM")
	binary.LittleEndian.PutUint32(data[2:], fileSize)
	binary.LittleEndian.PutUint32(data[10:], pixelOffset)
	binary.LittleEndian.PutUint32(data[14:], dibSize)
	if dibSize == 12 {
		binary.LittleEndian.PutUint16(data[22:], planes)
		binary.LittleEndian.PutUint16(data[24:], bitsPerPixel)
	} else {
		binary.LittleEndian.PutUint16(data[26:], planes)
		binary.LittleEndian.PutUint16(data[28:], bitsPerPixel)
	}
	return data[:length]
}

// image.ts isBmp: a buffer of at least 26 bytes (30 for a Windows DIB header), a declared file size that is 0 or at least 26, a pixel data offset that is not inside the headers
// and, when a file size is declared, lies before the end of the file, a DIB header of 12 or 40..124 bytes, one colour plane and 1, 4, 8, 16, 24 or 32 bits per pixel.
// mutation-checked: every boundary comparison and the declared-size guards of isBmp fail it.
func TestDetectSupportedImageMimeTypeAcceptsABMPOnlyWhenItsHeaderIsConsistent(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"windows header, declared size, consistent", bmpHeader(100, 100, 54, 40, 1, 24), "image/bmp"},
		{"windows header, no declared size", bmpHeader(54, 0, 54, 40, 1, 24), "image/bmp"},
		{"windows header, a large pixel offset without a declared size", bmpHeader(54, 0, 100000, 40, 1, 24), "image/bmp"},
		{"windows header, only 30 bytes", bmpHeader(30, 0, 54, 40, 1, 8), "image/bmp"},
		{"windows header, only 29 bytes", bmpHeader(29, 0, 54, 40, 1, 8), ""},
		{"os/2 header, exactly 26 bytes", bmpHeader(26, 0, 26, 12, 1, 8), "image/bmp"},
		{"os/2 header, 25 bytes", bmpHeader(25, 0, 26, 12, 1, 8), ""},
		{"declared size 25 is below the headers", bmpHeader(100, 25, 54, 40, 1, 24), ""},
		{"declared size 5 is below the headers", bmpHeader(100, 5, 54, 40, 1, 24), ""},
		{"declared size 26 with an os/2 pixel offset of 26", bmpHeader(26, 26, 26, 12, 1, 8), ""},
		{"declared size 27 with an os/2 pixel offset of 26", bmpHeader(27, 27, 26, 12, 1, 8), "image/bmp"},
		{"pixel offset inside the headers", bmpHeader(100, 100, 53, 40, 1, 24), ""},
		{"pixel offset at the end of the declared file", bmpHeader(100, 100, 100, 40, 1, 24), ""},
		{"pixel offset one before the end of the declared file", bmpHeader(100, 100, 99, 40, 1, 24), "image/bmp"},
		{"dib header of 13 bytes", bmpHeader(100, 0, 100, 13, 1, 24), ""},
		{"dib header of 39 bytes", bmpHeader(100, 0, 100, 39, 1, 24), ""},
		{"dib header of 124 bytes", bmpHeader(200, 200, 138, 124, 1, 24), "image/bmp"},
		{"dib header of 125 bytes", bmpHeader(200, 200, 139, 125, 1, 24), ""},
		{"two colour planes", bmpHeader(100, 100, 54, 40, 2, 24), ""},
		{"zero colour planes", bmpHeader(100, 100, 54, 40, 0, 24), ""},
		{"0 bits per pixel", bmpHeader(100, 100, 54, 40, 1, 0), ""},
		{"2 bits per pixel", bmpHeader(100, 100, 54, 40, 1, 2), ""},
		{"12 bits per pixel", bmpHeader(100, 100, 54, 40, 1, 12), ""},
	}
	for _, bpp := range []uint16{1, 4, 8, 16, 24, 32} {
		cases = append(cases, struct {
			name string
			data []byte
			want string
		}{"bits per pixel " + string(rune('0'+bpp/10)) + string(rune('0'+bpp%10)), bmpHeader(100, 100, 54, 40, 1, bpp), "image/bmp"})
	}
	for _, c := range cases {
		if got := detectSupportedImageMimeType(c.data); got != c.want {
			t.Errorf("%s: detectSupportedImageMimeType = %q, want %q", c.name, got, c.want)
		}
		if got, err := detectSupportedImageMimeTypeOf(memorySource(c.data)); err != nil || got != c.want {
			t.Errorf("%s: detectSupportedImageMimeTypeOf = %q, %v, want %q", c.name, got, err, c.want)
		}
	}
}

// image.ts:37-69: the chunk walk reads a chunk header while 8 bytes remain, so a header that ends exactly at the end of the data is still read; an `acTL` there makes the PNG animated, and an
// `IDAT` makes it still. The file variant reads in 64 KiB blocks and finds an `acTL` that follows a chunk spanning several blocks.
// mutation-checked: `<` for `<=` on the remaining 8 bytes, in the buffer and the file walk, fails it.
func TestAnimatedPngChunkWalkReadsAHeaderThatEndsTheFile(t *testing.T) {
	truncatedActl := append(pngWith(), []byte{0, 0, 0, 8, 'a', 'c', 'T', 'L'}...)
	truncatedIdat := append(pngWith(), []byte{0, 0, 0, 8, 'I', 'D', 'A', 'T'}...)
	for _, c := range []struct {
		name, want string
		data       []byte
	}{
		{"acTL header ending the file", "", truncatedActl},
		{"IDAT header ending the file", "image/png", truncatedIdat},
		{"seven bytes after IHDR are not a header", "image/png", append(pngWith(), []byte{0, 0, 0, 8, 'a', 'c', 'T'}...)},
	} {
		if got := detectSupportedImageMimeType(c.data); got != c.want {
			t.Errorf("%s: detectSupportedImageMimeType = %q, want %q", c.name, got, c.want)
		}
		if got, err := detectSupportedImageMimeTypeOf(memorySource(c.data)); err != nil || got != c.want {
			t.Errorf("%s: detectSupportedImageMimeTypeOf = %q, %v, want %q", c.name, got, err, c.want)
		}
	}
	// A 200,000-byte ancillary chunk puts the acTL header in a later block of the file variant.
	large := pngWith(pngChunk("tEXt", make([]byte, 200_000)), pngChunk("acTL", make([]byte, 8)), pngChunk("IDAT", []byte{1}))
	if got, err := detectSupportedImageMimeTypeOf(memorySource(large)); err != nil || got != "" {
		t.Errorf("animated PNG behind a large chunk: %q, %v, want it detected as animated", got, err)
	}
	largeStill := pngWith(pngChunk("tEXt", make([]byte, 200_000)), pngChunk("IDAT", []byte{1}), pngChunk("acTL", make([]byte, 8)))
	if got, err := detectSupportedImageMimeTypeOf(memorySource(largeStill)); err != nil || got != "image/png" {
		t.Errorf("still PNG behind a large chunk: %q, %v, want image/png (an acTL after the first IDAT does not count)", got, err)
	}
}
