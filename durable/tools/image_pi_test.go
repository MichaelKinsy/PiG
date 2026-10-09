package tools

import (
	"encoding/binary"
	"testing"
)

// pi: packages/durable/src/tools/image.ts

func pngChunk(kind string, payload []byte) []byte {
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(payload)))
	chunk = append(chunk, kind...)
	chunk = append(chunk, payload...)
	return append(chunk, 0, 0, 0, 0)
}

func pngWith(chunks ...[]byte) []byte {
	data := append([]byte{}, pngSignature...)
	data = append(data, pngChunk("IHDR", make([]byte, 13))...)
	for _, chunk := range chunks {
		data = append(data, chunk...)
	}
	return data
}

func memorySource(data []byte) byteSource {
	return byteSource{size: int64(len(data)), read: func(offset, length int64) ([]byte, error) {
		if offset >= int64(len(data)) {
			return nil, nil
		}
		return data[offset:min(offset+length, int64(len(data)))], nil
	}}
}

// detectSupportedImageMimeType / detectSupportedImageMimeTypeOf (image.ts:20-34, 71-79): the signature selects the type; an animated PNG
// (an acTL chunk before the first IDAT) and a JPEG whose fourth byte is 0xf7 (JPEG-LS) are not supported images; the file-reading variant
// agrees with the buffer variant while reading only the header and the chunk headers.
func TestDetectSupportedImageMimeTypeMatchesPi(t *testing.T) {
	still := pngWith(pngChunk("IDAT", []byte{1, 2, 3}))
	animated := pngWith(pngChunk("acTL", make([]byte, 8)), pngChunk("IDAT", []byte{1}))
	stillAfterAncillary := pngWith(pngChunk("tEXt", []byte("k\x00v")), pngChunk("IDAT", []byte{1}))
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"jpeg", []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0}, "image/jpeg"},
		{"jpeg-ls", []byte{0xff, 0xd8, 0xff, 0xf7, 0, 0}, ""},
		{"png", still, "image/png"},
		{"png with ancillary chunk first", stillAfterAncillary, "image/png"},
		{"animated png", animated, ""},
		{"png without IHDR", append(append([]byte{}, pngSignature...), make([]byte, 24)...), ""},
		{"gif87a", []byte("GIF87a....."), "image/gif"},
		{"gif89a", []byte("GIF89a....."), "image/gif"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBP...."), "image/webp"},
		{"riff that is not webp", []byte("RIFF\x00\x00\x00\x00WAVE...."), ""},
		{"text", []byte("hello world, not an image"), ""},
		{"empty", nil, ""},
	}
	for _, c := range cases {
		if got := detectSupportedImageMimeType(c.data); got != c.want {
			t.Errorf("%s: detectSupportedImageMimeType = %q, want %q", c.name, got, c.want)
		}
		got, err := detectSupportedImageMimeTypeOf(memorySource(c.data))
		if err != nil || got != c.want {
			t.Errorf("%s: detectSupportedImageMimeTypeOf = %q, %v, want %q", c.name, got, err, c.want)
		}
	}
}
