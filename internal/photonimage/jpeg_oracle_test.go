package photonimage

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/draw"
	"image/png"
	"os"
	"strconv"
	"strings"
	"testing"
)

// corpusRNG is jpeg_oracle.mjs rng: xorshift32.
type corpusRNG uint32

func (r *corpusRNG) next() uint32 {
	s := uint32(*r)
	s ^= s << 13
	s ^= s >> 17
	s ^= s << 5
	*r = corpusRNG(s)
	return s
}

// corpusHash is jpeg_oracle.mjs hash32.
func corpusHash(a uint32) uint32 {
	a = (a ^ a>>16) * 0x45d9f3b
	a = (a ^ a>>16) * 0x45d9f3b
	return a ^ a>>16
}

func corpusClamp(v int) byte {
	return byte(min(max(v, 0), 255))
}

// corpusLattice is jpeg_oracle.mjs lattice: one octave of integer value noise.
func corpusLattice(channel, octave, size, x, y int) int {
	value := func(i, j int) int {
		return int(corpusHash(uint32(i)*73856093^uint32(j)*19349663^uint32(channel+1)*83492791^uint32(octave+1)*668265263) >> 24)
	}
	i, j, fx, fy := x/size, y/size, x%size, y%size
	sum := value(i, j)*(size-fx)*(size-fy) + value(i+1, j)*fx*(size-fy) + value(i, j+1)*(size-fx)*fy + value(i+1, j+1)*fx*fy
	return sum / (size * size)
}

var corpusFlats = map[string][4]byte{
	"white": {255, 255, 255, 255}, "black": {0, 0, 0, 255}, "red": {255, 0, 0, 255}, "green": {0, 255, 0, 255}, "blue": {0, 0, 255, 255},
	"gray": {128, 128, 128, 255}, "clear": {10, 200, 30, 0}, "half": {255, 128, 0, 128},
}

// corpusPixel is the pixel (x, y) of the generator kind in jpeg_oracle.mjs.
func corpusPixel(kind string, flat [4]byte, x, y, w, h int, rnd *corpusRNG) [4]byte {
	switch kind {
	case "flat":
		return flat
	case "noise":
		return [4]byte{byte(rnd.next()), byte(rnd.next() >> 8), byte(rnd.next() >> 16), 255}
	case "noisea":
		return [4]byte{byte(rnd.next()), byte(rnd.next() >> 8), byte(rnd.next() >> 16), byte(rnd.next() >> 24)}
	case "hgrad":
		return [4]byte{byte(x * 255 / max(1, w-1)), byte(y * 255 / max(1, h-1)), byte((x + y) * 255 / max(1, w+h-2)), 255}
	case "diag":
		return [4]byte{byte(x*3 + y*5), byte(x * 7), byte(y*11 + x), byte(255 - ((x + y) & 63))}
	case "checker":
		if (x+y)&1 != 0 {
			return [4]byte{255, 255, 255, 255}
		}
		return [4]byte{0, 0, 0, 255}
	case "photo":
		pixel := [4]byte{0, 0, 0, 255}
		for c := range 3 {
			v := (corpusLattice(c, 0, 32, x, y)*5 + corpusLattice(c, 1, 8, x, y)*3) >> 3
			pixel[c] = corpusClamp(v + int(rnd.next()&7) - 3)
		}
		return pixel
	case "screenshot":
		if y < 24 {
			return [4]byte{byte(40 + (x>>3)&31), 60, byte(90 + (x>>4)&63), 255}
		}
		if x >= 20 && x < 120 && y >= 40 && y < 64 {
			return [4]byte{0, 102, 204, 255}
		}
		if x%64 == 0 || y%64 == 0 {
			return [4]byte{200, 200, 200, 255}
		}
		if row := (y - 24) % 14; row >= 3 && row < 10 {
			cell := corpusHash(uint32(x/6+1)*2654435761 ^ uint32((y-24)/14+1)*40503)
			if cell%3 != 0 && x%6 != 5 {
				edge := byte(corpusHash(uint32(x+1)*374761393^uint32(y+1)*668265263) % 3)
				v := 30 + edge*60
				return [4]byte{v, v, v, 255}
			}
		}
		return [4]byte{245, 245, 245, 255}
	}
	panic("unknown corpus kind " + kind)
}

