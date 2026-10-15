package imageprocessing

import (
	"encoding/binary"
	"encoding/hex"
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/utils/exif-orientation.ts

// exifJS is getExifOrientation of Pi's utils/exif-orientation.ts, copied unchanged apart from the type annotations: the module exports only
// applyExifOrientation, which needs the photon image library. The scan of a RIFF chunk list that never advances (a negative chunk size) is capped
// and reported as "loop", where Pi would hang.
const exifJS = `
function readOrientationFromTiff(bytes, tiffStart) {
	if (tiffStart + 8 > bytes.length) return 1;
	const byteOrder = (bytes[tiffStart] << 8) | bytes[tiffStart + 1];
	const le = byteOrder === 0x4949;
	const read16 = (pos) => { if (le) return bytes[pos] | (bytes[pos + 1] << 8); return (bytes[pos] << 8) | bytes[pos + 1]; };
	const read32 = (pos) => {
		if (le) return bytes[pos] | (bytes[pos + 1] << 8) | (bytes[pos + 2] << 16) | (bytes[pos + 3] << 24);
		return ((bytes[pos] << 24) | (bytes[pos + 1] << 16) | (bytes[pos + 2] << 8) | bytes[pos + 3]) >>> 0;
	};
	const ifdOffset = read32(tiffStart + 4);
	const ifdStart = tiffStart + ifdOffset;
	if (ifdStart + 2 > bytes.length) return 1;
	const entryCount = read16(ifdStart);
	for (let i = 0; i < entryCount; i++) {
		const entryPos = ifdStart + 2 + i * 12;
		if (entryPos + 12 > bytes.length) return 1;
		if (read16(entryPos) === 0x0112) {
			const value = read16(entryPos + 8);
			return value >= 1 && value <= 8 ? value : 1;
		}
	}
	return 1;
}
function hasExifHeader(bytes, offset) {
	return bytes[offset] === 0x45 && bytes[offset + 1] === 0x78 && bytes[offset + 2] === 0x69 && bytes[offset + 3] === 0x66 && bytes[offset + 4] === 0x00 && bytes[offset + 5] === 0x00;
}
function findJpegTiffOffset(bytes) {
	let offset = 2;
	while (offset < bytes.length - 1) {
		if (bytes[offset] !== 0xff) return -1;
		const marker = bytes[offset + 1];
		if (marker === 0xff) { offset++; continue; }
		if (marker === 0xe1) {
			if (offset + 4 >= bytes.length) return -1;
			const segmentStart = offset + 4;
			if (segmentStart + 6 > bytes.length) return -1;
			if (hasExifHeader(bytes, segmentStart)) return segmentStart + 6;
		}
		if (offset + 4 > bytes.length) return -1;
		const length = (bytes[offset + 2] << 8) | bytes[offset + 3];
		offset += 2 + length;
	}
	return -1;
}
function findWebpTiffOffset(bytes) {
	let offset = 12;
	let steps = 0;
	while (offset + 8 <= bytes.length) {
		if (++steps > 10000) return "loop";
		const chunkId = String.fromCharCode(bytes[offset], bytes[offset + 1], bytes[offset + 2], bytes[offset + 3]);
		const chunkSize = bytes[offset + 4] | (bytes[offset + 5] << 8) | (bytes[offset + 6] << 16) | (bytes[offset + 7] << 24);
		const dataStart = offset + 8;
		if (chunkId === "EXIF") {
			if (dataStart + chunkSize > bytes.length) return -1;
			const tiffStart = chunkSize >= 6 && hasExifHeader(bytes, dataStart) ? dataStart + 6 : dataStart;
			return tiffStart;
		}
		offset = dataStart + chunkSize + (chunkSize % 2);
	}
	return -1;
}
function getExifOrientation(bytes) {
	let tiffOffset = -1;
	if (bytes.length >= 2 && bytes[0] === 0xff && bytes[1] === 0xd8) {
		tiffOffset = findJpegTiffOffset(bytes);
	} else if (bytes.length >= 12 && bytes[0] === 0x52 && bytes[1] === 0x49 && bytes[2] === 0x46 && bytes[3] === 0x46 && bytes[8] === 0x57 && bytes[9] === 0x45 && bytes[10] === 0x42 && bytes[11] === 0x50) {
		tiffOffset = findWebpTiffOffset(bytes);
	}
	if (tiffOffset === "loop") return "loop";
	if (tiffOffset === -1) return 1;
	return readOrientationFromTiff(bytes, tiffOffset);
}
const results = input.map((hex) => getExifOrientation(Uint8Array.from(Buffer.from(hex, "hex"))));
emit(results);`

func TestExifOrientationMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(211))
	u16 := func(le bool, v uint16) []byte {
		if le {
			return binary.LittleEndian.AppendUint16(nil, v)
		}
		return binary.BigEndian.AppendUint16(nil, v)
	}
	u32 := func(le bool, v uint32) []byte {
		if le {
			return binary.LittleEndian.AppendUint32(nil, v)
		}
		return binary.BigEndian.AppendUint32(nil, v)
	}
	// tiff builds a TIFF block: a byte order mark, an IFD offset, and entries with random tags (the orientation tag among them) and values.
	tiff := func() []byte {
		le := rng.Intn(2) == 0
		out := []byte("MM")
		if le {
			out = []byte("II")
		}
		if rng.Intn(15) == 0 {
			out = []byte{byte(rng.Intn(256)), byte(rng.Intn(256))}
		}
		out = append(out, u16(le, 42)...)
		offset := uint32(8)
		switch rng.Intn(8) {
		case 0:
			offset = uint32(rng.Intn(40))
		case 1:
			offset = 0x80000000 + uint32(rng.Intn(4))
		case 2:
			offset = 0xfffffff0 + uint32(rng.Intn(16))
		case 3:
			offset = uint32(rng.Intn(1 << 20))
		}
		out = append(out, u32(le, offset)...)
		count := rng.Intn(6)
		entries := count
		if rng.Intn(6) == 0 {
			entries = rng.Intn(70000)
		}
		out = append(out, u16(le, uint16(entries))...)
		for i := range count {
			tag := uint16(0x0100 + i)
			if rng.Intn(3) == 0 {
				tag = 0x0112
			}
			value := uint16(rng.Intn(11))
			if rng.Intn(6) == 0 {
				value = uint16(rng.Intn(65536))
			}
			entry := append(u16(le, tag), u16(le, 3)...)
			entry = append(entry, u32(le, 1)...)
			entry = append(entry, u16(le, value)...)
			entry = append(entry, 0, 0)
			out = append(out, entry...)
		}
		return out
	}
	exif := []byte("Exif\x00\x00")
	jpeg := func() []byte {
		out := []byte{0xff, 0xd8}
		for range rng.Intn(4) {
			switch rng.Intn(6) {
			case 0:
				out = append(out, 0xff)
			case 1:
				body := make([]byte, rng.Intn(20))
				out = append(out, 0xff, 0xe0, byte((len(body)+2)>>8), byte(len(body)+2))
				out = append(out, body...)
			case 2:
				body := append([]byte("http://ns.adobe.com/xap/1.0/\x00"), make([]byte, rng.Intn(10))...)
				out = append(out, 0xff, 0xe1, byte((len(body)+2)>>8), byte(len(body)+2))
				out = append(out, body...)
			default:
				out = append(out, 0xff, byte(0xc0+rng.Intn(16)), byte(rng.Intn(2)), byte(rng.Intn(20)))
				out = append(out, make([]byte, rng.Intn(20))...)
			}
		}
		body := append(append([]byte{}, exif...), tiff()...)
		length := len(body) + 2
		if rng.Intn(10) == 0 {
			length = rng.Intn(70000)
		}
		out = append(out, 0xff, 0xe1, byte(length>>8), byte(length))
		return append(out, body...)
	}
	webp := func() []byte {
		out := []byte("RIFF\x00\x00\x00\x00WEBP")
		for range rng.Intn(3) {
			size := rng.Intn(12)
			out = append(out, "VP8 "...)
			out = binary.LittleEndian.AppendUint32(out, uint32(size))
			out = append(out, make([]byte, size+size%2)...)
		}
		body := tiff()
		if rng.Intn(2) == 0 {
			body = append(append([]byte{}, exif...), body...)
		}
		size := uint32(len(body))
		switch rng.Intn(12) {
		case 0:
			size = uint32(rng.Intn(1 << 20))
		case 1:
			size = 0xffffff00 + uint32(rng.Intn(256))
		case 2:
			size = uint32(len(body)) + 1
		}
		id := "EXIF"
		if rng.Intn(8) == 0 {
			id = "ICCP"
		}
		out = append(out, id...)
		out = binary.LittleEndian.AppendUint32(out, size)
		return append(out, body...)
	}
	var inputs []string
	for range 4000 {
		var data []byte
		switch rng.Intn(5) {
		case 0, 1:
			data = jpeg()
		case 2, 3:
			data = webp()
		default:
			data = make([]byte, rng.Intn(40))
			rng.Read(data)
		}
		if rng.Intn(3) == 0 && len(data) > 0 {
			data = data[:rng.Intn(len(data)+1)]
		}
		if rng.Intn(4) == 0 && len(data) > 0 {
			data[rng.Intn(len(data))] = byte(rng.Intn(256))
		}
		inputs = append(inputs, hex.EncodeToString(data))
	}
	var want []any
	pioracle.Run(t, exifJS, inputs, &want)
	orientations := map[int]int{}
	skipped := 0
	for i, in := range inputs {
		data, _ := hex.DecodeString(in)
		got := int(getExifOrientation(data))
		if want[i] == "loop" {
			skipped++
			continue
		}
		orientations[got]++
		if float64(got) != want[i] {
			t.Errorf("%s: PiG %d, Pi %v", in, got, want[i])
		}
	}
	// An oracle whose inputs never reach a real orientation would pass vacuously.
	if orientations[1] == len(inputs)-skipped || len(orientations) < 5 {
		t.Fatalf("orientations seen: %v (skipped %d)", orientations, skipped)
	}
}
