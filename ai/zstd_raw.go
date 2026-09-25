package ai

import "encoding/binary"

const (
	zstdRawBlockMax               = 128 * 1024
	zstdSingleByteContentRange    = 1 << 8
	zstdTwoByteContentRange       = 1 << 16
	zstdTwoByteContentValueOffset = 1 << 8
)

// encodeZstdRawFrame emits a standards-compliant single-segment Zstandard
// frame. Raw blocks preserve the exact JSON bytes while avoiding a native or
// subprocess compressor dependency; the Codex endpoint decodes it like any
// other zstd frame.
func encodeZstdRawFrame(data []byte) []byte {
	frame := make([]byte, 0, len(data))
	frame = append(frame, 0x28, 0xb5, 0x2f, 0xfd)
	size := uint64(len(data))
	switch {
	case size < zstdSingleByteContentRange:
		frame = append(frame, 0x20, byte(size))
	case size < zstdTwoByteContentRange+zstdTwoByteContentValueOffset:
		frame = append(frame, 0x60)
		var encoded [2]byte
		binary.LittleEndian.PutUint16(encoded[:], uint16(size-zstdTwoByteContentValueOffset))
		frame = append(frame, encoded[:]...)
	case size <= uint64(^uint32(0)):
		frame = append(frame, 0xa0)
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], uint32(size))
		frame = append(frame, encoded[:]...)
	default:
		frame = append(frame, 0xe0)
		var encoded [8]byte
		binary.LittleEndian.PutUint64(encoded[:], size)
		frame = append(frame, encoded[:]...)
	}
	for offset := 0; offset < len(data) || (len(data) == 0 && offset == 0); {
		blockSize := min(zstdRawBlockMax, len(data)-offset)
		last := offset+blockSize == len(data)
		header := uint32(blockSize) << 3
		if last {
			header++
		}
		frame = append(frame, byte(header), byte(header>>8), byte(header>>16))
		frame = append(frame, data[offset:offset+blockSize]...)
		offset += blockSize
		if last {
			break
		}
	}
	return frame
}
