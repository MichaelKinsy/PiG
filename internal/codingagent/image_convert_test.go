package codingagent

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"image"
	"image/color"
	"image/gif"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// Fixtures from upstream test/image-processing.test.ts.
const (
	upstreamTinyPNG  = "iVBORw0KGgoAAAANSUhEUgAAAAIAAAACAQMAAABIeJ9nAAAAIGNIUk0AAHomAACAhAAA+gAAAIDoAAB1MAAA6mAAADqYAAAXcJy6UTwAAAAGUExURf8AAP///0EdNBEAAAABYktHRAH/Ai3eAAAAB3RJTUUH6gEOADM5Ddoh/wAAAAxJREFUCNdjYGBgAAAABAABJzQnCgAAACV0RVh0ZGF0ZTpjcmVhdGUAMjAyNi0wMS0xNFQwMDo1MTo1NyswMDowMOnKzHgAAAAldEVYdGRhdGU6bW9kaWZ5ADIwMjYtMDEtMTRUMDA6NTE6NTcrMDA6MDCYl3TEAAAAKHRFWHRkYXRlOnRpbWVzdGFtcAAyMDI2LTAxLTE0VDAwOjUxOjU3KzAwOjAwz4JVGwAAAABJRU5ErkJggg=="
	upstreamTinyJPEG = "/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAMCAgMCAgMDAwMEAwMEBQgFBQQEBQoHBwYIDAoMDAsKCwsNDhIQDQ4RDgsLEBYQERMUFRUVDA8XGBYUGBIUFRT/2wBDAQMEBAUEBQkFBQkUDQsNFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBT/wAARCAACAAIDAREAAhEBAxEB/8QAFAABAAAAAAAAAAAAAAAAAAAACf/EABQQAQAAAAAAAAAAAAAAAAAAAAD/xAAVAQEBAAAAAAAAAAAAAAAAAAAGCf/EABQRAQAAAAAAAAAAAAAAAAAAAAD/2gAMAwEAAhEDEQA/AD3VTB3/2Q=="
	tinyWebP         = "UklGRjwAAABXRUJQVlA4IDAAAADQAQCdASoBAAEAAgA0JaACdLoB+AADsAD+8MQL/yC5YXXI1/8gP+QH/ID/+PIAAAA="
)

func pngDimensions(t *testing.T, data string) (uint32, uint32) {
	t.Helper()
	png, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatalf("result is not base64: %v", err)
	}
	if len(png) < 24 || !bytes.Equal(png[:4], []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("result is not PNG: % x", png[:min(len(png), 8)])
	}
	return binary.BigEndian.Uint32(png[16:20]), binary.BigEndian.Uint32(png[20:24])
}

// Upstream "should return original data for PNG input".
func TestConvertToPngReturnsOriginalDataForPNG(t *testing.T) {
	got := ConvertToPng(upstreamTinyPNG, "image/png")
	if got == nil || got.Data != upstreamTinyPNG || got.MimeType != "image/png" {
		t.Fatalf("ConvertToPng(png) = %+v, want original data", got)
	}
	// The passthrough keys on the declared MIME alone, even for non-image data.
	if got := ConvertToPng("not-an-image", "image/png"); got == nil || got.Data != "not-an-image" {
		t.Fatalf("ConvertToPng(declared png) = %+v, want passthrough", got)
	}
}

// Upstream "should convert JPEG to PNG".
func TestConvertToPngConvertsJPEG(t *testing.T) {
	got := ConvertToPng(upstreamTinyJPEG, "image/jpeg")
	if got == nil || got.MimeType != "image/png" {
		t.Fatalf("ConvertToPng(jpeg) = %+v", got)
	}
	if w, h := pngDimensions(t, got.Data); w != 2 || h != 2 {
		t.Fatalf("converted size = %dx%d, want 2x2", w, h)
	}
}

// Upstream "should apply EXIF orientation after an XMP APP1 segment".
func TestConvertToPngAppliesExifOrientationAfterXMPSegment(t *testing.T) {
	got := ConvertToPng(jpegWithXmpBeforeOrientation(t), "image/jpeg")
	if got == nil {
		t.Fatal("ConvertToPng returned nil")
	}
	if w, h := pngDimensions(t, got.Data); w != 1 || h != 2 {
		t.Fatalf("oriented size = %dx%d, want 1x2", w, h)
	}
}

