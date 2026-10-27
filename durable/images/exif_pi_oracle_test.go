package images_test

// pi: packages/durable/src/images/exif.ts

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/images"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's own tools-images.test.ts exercises exifOrientation on well-formed JPEG and WebP inputs. The inputs below are the
// malformed and boundary shapes it leaves out: a directory with several entries, an offset or entry count beyond the
// data, segment and chunk walking (fill bytes, a non-marker byte, zero-length segments, odd chunk padding), a foreign
// RIFF type, a damaged Exif prefix, and every truncation of a valid file. Pi's real exifOrientation (exif.ts, imported
// as-is with Node's type stripping) answers each one; ExifOrientation must answer the same, and must not read past the end
// of the data, which a Uint8Array reports as undefined and Go reports as a panic.

type ifdEntry struct {
	tag, kind uint16
	count     uint32
	value     uint16
}

// tiffOf is a TIFF header whose first directory (at offset 8, whatever directoryOffset says) holds entries.
func tiffOf(little bool, directoryOffset uint32, entries ...ifdEntry) []byte {
	var order binary.ByteOrder = binary.BigEndian
	out := []byte{0x4d, 0x4d}
	if little {
		order, out = binary.LittleEndian, []byte{0x49, 0x49}
	}
	out = append(out, make([]byte, 6+2+12*len(entries))...)
	order.PutUint16(out[2:], 42)
	order.PutUint32(out[4:], directoryOffset)
	order.PutUint16(out[8:], uint16(len(entries)))
	for i, e := range entries {
		at := 10 + 12*i
		order.PutUint16(out[at:], e.tag)
		order.PutUint16(out[at+2:], e.kind)
		order.PutUint32(out[at+4:], e.count)
		order.PutUint16(out[at+8:], e.value)
	}
	return out
}

func orientationEntry(value uint16) ifdEntry { return ifdEntry{0x0112, 3, 1, value} }

var widthEntry = ifdEntry{0x0100, 3, 1, 640}

// jpegWith is a JPEG start of image, then the given segments, then an end of image.
func jpegWith(segments ...[]byte) []byte {
	out := []byte{0xff, 0xd8}
	for _, segment := range segments {
		out = append(out, segment...)
	}
	return append(out, 0xff, 0xd9)
}

// segment is a marker segment: 0xff, marker, a big-endian length counting itself, then data.
func segment(marker byte, data []byte) []byte {
	return append([]byte{0xff, marker, byte((len(data) + 2) >> 8), byte(len(data) + 2)}, data...)
}

func app1(header string, tiff []byte) []byte { return segment(0xe1, append([]byte(header), tiff...)) }

// webpOf is a RIFF WebP made of the given chunks, each padded to an even size.
func webpOf(form string, chunks ...[]byte) []byte {
	body := []byte(form)
	for _, c := range chunks {
		body = append(body, c...)
	}
	return append(append([]byte("RIFF"), byte(len(body)), byte(len(body)>>8), 0, 0), body...)
}

