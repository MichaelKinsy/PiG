package images

import "bytes"

// Ports packages/durable/src/images/exif.ts: the EXIF orientation of JPEG and WebP files, read from the bytes without
// decoding the image.

// ExifOrientation is the EXIF orientation of a JPEG or WebP, 1 to 8; 1 when absent or unreadable.
func ExifOrientation(data []byte) int {
	tiff := tiffStart(data)
	if tiff < 0 || tiff+8 > len(data) {
		return 1
	}
	little := data[tiff] == 0x49 && data[tiff+1] == 0x49
	u16 := func(at int) int {
		if little {
			return int(data[at]) | int(data[at+1])<<8
		}
		return int(data[at])<<8 | int(data[at+1])
	}
	u32 := func(at int) int {
		if little {
			return int(uint32(data[at]) | uint32(data[at+1])<<8 | uint32(data[at+2])<<16 | uint32(data[at+3])<<24)
		}
		return int(uint32(data[at])<<24 | uint32(data[at+1])<<16 | uint32(data[at+2])<<8 | uint32(data[at+3]))
	}
	directory := tiff + u32(tiff+4)
	if directory+2 > len(data) {
		return 1
	}
	entries := u16(directory)
	for index := range entries {
		entry := directory + 2 + index*12
		if entry+12 > len(data) {
			return 1
		}
		// Tag 0x0112 is Orientation, a SHORT whose value sits in the entry.
		if u16(entry) == 0x0112 {
			value := u16(entry + 8)
			if value >= 1 && value <= 8 {
				return value
			}
			return 1
		}
	}
	return 1
}

// tiffStart is where the TIFF header of a JPEG's APP1 or a WebP's EXIF chunk starts; -1 when there is none.
func tiffStart(data []byte) int {
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xd8 {
		for offset := 2; offset+4 <= len(data); {
			if data[offset] != 0xff {
				return -1
			}
			marker := data[offset+1]
			if marker == 0xff {
				offset++
				continue
			}
			if marker == 0xe1 && isExifHeader(data, offset+4) {
				return offset + 10
			}
			offset += 2 + (int(data[offset+2])<<8 | int(data[offset+3]))
		}
		return -1
	}
	if ascii(data, 0, "RIFF") && ascii(data, 8, "WEBP") {
		for offset := 12; offset+8 <= len(data); {
			size := int(uint32(data[offset+4]) | uint32(data[offset+5])<<8 | uint32(data[offset+6])<<16 | uint32(data[offset+7])<<24)
			// Some WebP files prefix the TIFF header with "Exif\0\0".
			if ascii(data, offset, "EXIF") {
				if isExifHeader(data, offset+8) {
					return offset + 14
				}
				return offset + 8
			}
			// RIFF chunks are padded to an even size.
			offset += 8 + size + size%2
		}
	}
	return -1
}

func isExifHeader(data []byte, offset int) bool {
	return ascii(data, offset, "Exif") && at(data, offset+4) == 0 && at(data, offset+5) == 0
}

// ascii reports whether data holds text at offset. An offset beyond the data holds nothing, as for a Uint8Array.
func ascii(data []byte, offset int, text string) bool {
	return offset >= 0 && offset+len(text) <= len(data) && bytes.Equal(data[offset:offset+len(text)], []byte(text))
}

// at is data[offset], or a value that is none of the bytes tested when the offset is beyond the data (undefined).
func at(data []byte, offset int) int {
	if offset < 0 || offset >= len(data) {
		return -1
	}
	return int(data[offset])
}
