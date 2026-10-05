package tui

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Ports .upstream/v1.0.1/packages/tui/test/terminal-image.test.ts ("Image transcoding", #10292): Kitty only accepts PNG
// (f=100), so non-PNG images are transcoded.
func TestUpstreamImageTranscoding(t *testing.T) {
	jpeg := base64.StdEncoding.EncodeToString([]byte("jpeg"))
	// Minimal PNG header (signature + IHDR) for a 40x10 image. Enough for GetPNGDimensions.
	header, _ := hex.DecodeString("89504e470d0a1a0a0000000d49484452000000280000000a")
	png := base64.StdEncoding.EncodeToString(header)
	var calls []string
	identity := ImageTheme{FallbackColor: func(value string) string { return value }}
	newImage := func(data, mimeType string, dimensions *ImageDimensions) *Image {
		image := NewImage(data, mimeType, ImageOptions{}, dimensions)
		image.Theme = identity
		return image
	}
	render := func(data, mimeType string) []string {
		return newImage(data, mimeType, &ImageDimensions{WidthPx: 20, HeightPx: 20}).Render(20)
	}
	transcode := func(data, _ string) (string, bool) {
		calls = append(calls, data)
		if data == jpeg {
			return png, true
		}
		return "", false
	}
	setup := func(t *testing.T) {
		calls = nil
		prevCaps, prevCells := GetCapabilities(), GetCellDimensions()
		SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
		SetCellDimensions(CellDimensions{WidthPx: 10, HeightPx: 10})
		t.Cleanup(func() {
			SetImageTranscoder(nil)
			SetCapabilities(prevCaps)
			SetCellDimensions(prevCells)
		})
	}
	fallback := regexp.MustCompile(`^\[Image: \[image/jpeg\]`)

	t.Run("sends converted PNG data sized from the PNG", func(t *testing.T) {
		setup(t)
		SetImageTranscoder(transcode)
		lines := render(jpeg, "image/jpeg")
		if !strings.Contains(lines[0], "f=100") || !strings.Contains(lines[0], ";"+png+"\x1b\\") {
			t.Fatalf("lines[0] = %q", lines[0])
		}
		// 40x10 PNG at 18 columns: 5 rows, not the 18 rows of the 20x20 source dimensions.
		if len(lines) != 5 {
			t.Fatalf("rows = %d", len(lines))
		}
	})
	t.Run("renders a text fallback until a working transcoder is registered", func(t *testing.T) {
		setup(t)
		image := newImage(jpeg, "image/jpeg", nil)
		if got := image.Render(80)[0]; !fallback.MatchString(got) {
			t.Fatalf("without a transcoder = %q", got)
		}
		SetImageTranscoder(func(string, string) (string, bool) { return "", false })
		image.Invalidate()
		if got := image.Render(80)[0]; !fallback.MatchString(got) {
			t.Fatalf("with a failing transcoder = %q", got)
		}
		SetImageTranscoder(transcode)
		image.Invalidate()
		if got := image.Render(80)[0]; !strings.Contains(got, "\x1b_G") {
			t.Fatalf("with a working transcoder = %q", got)
		}
	})
	t.Run("converts each image once", func(t *testing.T) {
		setup(t)
		SetImageTranscoder(transcode)
		image := newImage(jpeg, "image/jpeg", nil)
		image.Render(80)
		render(jpeg, "image/jpeg") // New instance hits the shared cache.
		for i := range 40 {
			render(fmt.Sprintf("other-%d", i), "image/jpeg") // Evicts the shared entry.
		}
		image.Invalidate()
		image.Render(40) // Instance keeps its own PNG.
		if got := len(slices.DeleteFunc(slices.Clone(calls), func(data string) bool { return data != jpeg })); got != 1 {
			t.Fatalf("jpeg converted %d times", got)
		}
	})
	t.Run("does not convert PNG data or iTerm2 output", func(t *testing.T) {
		setup(t)
		SetImageTranscoder(transcode)
		if got := render(png, "image/png")[0]; !strings.Contains(got, ";"+png+"\x1b\\") {
			t.Fatalf("png = %q", got)
		}
		SetCapabilities(TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true})
		if lines := render(jpeg, "image/jpeg"); !strings.HasSuffix(lines[len(lines)-1], ":"+jpeg+"\x07") {
			t.Fatalf("iterm2 = %q", lines)
		}
		if len(calls) != 0 {
			t.Fatalf("calls = %q", calls)
		}
	})
}
