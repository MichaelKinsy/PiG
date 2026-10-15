package tui

import (
	"encoding/base64"
	"testing"
)

// terminal-image.ts getPngDimensions / getJpegDimensions / getGifDimensions / getWebpDimensions / getImageDimensions: each reads its header with the
// byte order and offsets of its format and answers undefined for a wrong signature, a short buffer or an unknown chunk.
// mutation-checked: swapped width and height, a shifted offset, a dropped signature check and a dropped dispatch branch each fail it.
func TestImageHeaderParsersReadEachFormatsDimensions(t *testing.T) {
	b64 := func(data []byte) string { return base64.StdEncoding.EncodeToString(data) }
	want := func(t *testing.T, name string, got *ImageDimensions, width, height int) {
		t.Helper()
		if got == nil || got.WidthPx != width || got.HeightPx != height {
			t.Errorf("%s = %+v, want %dx%d", name, got, width, height)
		}
	}
	none := func(t *testing.T, name string, got *ImageDimensions) {
		t.Helper()
		if got != nil {
			t.Errorf("%s = %+v, want nil", name, got)
		}
	}

	png := make([]byte, 24)
	copy(png, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	copy(png[16:], []byte{0x00, 0x01, 0x02, 0x03, 0x00, 0x04, 0x05, 0x06})
	gotPNG := GetPNGDimensions(b64(png))
	want(t, "PNG", gotPNG, 0x010203, 0x040506)
	bad := append([]byte(nil), png...)
	bad[1] = 'X'
	none(t, "PNG with a bad signature", GetPNGDimensions(b64(bad)))
	none(t, "a short PNG", GetPNGDimensions(b64(png[:23])))
	none(t, "PNG that is not base64", GetPNGDimensions("!!!"))

	// An APP0 segment (length 4, two payload bytes) precedes the SOF marker; height comes before width.
	jpeg := func(marker byte) []byte {
		return []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x04, 0x00, 0x00, 0xff, marker, 0x00, 0x11, 0x08, 0x01, 0x02, 0x03, 0x04, 0x03, 0x00}
	}
	gotJPEG := GetJPEGDimensions(b64(jpeg(0xc0)))
	want(t, "JPEG SOF0", gotJPEG, 0x0304, 0x0102)
	want(t, "JPEG SOF1", GetJPEGDimensions(b64(jpeg(0xc1))), 0x0304, 0x0102)
	want(t, "JPEG SOF2", GetJPEGDimensions(b64(jpeg(0xc2))), 0x0304, 0x0102)
	none(t, "JPEG whose only frame marker is SOF3", GetJPEGDimensions(b64(jpeg(0xc3))))
	none(t, "JPEG without the SOI marker", GetJPEGDimensions(b64([]byte{0xff, 0xd9, 0xff, 0xc0, 0, 0x11, 8, 1, 2, 3, 4, 3, 0})))
	none(t, "JPEG with a segment shorter than its length field", GetJPEGDimensions(b64([]byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x01, 0, 0, 0, 0, 0, 0, 0, 0})))
	// A segment whose length is below 2 ends the scan even when a frame marker follows it.
	none(t, "JPEG with a zero-length segment before a frame marker", GetJPEGDimensions(b64([]byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x00, 0xff, 0xc0, 0x00, 0x11, 0x08, 0x01, 0x02, 0x03, 0x04, 0x03, 0x00})))

	gif := []byte{'G', 'I', 'F', '8', '9', 'a', 0x02, 0x01, 0x04, 0x03}
	gotGIF := GetGIFDimensions(b64(gif))
	want(t, "GIF89a", gotGIF, 0x0102, 0x0304)
	gif87 := append([]byte(nil), gif...)
	gif87[4] = '7'
	want(t, "GIF87a", GetGIFDimensions(b64(gif87)), 0x0102, 0x0304)
	gif[3] = '9'
	none(t, "GIF with a bad signature", GetGIFDimensions(b64(gif)))
	none(t, "a short GIF", GetGIFDimensions(b64(gif87[:9])))

	webp := func(chunk string) []byte {
		buf := make([]byte, 30)
		copy(buf, "RIFF")
		copy(buf[8:], "WEBP")
		copy(buf[12:], chunk)
		return buf
	}
	vp8 := webp("VP8 ")
	copy(vp8[26:], []byte{0x34, 0xc2, 0x12, 0xc1}) // 14-bit fields: the top two bits are scale and are dropped
	gotWebP := GetWebPDimensions(b64(vp8))
	want(t, "WebP VP8", gotWebP, 0x0234, 0x0112)
	vp8l := webp("VP8L")
	// width-1 = 0x0123, height-1 = 0x0045: bits = 0x0123 | 0x0045<<14
	bits := 0x0123 | 0x0045<<14
	copy(vp8l[21:], []byte{byte(bits), byte(bits >> 8), byte(bits >> 16), byte(bits >> 24)})
	want(t, "WebP VP8L", GetWebPDimensions(b64(vp8l)), 0x0124, 0x0046)
	vp8x := webp("VP8X")
	copy(vp8x[24:], []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06})
	want(t, "WebP VP8X", GetWebPDimensions(b64(vp8x)), 0x030201+1, 0x060504+1)
	none(t, "WebP with an unknown chunk", GetWebPDimensions(b64(webp("ABCD"))))
	notWebP := webp("VP8X")
	copy(notWebP[8:], "WEBX")
	none(t, "RIFF without the WEBP tag", GetWebPDimensions(b64(notWebP)))
	none(t, "a short WebP", GetWebPDimensions(b64(vp8[:29])))

	want(t, "GetImageDimensions png", GetImageDimensions(b64(png), "image/png"), 0x010203, 0x040506)
	want(t, "GetImageDimensions jpeg", GetImageDimensions(b64(jpeg(0xc0)), "image/jpeg"), 0x0304, 0x0102)
	want(t, "GetImageDimensions gif", GetImageDimensions(b64(gif87), "image/gif"), 0x0102, 0x0304)
	want(t, "GetImageDimensions webp", GetImageDimensions(b64(vp8), "image/webp"), 0x0234, 0x0112)
	none(t, "GetImageDimensions of an unknown type", GetImageDimensions(b64(png), "image/bmp"))
}

// terminal-image.ts encodeITerm2: size is Buffer.byteLength(base64Data, "base64"), the decoded length: padding characters do not count.
// mutation-checked: dropping either padding adjustment, or the whole-groups term, fails it.
func TestEncodeITerm2SizeIsTheDecodedByteLength(t *testing.T) {
	for _, tc := range []struct {
		data string
		size int
	}{{"AAAA", 3}, {"AAA=", 2}, {"AA==", 1}, {"AAAAAAAA", 6}, {"AAAAAAA=", 5}, {"AAAAAA==", 4}, {"AAAAAA", 4}, {"", 0}} {
		want := "\x1b]1337;File=inline=1;size=" + string(rune('0'+tc.size)) + ":" + tc.data + "\x07"
		if got := EncodeITerm2(tc.data, ITerm2Options{}); got != want {
			t.Errorf("EncodeITerm2(%q) = %q, want %q", tc.data, got, want)
		}
	}
}