// corpusImage is the image `name` of the corpus: the ordinal-th case of jpeg_oracle.mjs, whose seed is ordinal+1.
func corpusImage(name string, ordinal, w, h int) *image.NRGBA {
	kind, rest, _ := strings.Cut(name, "-")
	var flat [4]byte
	if kind == "flat" {
		colour, _, _ := strings.Cut(rest, "-")
		flat = corpusFlats[colour]
	}
	seed := uint32(ordinal+1) * 2654435761
	if seed == 0 {
		seed = 1
	}
	rnd := corpusRNG(seed)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			pixel := corpusPixel(kind, flat, x, y, w, h, &rnd)
			copy(img.Pix[img.PixOffset(x, y):], pixel[:])
		}
	}
	return img
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestEncodeJPEGWritesPhotonsBytes encodes the corpus of testdata/jpeg_oracle.mjs, which Photon's `get_bytes_jpeg(quality)` encoded
// (@silvia-odwyer/photon 0.3.3 and @silvia-odwyer/photon-node 0.3.4, both image 0.24.9; regenerate testdata/photon-jpeg-corpus.tsv
// with `node jpeg_oracle.mjs <package directory>`): photos and screenshots, gradients and noise, alpha (including transparent pixels
// with colour), flat extremes, one-pixel and odd sizes, a 12 MP photo, every quality Pi uses and every quality from 1 to 100 for two
// images. Every length and SHA-256 must be Photon's, and where Photon panics (a coefficient the standard Huffman table cannot code)
// EncodeJPEG must fail.
func TestEncodeJPEGWritesPhotonsBytes(t *testing.T) {
	file, err := os.Open("testdata/photon-jpeg-corpus.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	var (
		img                  *image.NRGBA
		name                 string
		ordinal              = -1
		images, same, panics int
		failures             int
	)
	fail := func(format string, args ...any) {
		failures++
		if failures <= 12 {
			t.Errorf(format, args...)
		}
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		switch fields[0] {
		case "image":
			ordinal++
			name = fields[1]
			w, _ := strconv.Atoi(fields[2])
			h, _ := strconv.Atoi(fields[3])
			img = corpusImage(name, ordinal, w, h)
			if got := sha256Hex(img.Pix); got != fields[4] {
				t.Fatalf("%s: the Go generator makes pixels with SHA-256 %s, jpeg_oracle.mjs %s", name, got, fields[4])
			}
			images++
		case "jpeg":
			quality, _ := strconv.Atoi(fields[2])
			got, err := EncodeJPEG(img, uint8(quality))
			if fields[3] == "error" {
				if err == nil {
					fail("%s q%d: Photon panics, EncodeJPEG wrote %d bytes", name, quality, len(got))
					continue
				}
				panics++
				continue
			}
			if err != nil {
				fail("%s q%d: %v", name, quality, err)
				continue
			}
			if length := strconv.Itoa(len(got)); length != fields[3] || sha256Hex(got) != fields[4] {
				fail("%s q%d: %d bytes, SHA-256 %s; Photon's %s bytes, %s", name, quality, len(got), sha256Hex(got), fields[3], fields[4])
				continue
			}
			same++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d images: %d JPEGs identical to Photon's, %d panics in Photon reproduced as errors, %d differences", images, same, panics, failures)
}

// testdata/scene-photon-300x200-q80.jpg is Photon's own `get_bytes_jpeg(80)` of the pixels of testdata/scene-photon-300x200.png (see
// durable/images/photon_differential_test.go): those pixels encode to it byte for byte.
func TestEncodeJPEGWritesPhotonsBytesForTheScene(t *testing.T) {
	pngBytes, err := os.ReadFile("testdata/scene-photon-300x200.png")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/scene-photon-300x200-q80.jpg")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	scene := image.NewNRGBA(decoded.Bounds())
	draw.Draw(scene, scene.Rect, decoded, image.Point{}, draw.Src)
	got, err := EncodeJPEG(scene, 80)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("scene: %d bytes, want Photon's %d", len(got), len(want))
	}
}