func TestConvertToPngConvertsEveryDecodableFormat(t *testing.T) {
	var gifBuf bytes.Buffer
	pal := image.NewPaletted(image.Rect(0, 0, 3, 2), color.Palette{color.Black, color.White})
	if err := gif.Encode(&gifBuf, pal, nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, mime string
		data       []byte
		w, h       uint32
	}{
		{"gif", "image/gif", gifBuf.Bytes(), 3, 2},
		{"bmp", "image/bmp", makeBMPImage(t, 4, 5, color.RGBA{1, 2, 3, 255}), 4, 5},
		{"jpeg", "image/jpeg", makeJPEGImage(t, 6, 3, color.RGBA{9, 8, 7, 255}), 6, 3},
		// The declared MIME only gates the PNG passthrough; decoding sniffs
		// the bytes, as Photon does.
		{"png declared jpeg", "image/jpeg", makePNGImage(t, 2, 7, color.RGBA{1, 1, 1, 255}), 2, 7},
		{"png declared PNG uppercase", "image/PNG", makePNGImage(t, 5, 1, color.RGBA{1, 1, 1, 255}), 5, 1},
	}
	webp, err := base64.StdEncoding.DecodeString(tinyWebP)
	if err != nil {
		t.Fatal(err)
	}
	cases = append(cases, struct {
		name, mime string
		data       []byte
		w, h       uint32
	}{"webp", "image/webp", webp, 1, 1})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ConvertToPng(base64.StdEncoding.EncodeToString(tc.data), tc.mime)
			if got == nil || got.MimeType != "image/png" {
				t.Fatalf("ConvertToPng = %+v", got)
			}
			if w, h := pngDimensions(t, got.Data); w != tc.w || h != tc.h {
				t.Fatalf("size = %dx%d, want %dx%d", w, h, tc.w, tc.h)
			}
		})
	}
}

// Upstream returns null when Photon cannot decode the bytes.
func TestConvertToPngReturnsNilWhenConversionFails(t *testing.T) {
	for _, data := range []string{"", base64.StdEncoding.EncodeToString([]byte("not an image")), "%%%"} {
		if got := ConvertToPng(data, "image/jpeg"); got != nil {
			t.Fatalf("ConvertToPng(%q) = %+v, want nil", data, got)
		}
	}
	if got := ConvertImageBytesToPng(nil); got != nil {
		t.Fatalf("ConvertImageBytesToPng(nil) = %d bytes, want nil", len(got))
	}
}

// Expected bytes are Node 24 Buffer.from(input, "base64") results.
func TestDecodeNodeBase64MatchesNodeBuffer(t *testing.T) {
	cases := map[string]string{
		"QUJD":         "414243",
		"QUJ":          "4142",
		"QU":           "41",
		"Q":            "",
		"QUJD\nRA==":   "41424344",
		"QUJD RA":      "41424344",
		"QU=JD":        "41",
		"QUJD====RA==": "414243",
		"QUJ-_w":       "41427eff",
		"QUJ+/w":       "41427eff",
		"@@QUJD!!":     "414243",
		"QUJDRA=x":     "41424344",
	}
	for in, want := range cases {
		if got := hex.EncodeToString(decodeNodeBase64(in)); got != want {
			t.Errorf("decodeNodeBase64(%q) = %s, want %s", in, got, want)
		}
	}
}

// Whitespace-wrapped base64 still converts, as Node's decoder skips it.
func TestConvertToPngAcceptsWrappedBase64(t *testing.T) {
	wrapped := upstreamTinyJPEG[:40] + "\n" + upstreamTinyJPEG[40:]
	if got := ConvertToPng(wrapped, "image/jpeg"); got == nil {
		t.Fatal("wrapped base64 JPEG did not convert")
	}
}

// The Kitty tool-result path: a non-PNG tool image converts off the loop and
// lands on the component through the main loop, where it renders as PNG.
func TestMaybeConvertImagesForKittyAppliesConversionOnMainLoop(t *testing.T) {
	prev := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(prev) })
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty})

	m := newRunOnMainProbe(t)
	// Scheduled renders dispatch here instead of running on the timer
	// goroutine, so the test observes the render request on its own loop.
	renders := make(chan func(), 4)
	m.tuiInst.SetRenderDispatcher(func(render func()) { renders <- render })
	m.runCtx = t.Context()

	comp := tui.NewToolExecutionComponent("read", "")
	comp.ImageBlocks = []tui.ImageBlock{{Data: upstreamTinyJPEG, MIMEType: "image/jpeg"}}
	comp.SetResult("", false, 0)
	m.maybeConvertImagesForKitty(comp)

	select {
	case fn := <-m.uiTaskCh:
		fn()
	case <-time.After(30 * time.Second):
		t.Fatal("conversion result never reached the main loop")
	}
	if pending := comp.PendingKittyImageConversions(); len(pending) != 0 {
		t.Fatalf("image still pending after conversion: %+v", pending)
	}
	select {
	case render := <-renders:
		render()
	case <-time.After(30 * time.Second):
		t.Fatal("applied conversion did not request a render")
	}
	want := ConvertToPng(upstreamTinyJPEG, "image/jpeg")
	rendered := ""
	for _, line := range comp.Render(100) {
		rendered += line
	}
	if !bytes.Contains([]byte(rendered), []byte(want.Data[:32])) || bytes.Contains([]byte(rendered), []byte(upstreamTinyJPEG[:32])) {
		t.Fatal("rendered image is not the converted PNG")
	}
}

// Off Kitty no conversion is started.
func TestMaybeConvertImagesForKittyNoopOffKitty(t *testing.T) {
	prev := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(prev) })
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolITerm2})

	m := newRunOnMainProbe(t)
	comp := tui.NewToolExecutionComponent("read", "")
	comp.ImageBlocks = []tui.ImageBlock{{Data: upstreamTinyJPEG, MIMEType: "image/jpeg"}}
	m.maybeConvertImagesForKitty(comp)
	select {
	case <-m.uiTaskCh:
		t.Fatal("a conversion was posted off Kitty")
	case <-time.After(200 * time.Millisecond):
	}
}
