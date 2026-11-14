// Ports the fixtures of packages/durable/test/image-fixtures.ts that images-fit.test.ts uses: images encoded with Go's
// image codecs where Pi encodes with Photon, in the same shapes.

package fit_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func rgbaOf(width, height int, set func(index, x, y int) color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, set(y*width+x, x, y))
		}
	}
	return img
}

// pixels is a width by height image: red, with a green top-left pixel.
func pixels(width, height int) *image.NRGBA {
	return rgbaOf(width, height, func(_, x, y int) color.NRGBA {
		if x == 0 && y == 0 {
			return color.NRGBA{0, 255, 0, 255}
		}
		return color.NRGBA{255, 0, 0, 255}
	})
}

func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// encode is a width by height PNG, or a JPEG of quality 95.
func encode(t testing.TB, width, height int, format string) []byte {
	t.Helper()
	if format == "png" {
		return encodePNG(t, pixels(width, height))
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, pixels(width, height), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// noisyPng is noise, which neither PNG nor JPEG compresses well, so the byte limit decides: Pi's generator, one
// step of the seed for every byte of the RGBA pixels, the alpha steps included.
func noisyPng(t testing.TB, width, height int) []byte {
	t.Helper()
	seed := uint32(1)
	next := func() uint8 {
		seed = seed*1103515245 + 12345
		return uint8(seed >> 24)
	}
	return encodePNG(t, rgbaOf(width, height, func(_, _, _ int) color.NRGBA {
		pixel := color.NRGBA{A: 255}
		pixel.R = next()
		pixel.G = next()
		pixel.B = next()
		next()
		return pixel
	}))
}

// bmp is a 24-bit BMP, which providers do not take inline.
func bmp(width, height int) []byte {
	row := (width*3 + 3) / 4 * 4
	out := make([]byte, 54+row*height)
	copy(out, "BM")
	binary.LittleEndian.PutUint32(out[2:], uint32(len(out)))
	binary.LittleEndian.PutUint32(out[10:], 54)
	binary.LittleEndian.PutUint32(out[14:], 40)
	binary.LittleEndian.PutUint32(out[18:], uint32(width))
	binary.LittleEndian.PutUint32(out[22:], uint32(height))
	binary.LittleEndian.PutUint16(out[26:], 1)
	binary.LittleEndian.PutUint16(out[28:], 24)
	binary.LittleEndian.PutUint32(out[34:], uint32(row*height))
	for y := range height {
		for x := range width {
			copy(out[54+y*row+x*3:], []byte{0, 0, 255})
		}
	}
	return out
}

// tiff is a TIFF header whose first directory holds only Orientation = orientation.
func tiff(orientation int, little bool) []byte {
	order := binary.ByteOrder(binary.BigEndian)
	out := []byte{0x4d, 0x4d}
	if little {
		order, out = binary.LittleEndian, []byte{0x49, 0x49}
	}
	out = append(out, make([]byte, 24)...)
	order.PutUint16(out[2:], 42)
	order.PutUint32(out[4:], 8)
	order.PutUint16(out[8:], 1)
	order.PutUint16(out[10:], 0x0112)
	order.PutUint16(out[12:], 3)
	order.PutUint32(out[14:], 1)
	order.PutUint16(out[18:], uint16(orientation))
	return out
}

var exifHeader = []byte{'E', 'x', 'i', 'f', 0, 0}

// withOrientation is jpegBytes with an APP1 segment whose EXIF says orientation.
func withOrientation(jpegBytes []byte, orientation int) []byte {
	exif := append(append([]byte{}, exifHeader...), tiff(orientation, false)...)
	app1 := append([]byte{0xff, 0xe1, byte((len(exif) + 2) >> 8), byte((len(exif) + 2) & 0xff)}, exif...)
	out := append([]byte{}, jpegBytes[:2]...)
	out = append(out, app1...)
	return append(out, jpegBytes[2:]...)
}

// decodedSize is the size of the image in base64 data.
func decodedSize(t testing.TB, data string) (width, height int) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the image does not decode: %v", err)
	}
	return config.Width, config.Height
}
