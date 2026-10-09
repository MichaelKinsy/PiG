package codingagent

// pi: packages/coding-agent/src/utils/image-convert.ts

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"image"
	"image/color"
	"image/gif"
	"strings"
	"testing"

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

// tool-execution-component.test.ts "converts non-PNG tool images once the transcoder loads" (#10292, #8577): a tool card registers
// the PNG transcoder itself, through the loader the package installs, so a non-PNG tool image renders as a Kitty image in any host. A
// replaced partial image does not resurface, and invalidating reuses the converted Image, so the Kitty image id stays the same.
func TestToolCardRegistersTheTranscoderAndConvertsNonPNGImages(t *testing.T) {
	prev := tui.GetCapabilities()
	t.Cleanup(func() {
		tui.SetCapabilities(prev)
		tui.SetImageTranscoder(nil)
	})
	tui.SetImageTranscoder(nil)
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty, TrueColor: true, Hyperlinks: true})

	comp := newToolCardForTest("tool", "")
	comp.UpdateResult(tui.ToolResultUpdate{Content: []tui.ToolResultContent{{Type: "image", Data: "cGFydGlhbA==", MimeType: "image/jpeg"}}}, true)
	comp.UpdateResult(tui.ToolResultUpdate{Content: []tui.ToolResultContent{{Type: "image", Data: upstreamTinyJPEG, MimeType: "image/jpeg"}}})

	rendered := strings.Join(comp.Render(120), "\n")
	if !strings.Contains(rendered, ";iVBORw0KGgo") {
		t.Fatalf("the jpeg was not converted to a PNG image:\n%q", rendered)
	}
	if strings.Contains(rendered, "cGFydGlhbA==") {
		t.Fatalf("the replaced partial image resurfaced:\n%q", rendered)
	}
	comp.Invalidate()
	if again := strings.Join(comp.Render(120), "\n"); again != rendered {
		t.Fatalf("invalidation changed the image:\n%q\n%q", again, rendered)
	}
}

// Off Kitty the card registers no transcoder: iTerm2 shows any format unconverted.
func TestToolCardRegistersNoTranscoderOffKitty(t *testing.T) {
	prev := tui.GetCapabilities()
	t.Cleanup(func() {
		tui.SetCapabilities(prev)
		tui.SetImageTranscoder(nil)
	})
	tui.SetImageTranscoder(nil)
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolITerm2})
	comp := newToolCardForTest("read", "")
	comp.ImageBlocks = []tui.ImageBlock{{Data: upstreamTinyJPEG, MIMEType: "image/jpeg"}}
	comp.SetResult("", false, 0)
	comp.Render(100)
	if tui.ImageTranscoderRegistered() {
		t.Fatal("a transcoder was registered off Kitty")
	}
}

// .upstream/v1.0.1/packages/coding-agent/test/image-processing.test.ts:86 (#10292): pi-tui uses this transcoder to
// show non-PNG images on Kitty-protocol terminals.
func TestPngTranscoderConvertsSynchronouslyToOrientedPNGData(t *testing.T) {
	png, ok := PngTranscoder(jpegWithXmpBeforeOrientation(t), "image/jpeg")
	if !ok {
		t.Fatal("transcoder failed")
	}
	if w, h := pngDimensions(t, png); w != 1 || h != 2 {
		t.Fatalf("oriented size = %dx%d, want 1x2", w, h)
	}
	if _, ok := PngTranscoder(base64.StdEncoding.EncodeToString([]byte("not an image")), "image/jpeg"); ok {
		t.Fatal("transcoded non-image data")
	}
}

// interactive-mode.ts applyRuntimeSettings (1.0.1): on Kitty the interactive mode registers the transcoder, so a
// non-PNG tui.Image renders as an image instead of its text fallback.
func TestEnsurePngTranscoderRegistersOnKittyOnly(t *testing.T) {
	prev := tui.GetCapabilities()
	t.Cleanup(func() {
		tui.SetCapabilities(prev)
		tui.SetImageTranscoder(nil)
	})
	jpeg := jpegWithXmpBeforeOrientation(t)
	tui.SetImageTranscoder(nil)
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolITerm2})
	ensurePngTranscoder(nil)
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty})
	if got := tui.NewImage(jpeg, "image/jpeg", tui.DefaultImageTheme(), tui.ImageOptions{}, nil).Render(80)[0]; strings.Contains(got, "\x1b_G") || !strings.Contains(got, "[Image: [image/jpeg]") {
		t.Fatalf("transcoder registered off Kitty: %q", got)
	}
	ensurePngTranscoder(nil)
	if got := tui.NewImage(jpeg, "image/jpeg", tui.DefaultImageTheme(), tui.ImageOptions{}, nil).Render(80)[0]; !strings.Contains(got, "\x1b_G") {
		t.Fatalf("kitty image not transcoded: %q", got)
	}
}

// image-convert.ts ensurePngTranscoder: onRegistered runs after a new registration, not when a transcoder was already registered, and
// not off Kitty.
func TestEnsurePngTranscoderRunsOnRegisteredOnlyForANewRegistration(t *testing.T) {
	prev := tui.GetCapabilities()
	t.Cleanup(func() {
		tui.SetCapabilities(prev)
		tui.SetImageTranscoder(nil)
	})
	calls := 0
	tui.SetImageTranscoder(nil)
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolITerm2})
	ensurePngTranscoder(func() { calls++ })
	if calls != 0 {
		t.Fatalf("onRegistered ran off Kitty (%d)", calls)
	}
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty})
	ensurePngTranscoder(func() { calls++ })
	ensurePngTranscoder(func() { calls++ })
	if calls != 1 {
		t.Fatalf("onRegistered ran %d times, want once for the one registration", calls)
	}
}