func chunkOf(kind string, data []byte) []byte {
	out := append([]byte(kind), byte(len(data)), byte(len(data)>>8), 0, 0)
	out = append(out, data...)
	if len(data)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

type exifCase struct {
	name string
	data []byte
}

func exifOracleCorpus() []exifCase {
	exifPrefix := "Exif\x00\x00"
	var cases []exifCase
	add := func(name string, data []byte) { cases = append(cases, exifCase{name, bytes.Clone(data)}) }

	for _, little := range []bool{false, true} {
		end := map[bool]string{false: "big", true: "little"}[little]
		add(end+" orientation second of two entries", jpegWith(app1(exifPrefix, tiffOf(little, 8, widthEntry, orientationEntry(6)))))
		add(end+" orientation after two other entries", jpegWith(app1(exifPrefix, tiffOf(little, 8, widthEntry, widthEntry, orientationEntry(8)))))
		add(end+" orientation 0", jpegWith(app1(exifPrefix, tiffOf(little, 8, orientationEntry(0)))))
		add(end+" orientation 8", jpegWith(app1(exifPrefix, tiffOf(little, 8, orientationEntry(8)))))
		add(end+" orientation 9", jpegWith(app1(exifPrefix, tiffOf(little, 8, orientationEntry(9)))))
		add(end+" orientation 0xffff", jpegWith(app1(exifPrefix, tiffOf(little, 8, orientationEntry(0xffff)))))
		add(end+" no orientation entry", jpegWith(app1(exifPrefix, tiffOf(little, 8, widthEntry))))
		add(end+" empty directory", jpegWith(app1(exifPrefix, tiffOf(little, 8))))
		add(end+" the first orientation entry wins", jpegWith(app1(exifPrefix, tiffOf(little, 8, orientationEntry(3), orientationEntry(6)))))
		add(end+" a directory offset with a high byte set", jpegWith(app1(exifPrefix, tiffOf(little, 8+1<<24, orientationEntry(6)))))
		add(end+" a directory offset of 2^32-8", jpegWith(app1(exifPrefix, tiffOf(little, 0xfffffff8, orientationEntry(6)))))
		add(end+" a directory offset of 0", jpegWith(app1(exifPrefix, tiffOf(little, 0, orientationEntry(6)))))
		add(end+" a webp with an orientation second", webpOf("WEBP", chunkOf("VP8 ", []byte{1, 2, 3}), chunkOf("EXIF", append([]byte(exifPrefix), tiffOf(little, 8, widthEntry, orientationEntry(7))...))))
		// An entry count beyond the data: the walk stops at the end and answers 1.
		huge := tiffOf(little, 8, widthEntry)
		if little {
			binary.LittleEndian.PutUint16(huge[8:], 0xffff)
		} else {
			binary.BigEndian.PutUint16(huge[8:], 0xffff)
		}
		add(end+" an entry count beyond the data", jpegWith(app1(exifPrefix, huge)))
		// Every directory offset across and just past the data.
		for offset := range uint32(70) {
			add(fmt.Sprintf("%s directory offset %d", end, offset), jpegWith(app1(exifPrefix, tiffOf(little, offset, orientationEntry(6)))))
		}
		// Every truncation of a JPEG and of a WebP that read 6 or 5.
		full := jpegWith(app1(exifPrefix, tiffOf(little, 8, widthEntry, orientationEntry(6))))
		for n := range len(full) {
			add(fmt.Sprintf("%s jpeg cut to %d bytes", end, n), full[:n])
		}
		webp := webpOf("WEBP", chunkOf("VP8 ", []byte{1, 2, 3}), chunkOf("EXIF", append([]byte(exifPrefix), tiffOf(little, 8, widthEntry, orientationEntry(5))...)))
		for n := range len(webp) {
			add(fmt.Sprintf("%s webp cut to %d bytes", end, n), webp[:n])
		}
		bare := webpOf("WEBP", chunkOf("EXIF", tiffOf(little, 8, widthEntry, orientationEntry(4))))
		for n := range len(bare) {
			add(fmt.Sprintf("%s bare webp cut to %d bytes", end, n), bare[:n])
		}
	}

	for orientation := uint16(1); orientation <= 8; orientation++ {
		add(fmt.Sprintf("jpeg orientation %d", orientation), jpegWith(app1(exifPrefix, tiffOf(orientation%2 == 0, 8, orientationEntry(orientation)))))
		add(fmt.Sprintf("webp orientation %d", orientation), webpOf("WEBP", chunkOf("EXIF", tiffOf(orientation%2 == 1, 8, orientationEntry(orientation)))))
	}

	good := tiffOf(false, 8, orientationEntry(6))
	app0 := segment(0xe0, []byte("JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00"))
	// How the walk moves from segment to segment.
	add("a non-marker byte before an APP1", append(append([]byte{0xff, 0xd8, 0x00}, app1(exifPrefix, good)...), 0xff, 0xd9))
	add("a non-marker byte after an APP0", append(append(append([]byte{0xff, 0xd8}, app0...), 0x00), app1(exifPrefix, good)...))
	for fill := 1; fill <= 3; fill++ {
		add(fmt.Sprintf("%d fill bytes before the APP1", fill), append(append([]byte{0xff, 0xd8}, bytes.Repeat([]byte{0xff}, fill)...), app1(exifPrefix, good)...))
		add(fmt.Sprintf("%d fill bytes after an APP0", fill), append(append(append([]byte{0xff, 0xd8}, app0...), bytes.Repeat([]byte{0xff}, fill)...), app1(exifPrefix, good)...))
	}
	add("a first byte of 0xff that is no start of image, then an APP1", append([]byte{0xff, 0x00}, app1(exifPrefix, good)...))
	add("a start of image missing its second byte", append([]byte{0xff, 0xd9}, app1(exifPrefix, good)...))
	add("two APPn segments before the APP1", jpegWith(app0, segment(0xe2, []byte("ICC_PROFILE\x00")), app1(exifPrefix, good)))
	add("an APP1 that is not Exif, then the Exif one", jpegWith(segment(0xe1, []byte("http://ns.adobe.com/xap/1.0/\x00<x/>")), app1(exifPrefix, good)))
	add("a zero-length segment before the APP1", jpegWith([]byte{0xff, 0xe0, 0, 0}, app1(exifPrefix, good)))
	add("a length-1 segment before the APP1", jpegWith([]byte{0xff, 0xe0, 0, 1}, app1(exifPrefix, good)))
	add("a length-2 segment before the APP1", jpegWith([]byte{0xff, 0xe0, 0, 2}, app1(exifPrefix, good)))
	add("a segment longer than the data", jpegWith([]byte{0xff, 0xe0, 0xff, 0xff}, app1(exifPrefix, good)))
	add("the Exif APP1 in an APP0 marker", jpegWith(segment(0xe0, append([]byte(exifPrefix), good...))))
	add("the Exif APP1 in an APP2 marker", jpegWith(segment(0xe2, append([]byte(exifPrefix), good...))))
	add("an end of image before the APP1", append([]byte{0xff, 0xd8, 0xff, 0xd9}, app1(exifPrefix, good)...))
	add("no data after the start of image", []byte{0xff, 0xd8})
	add("three bytes", []byte{0xff, 0xd8, 0xff})
	add("four bytes", []byte{0xff, 0xd8, 0xff, 0xe1})
	add("empty", nil)
	add("one byte", []byte{0xff})

	// The Exif header that opens the APP1 data and the WebP EXIF chunk.
	for _, header := range []string{"Exif\x00\x00", "Exif\x00\x01", "Exif\x01\x00", "Exig\x00\x00", "exif\x00\x00", "Exif\x00", "Exif", ""} {
		add(fmt.Sprintf("jpeg APP1 with the header %q", header), jpegWith(app1(header, good)))
		add(fmt.Sprintf("webp EXIF with the header %q", header), webpOf("WEBP", chunkOf("EXIF", append([]byte(header), tiffOf(true, 8, orientationEntry(3))...))))
	}

	// How the WebP walk moves from chunk to chunk.
	little := tiffOf(true, 8, orientationEntry(2))
	add("a webp with the EXIF chunk first", webpOf("WEBP", chunkOf("EXIF", little)))
	add("a webp with an even chunk before the EXIF", webpOf("WEBP", chunkOf("VP8 ", []byte{1, 2, 3, 4}), chunkOf("EXIF", little)))
	add("a webp with an odd chunk before the EXIF", webpOf("WEBP", chunkOf("VP8 ", []byte{1, 2, 3}), chunkOf("EXIF", little)))
	add("a webp with three chunks before the EXIF", webpOf("WEBP", chunkOf("VP8X", make([]byte, 10)), chunkOf("ICCP", make([]byte, 7)), chunkOf("VP8 ", make([]byte, 5)), chunkOf("EXIF", little)))
	add("a webp without an EXIF chunk", webpOf("WEBP", chunkOf("VP8 ", []byte{1, 2, 3, 4})))
	add("a webp with a chunk of size 0 before the EXIF", webpOf("WEBP", chunkOf("VP8 ", nil), chunkOf("EXIF", little)))
	add("a RIFF WAVE with an EXIF chunk", webpOf("WAVE", chunkOf("EXIF", little)))
	add("a RIFF AVI with an EXIF chunk", webpOf("AVI ", chunkOf("EXIF", little)))
	add("a RIFX WEBP with an EXIF chunk", append([]byte("RIFX"), webpOf("WEBP", chunkOf("EXIF", little))[4:]...))
	add("a webp whose first chunk is declared longer than the data", append(webpOf("WEBP"), []byte{'V', 'P', '8', ' ', 0xff, 0xff, 0xff, 0x7f}...))
	add("a webp with a chunk size of 2^32-1", append(webpOf("WEBP"), []byte{'V', 'P', '8', ' ', 0xff, 0xff, 0xff, 0xff}...))
	add("a webp with only its header", webpOf("WEBP"))
	add("a webp with a partial chunk header", append(webpOf("WEBP"), 'E', 'X', 'I', 'F', 1, 0))
	add("a webp with a lower-case exif chunk", webpOf("WEBP", chunkOf("exif", little)))
	add("a png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))

	// Every single-byte change and every two-byte change at a seeded position of valid files, as a fuzz around the walk.
	bases := [][]byte{
		jpegWith(app0, app1(exifPrefix, tiffOf(false, 8, widthEntry, orientationEntry(6)))),
		jpegWith(app1(exifPrefix, tiffOf(true, 8, widthEntry, orientationEntry(3)))),
		webpOf("WEBP", chunkOf("VP8 ", []byte{1, 2, 3}), chunkOf("EXIF", append([]byte(exifPrefix), tiffOf(true, 8, orientationEntry(8))...))),
		webpOf("WEBP", chunkOf("EXIF", tiffOf(false, 8, widthEntry, orientationEntry(5)))),
	}
	interesting := []byte{0x00, 0x01, 0x02, 0x08, 0x0a, 0x0c, 0x12, 0x2a, 0x49, 0x4d, 0x7f, 0x80, 0xe0, 0xe1, 0xfe, 0xff}
	for b, base := range bases {
		for position := range base {
			for _, value := range interesting {
				mutated := bytes.Clone(base)
				mutated[position] = value
				add(fmt.Sprintf("base %d byte %d = %#x", b, position, value), mutated)
			}
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 3000 {
		base := bases[rng.IntN(len(bases))]
		mutated := bytes.Clone(base)
		for range 1 + rng.IntN(3) {
			mutated[rng.IntN(len(mutated))] = byte(rng.IntN(256))
		}
		if rng.IntN(4) == 0 {
			mutated = mutated[:rng.IntN(len(mutated)+1)]
		}
		add(fmt.Sprintf("random %d", i), mutated)
	}
	return cases
}

func TestExifOrientationMatchesPiOnMalformedAndBoundaryInputs(t *testing.T) {
	corpus := exifOracleCorpus()
	payload := make([]string, len(corpus))
	for i, c := range corpus {
		payload[i] = base64.StdEncoding.EncodeToString(c.data)
	}
	input, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/exif_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi []int
	if err := json.Unmarshal(output, &pi); err != nil || len(pi) != len(corpus) {
		t.Fatalf("oracle output: %v (%d answers for %d cases)", err, len(pi), len(corpus))
	}
	answers := map[int]int{}
	for i, c := range corpus {
		answers[pi[i]]++
		got := func() (orientation int) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("%s: PiG panicked (%v), Pi answers %d", c.name, recovered, pi[i])
					orientation = -1
				}
			}()
			return images.ExifOrientation(c.data)
		}()
		if got != -1 && got != pi[i] {
			t.Errorf("%s: PiG orientation %d, Pi %d", c.name, got, pi[i])
		}
	}
	// The corpus has to reach every orientation Pi can answer, or it would pass on a constant.
	for orientation := 1; orientation <= 8; orientation++ {
		if answers[orientation] == 0 {
			t.Errorf("no case makes Pi answer %d (answers %v)", orientation, answers)
		}
	}
}
