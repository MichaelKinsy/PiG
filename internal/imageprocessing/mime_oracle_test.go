package imageprocessing

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

func pngChunk(kind string, length uint32, body int) []byte {
	out := binary.BigEndian.AppendUint32(nil, length)
	out = append(out, kind...)
	out = append(out, make([]byte, body)...)
	return append(out, 0, 0, 0, 0)
}

func bmpHeader(fileSize, pixelOffset, dib uint32, planes, bpp uint16, total int) []byte {
	out := make([]byte, total)
	copy(out, "BM")
	binary.LittleEndian.PutUint32(out[2:], fileSize)
	binary.LittleEndian.PutUint32(out[10:], pixelOffset)
	binary.LittleEndian.PutUint32(out[14:], dib)
	if dib == 12 && total >= 26 {
		binary.LittleEndian.PutUint16(out[22:], planes)
		binary.LittleEndian.PutUint16(out[24:], bpp)
	} else if total >= 30 {
		binary.LittleEndian.PutUint16(out[26:], planes)
		binary.LittleEndian.PutUint16(out[28:], bpp)
	}
	return out
}

// Pi's detectSupportedImageMimeType (utils/mime.ts) runs against the vendored Pi module for the same buffers: signatures, truncated prefixes of each, PNG chunk walks (acTL, IDAT, huge chunk lengths), BMP header consistency, and seeded single-byte mutations.
func TestDetectSupportedImageMimeTypeMatchesPiOracle(t *testing.T) {
	corpus := mimeOracleCorpus()
	encoded := make([]string, len(corpus))
	for i, buffer := range corpus {
		encoded[i] = hex.EncodeToString(buffer)
	}
	var want []string
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/utils/mime.js");
emit(input.map((h) => mod.detectSupportedImageMimeType(Buffer.from(h, "hex")) ?? ""));`, encoded, &want)
	mismatches := 0
	for i, buffer := range corpus {
		if got := DetectSupportedImageMimeType(buffer); got != want[i] {
			if mismatches++; mismatches <= 10 {
				t.Errorf("%x: Go %q, Pi %q", buffer[:min(len(buffer), 40)], got, want[i])
			}
		}
	}
	if mismatches > 10 {
		t.Errorf("%d mismatches in %d buffers", mismatches, len(corpus))
	}
}

// detectSupportedImageMimeTypeFromFile (utils/mime.ts) reads the first 4100 bytes of a file: the same buffers, each written to a file and padded past the sniff window, an empty file, a missing path and a directory (Pi rejects, Pig returns an error).
func TestDetectSupportedImageMimeTypeFromFileMatchesPiOracle(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	var buffers [][]byte
	add := func(buffer []byte) {
		path := filepath.Join(dir, fmt.Sprintf("f%d", len(paths)))
		if err := os.WriteFile(path, buffer, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		buffers = append(buffers, buffer)
	}
	for i, buffer := range mimeOracleCorpus() {
		add(buffer)
		if i%7 == 0 {
			// Content after the sniff window must not change the answer.
			add(append(append([]byte{}, buffer...), bytes.Repeat([]byte{0x5a}, 4200)...))
		}
	}
	paths = append(paths, filepath.Join(dir, "missing"), dir)
	buffers = append(buffers, nil, nil)
	var want []string
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/utils/mime.js");
const out = [];
for (const path of input) {
  try { out.push((await mod.detectSupportedImageMimeTypeFromFile(path)) ?? ""); } catch { out.push("<rejected>"); }
}
emit(out);`, paths, &want)
	mismatches := 0
	for i, path := range paths {
		got, err := DetectSupportedImageMimeTypeFromFile(path)
		if err != nil {
			got = "<rejected>"
		}
		if got != want[i] {
			if mismatches++; mismatches <= 10 {
				t.Errorf("%s (%d bytes): Go %q, Pi %q", filepath.Base(path), len(buffers[i]), got, want[i])
			}
		}
	}
	if mismatches > 10 {
		t.Errorf("%d mismatches in %d files", mismatches, len(paths))
	}
}

func mimeOracleCorpus() [][]byte {
	signature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	ihdr := pngChunk("IHDR", 13, 13)
	png := func(chunks ...[]byte) []byte {
		out := append([]byte{}, signature...)
		for _, chunk := range chunks {
			out = append(out, chunk...)
		}
		return out
	}
	seeds := [][]byte{
		nil, {0xff}, {0xff, 0xd8}, {0xff, 0xd8, 0xff}, {0xff, 0xd8, 0xff, 0xe0}, {0xff, 0xd8, 0xff, 0xf7},
		signature, png(ihdr), png(ihdr, pngChunk("IDAT", 2, 2)), png(ihdr, pngChunk("acTL", 8, 8), pngChunk("IDAT", 0, 0)),
		png(ihdr, pngChunk("tEXt", 3, 3), pngChunk("acTL", 8, 8)), png(ihdr, pngChunk("tEXt", 0xffffffff, 4), pngChunk("acTL", 8, 8)),
		png(ihdr, pngChunk("tEXt", 0xfffffff0, 0)), png(pngChunk("IHDR", 12, 12)), png(pngChunk("IHDX", 13, 13)),
		[]byte("GIF87a"), []byte("GIF89a"), []byte("GIF88a"), []byte("GIF89"), []byte("RIFF\x00\x00\x00\x00WEBP"), []byte("RIFF\x00\x00\x00\x00WEB"), []byte("RIFFWEBP"),
		[]byte("BM"), []byte("hello"), []byte("BM is text"),
		bmpHeader(0, 54, 40, 1, 24, 64), bmpHeader(0, 26, 12, 1, 8, 40), bmpHeader(100, 54, 40, 1, 32, 64), bmpHeader(25, 54, 40, 1, 32, 64),
		bmpHeader(54, 54, 40, 1, 32, 64), bmpHeader(0, 53, 40, 1, 24, 64), bmpHeader(0, 54, 40, 2, 24, 64), bmpHeader(0, 54, 40, 1, 2, 64),
		bmpHeader(0, 54, 40, 1, 24, 29), bmpHeader(0, 54, 40, 1, 24, 30), bmpHeader(0, 138, 124, 1, 16, 160), bmpHeader(0, 139, 125, 1, 16, 160),
		bmpHeader(0, 54, 39, 1, 24, 64), bmpHeader(0xffffffff, 0xfffffff0, 40, 1, 24, 64), bmpHeader(0, 0xfffffff0, 0xffffffff, 1, 24, 64), bmpHeader(0, 26, 12, 1, 4, 25),
	}
	var corpus [][]byte
	for _, seed := range seeds {
		corpus = append(corpus, seed)
		for n := 0; n < len(seed) && n < 80; n++ {
			corpus = append(corpus, seed[:n])
		}
	}
	rng := rand.New(rand.NewSource(7))
	for _, seed := range seeds {
		if len(seed) == 0 {
			continue
		}
		for range 60 {
			mutated := append([]byte{}, seed...)
			mutated[rng.Intn(len(mutated))] = byte(rng.Intn(256))
			corpus = append(corpus, mutated)
		}
	}
	return corpus
}
