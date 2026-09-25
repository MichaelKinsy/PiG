package imageprocessing

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"image/color"
	"testing"
	"time"
)

// Upstream test/image-processing.test.ts TINY_JPEG_2X1: a 2x1 JPEG.
const upstreamTinyJPEG2x1 = "/9j/4AAQSkZJRgABAgAAAQABAAD/wAARCAABAAIDAREAAhEBAxEB/9sAQwADAgIDAgIDAwMDBAMDBAUIBQUEBAUKBwcGCAwKDAwLCgsLDQ4SEA0OEQ4LCxAWEBETFBUVFQwPFxgWFBgSFBUU/9sAQwEDBAQFBAUJBQUJFA0LDRQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQU/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgEDAwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKBkaEII0KxwRVS0fAkM2JyggkKFhcYGRolJicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3+Pn6/8QAHwEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoL/8QAtREAAgECBAQDBAcFBAQAAQJ3AAECAxEEBSExBhJBUQdhcRMiMoEIFEKRobHBCSMzUvAVYnLRChYkNOEl8RcYGRomJygpKjU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6goOEhYaHiImKkpOUlZaXmJmaoqOkpaanqKmqsrO0tba3uLm6wsPExcbHyMnK0tPU1dbX2Nna4uPk5ebn6Onq8vP09fb3+Pn6/9oADAMBAAIRAxEAPwD4H8Q/8h/Uv+vmX/0M1/o1wJ/ySWU/9g1D/wBNRMOM/wDkp8z/AOv9b/05I//Z"

func app1Segment(payload []byte) []byte {
	seg := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

// Mirrors upstream jpegWithXmpBeforeOrientation: an XMP APP1 segment precedes
// the Exif APP1 segment carrying little-endian orientation 6.
func jpegWithXmpBeforeOrientation(t *testing.T) string {
	t.Helper()
	jpeg, err := base64.StdEncoding.DecodeString(upstreamTinyJPEG2x1)
	if err != nil {
		t.Fatal(err)
	}
	tiff, err := hex.DecodeString("49492a0008000000010012010300010000000600000000000000")
	if err != nil {
		t.Fatal(err)
	}
	xmp := app1Segment([]byte("http://ns.adobe.com/xap/1.0/\x00<x:xmpmeta xmlns:x=\"adobe:ns:meta/\"/>"))
	orientation6 := app1Segment(append([]byte("Exif\x00\x00"), tiff...))
	var out bytes.Buffer
	out.Write(jpeg[:2])
	out.Write(xmp)
	out.Write(orientation6)
	out.Write(jpeg[2:])
	return base64.StdEncoding.EncodeToString(out.Bytes())
}

// Upstream getExifOrientation keeps walking past an APP1 segment that is not
// "Exif\0\0" (here XMP), so the orientation-6 tag that follows still turns the
// stored 2x1 image upright as 1x2.
func TestDecodeAutoOrientedAppliesExifAfterXMPSegment(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(jpegWithXmpBeforeOrientation(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := getExifOrientation(data); got != orientationRotate270 {
		t.Fatalf("getExifOrientation = %d, want 6", got)
	}
	img, err := decodeAutoOriented(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if w, h := img.Bounds().Dx(), img.Bounds().Dy(); w != 1 || h != 2 {
		t.Fatalf("oriented size = %dx%d, want 1x2", w, h)
	}
}

func webpWithExifChunk(payload []byte) []byte {
	var out bytes.Buffer
	out.WriteString("RIFF\x00\x00\x00\x00WEBP")
	// An unrelated odd-sized chunk exercises RIFF even padding.
	out.WriteString("ICCP")
	_ = binary.Write(&out, binary.LittleEndian, uint32(3))
	out.Write([]byte{1, 2, 3, 0})
	out.WriteString("EXIF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(len(payload)))
	out.Write(payload)
	return out.Bytes()
}

func TestGetExifOrientationMirrorsUpstreamLocators(t *testing.T) {
	leTIFF6, _ := hex.DecodeString("49492a0008000000010012010300010000000600000000000000")
	beTIFF3 := []byte{0x4d, 0x4d, 0, 0x2a, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, 3, 0, 0, 0, 0}
	beTIFFSecondEntry8 := []byte{0x4d, 0x4d, 0, 0x2a, 0, 0, 0, 8, 0, 2,
		0x01, 0x00, 0, 3, 0, 0, 0, 1, 0, 9, 0, 0,
		0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, 8, 0, 0,
		0, 0, 0, 0}
	leNegativeIFD := []byte{0x49, 0x49, 0x2a, 0, 0, 0, 0, 0x80, 0, 0}
	truncatedEntry := []byte{0x4d, 0x4d, 0, 0x2a, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3}

	cases := []struct {
		name string
		data []byte
		want exifOrientation
	}{
		{"webp exif with prefix", webpWithExifChunk(append([]byte("Exif\x00\x00"), leTIFF6...)), orientationRotate270},
		{"webp exif without prefix", webpWithExifChunk(beTIFF3), orientationRotate180},
		{"webp exif chunk overruns data", webpWithExifChunk(beTIFF3)[:40], orientationNormal},
		{"webp without exif", webpWithExifChunk(nil)[:24], orientationNormal},
		{"tiff second entry", webpWithExifChunk(beTIFFSecondEntry8), orientationRotate90},
		{"little-endian negative ifd offset", webpWithExifChunk(leNegativeIFD), orientationNormal},
		{"truncated ifd entry", webpWithExifChunk(truncatedEntry), orientationNormal},
		{"jpeg fill bytes before exif", append([]byte{0xff, 0xd8, 0xff}, app1Segment(append([]byte("Exif\x00\x00"), leTIFF6...))...), orientationRotate270},
		{"jpeg non-marker byte", []byte{0xff, 0xd8, 0x00, 0xe1}, orientationNormal},
		{"jpeg app1 header truncated", []byte{0xff, 0xd8, 0xff, 0xe1, 0x00}, orientationNormal},
		{"jpeg app1 too short for exif header", []byte{0xff, 0xd8, 0xff, 0xe1, 0x00, 0x08, 'E', 'x'}, orientationNormal},
		{"png", makePNGImage(t, 1, 1, color.RGBA{A: 255}), orientationNormal},
	}
	for _, tc := range cases {
		if got := getExifOrientation(tc.data); got != tc.want {
			t.Errorf("%s: getExifOrientation = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// A RIFF chunk whose signed size walks backwards would loop forever upstream;
// the port stops with the not-found result.
func TestGetExifOrientationStopsOnBackwardWebPChunk(t *testing.T) {
	data := []byte("RIFF\x00\x00\x00\x00WEBPJUNK\xf8\xff\xff\xff")
	done := make(chan exifOrientation, 1)
	go func() { done <- getExifOrientation(data) }()
	select {
	case got := <-done:
		if got != orientationNormal {
			t.Fatalf("getExifOrientation = %d, want normal", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("getExifOrientation did not terminate on a backward chunk size")
	}
}
