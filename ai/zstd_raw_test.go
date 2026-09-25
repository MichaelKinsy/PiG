package ai

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeZstdRawFrameRoundTrips(t *testing.T) {
	for _, size := range []int{0, 1, 255, 256, 65_791, 65_792, zstdRawBlockMax + 7} {
		input := bytes.Repeat([]byte("x"), size)
		encoded := encodeZstdRawFrame(input)
		decoded, err := decodeZstdRawFrameForTest(encoded)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if !bytes.Equal(decoded, input) {
			t.Fatalf("size %d: decoded payload differs", size)
		}
	}
}

func decodeZstdRawFrameForTest(frame []byte) ([]byte, error) {
	if len(frame) < 6 || !bytes.Equal(frame[:4], []byte{0x28, 0xb5, 0x2f, 0xfd}) {
		return nil, errors.New("invalid zstd frame")
	}
	descriptor := frame[4]
	contentSizeFlag := descriptor >> 6
	contentSizeBytes := [...]int{1, 2, 4, 8}[contentSizeFlag]
	offset := 5 + contentSizeBytes
	var output []byte
	for {
		if offset+3 > len(frame) {
			return nil, errors.New("truncated zstd block header")
		}
		header := uint32(frame[offset]) | uint32(frame[offset+1])<<8 | uint32(frame[offset+2])<<16
		offset += 3
		if (header>>1)&0x3 != 0 {
			return nil, errors.New("zstd block is not raw")
		}
		size := int(header >> 3)
		if offset+size > len(frame) {
			return nil, errors.New("truncated zstd block")
		}
		output = append(output, frame[offset:offset+size]...)
		offset += size
		if header&1 != 0 {
			break
		}
	}
	if offset != len(frame) {
		return nil, errors.New("trailing zstd bytes")
	}
	return output, nil
}
