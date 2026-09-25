package tui

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"sync"
	"testing"
)

func makePNGBase64(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var b strings.Builder
	enc := base64.NewEncoder(base64.StdEncoding, &b)
	if err := png.Encode(enc, img); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func makeJPEGBase64(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var sb strings.Builder
	enc := base64.NewEncoder(base64.StdEncoding, &sb)
	if err := jpeg.Encode(enc, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

func makeGIFBase64(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, w, h), []color.Color{color.Black, color.White})
	var sb strings.Builder
	enc := base64.NewEncoder(base64.StdEncoding, &sb)
	if err := gif.Encode(enc, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

func TestDetectCapabilities(t *testing.T) {
	// Each case clears the whole environment, so restore all of it, not only
	// the variables the cases set; later tests need HOME, PATH, and TMPDIR.
	saved := os.Environ()
	t.Cleanup(func() {
		os.Clearenv()
		for _, entry := range saved {
			name, value, _ := strings.Cut(entry, "=")
			_ = os.Setenv(name, value)
		}
		ResetCapabilitiesCache()
	})
	detectCapabilitiesAs(t, "linux")

	cases := []struct {
		name          string
		setEnv        func()
		wantImg       ImageProtocol
		wantTrueColor bool
		wantHL        bool
	}{
		{"tmux disables images and hyperlinks without forwarding", func() { _ = os.Setenv("TMUX", "1"); _ = os.Setenv("COLORTERM", "truecolor") }, "", true, false},
		{"kitty", func() { _ = os.Setenv("KITTY_WINDOW_ID", "1") }, ImageProtocolKitty, true, true},
		{"ghostty", func() { _ = os.Setenv("TERM_PROGRAM", "ghostty") }, ImageProtocolKitty, true, true},
		{"herdr inherited ghostty without graphics", func() { _ = os.Setenv("HERDR_ENV", "1"); _ = os.Setenv("TERM_PROGRAM", "ghostty") }, "", false, false},
		{"herdr positively advertises graphics", func() {
			_ = os.Setenv("HERDR_ENV", "1")
			_ = os.Setenv("HERDR_KITTY_GRAPHICS", "1")
			_ = os.Setenv("TERM_PROGRAM", "ghostty")
		}, ImageProtocolKitty, true, true},
		{"wezterm", func() { _ = os.Setenv("WEZTERM_PANE", "1") }, ImageProtocolKitty, true, true},
		{"iterm2", func() { _ = os.Setenv("ITERM_SESSION_ID", "1") }, ImageProtocolITerm2, true, true},
		{"apple terminal without a truecolor hint", func() { _ = os.Setenv("TERM_PROGRAM", "Apple_Terminal") }, "", false, false},
		{"windows terminal enables truecolor and hyperlinks", func() { _ = os.Setenv("WT_SESSION", "1") }, "", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			os.Clearenv()
			ResetCapabilitiesCache()
			tc.setEnv()
			got := DetectCapabilities(func() bool { return false })
			if got.Images != tc.wantImg || got.TrueColor != tc.wantTrueColor || got.Hyperlinks != tc.wantHL {
				t.Fatalf("DetectCapabilities() = %+v want images=%q trueColor=%v hyperlinks=%v", got, tc.wantImg, tc.wantTrueColor, tc.wantHL)
			}
		})
	}
}

func TestGetCapabilitiesConcurrentInitialization(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "kitty")
	ResetCapabilitiesCache()
	t.Cleanup(ResetCapabilitiesCache)

	const workers = 32
	start := make(chan struct{})
	results := make(chan TerminalCapabilities, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			<-start
			results <- GetCapabilities()
		})
	}
	close(start)
	wg.Wait()
	close(results)

	for caps := range results {
		if caps.Images != ImageProtocolKitty || !caps.TrueColor || !caps.Hyperlinks {
			t.Fatalf("GetCapabilities() = %+v, want kitty capabilities", caps)
		}
	}
}

func TestAC51HerdrImageCapabilityOwnsRowReservation(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("HERDR_KITTY_GRAPHICS", "")
	ResetCapabilitiesCache()
	t.Cleanup(ResetCapabilitiesCache)

	image := NewImage("eA==", "image/png", ImageOptions{Filename: "probe.png"}, &ImageDimensions{WidthPx: 800, HeightPx: 600})
	lines := image.Render(80)
	if len(lines) != 1 {
		t.Fatalf("Herdr fallback reserved %d rows: %#v", len(lines), lines)
	}
	if strings.Contains(lines[0], "\x1b_G") || !strings.Contains(lines[0], "probe.png") {
		t.Fatalf("Herdr fallback = %q", lines[0])
	}
}

func TestHerdrWithGraphicsSignalRendersKittyImage(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_KITTY_GRAPHICS", "1")
	t.Setenv("TERM_PROGRAM", "ghostty")
	ResetCapabilitiesCache()
	t.Cleanup(ResetCapabilitiesCache)

	image := NewImage("eA==", "image/png", ImageOptions{Filename: "probe.png"}, &ImageDimensions{WidthPx: 800, HeightPx: 600})
	lines := image.Render(80)
	if len(lines) < 2 || !strings.Contains(lines[0], "\x1b_G") {
		t.Fatalf("positive Herdr graphics render = %#v", lines)
	}
}

func TestImageDimensionsParsers(t *testing.T) {
	pngData := makePNGBase64(t, 10, 5)
	if dims := GetImageDimensions(pngData, "image/png"); dims == nil || dims.WidthPx != 10 || dims.HeightPx != 5 {
		t.Fatalf("png dims = %+v", dims)
	}
	jpegData := makeJPEGBase64(t, 12, 7)
	if dims := GetImageDimensions(jpegData, "image/jpeg"); dims == nil || dims.WidthPx != 12 || dims.HeightPx != 7 {
		t.Fatalf("jpeg dims = %+v", dims)
	}
	gifData := makeGIFBase64(t, 9, 4)
	if dims := GetImageDimensions(gifData, "image/gif"); dims == nil || dims.WidthPx != 9 || dims.HeightPx != 4 {
		t.Fatalf("gif dims = %+v", dims)
	}
}

func TestEncodeKittyAndITerm2(t *testing.T) {
	seq := EncodeKitty(strings.Repeat("a", 5000), 10, 5, 42)
	if !strings.Contains(seq, "a=T") || !strings.Contains(seq, "i=42") || !strings.Contains(seq, "m=1") || !strings.Contains(seq, "m=0") {
		t.Fatalf("kitty sequence malformed: %q", seq[:80])
	}
	iterm := EncodeITerm2("Zm9v", 10, "auto", "x.png", true)
	if !strings.HasPrefix(iterm, "\x1b]1337;File=") || !strings.Contains(iterm, "inline=1") || !strings.Contains(iterm, ":Zm9v\x07") {
		t.Fatalf("iterm sequence malformed: %q", iterm)
	}
}

func TestEncodeKitty_ExactByteParity(t *testing.T) {
	t.Run("single chunk exact bytes", func(t *testing.T) {
		got := EncodeKitty("YWJj", 10, 5, 42)
		want := "\x1b_Ga=T,f=100,q=2,c=10,r=5,i=42;YWJj\x1b\\"
		if got != want {
			t.Fatalf("single chunk mismatch\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("multi chunk sha256", func(t *testing.T) {
		got := EncodeKitty(strings.Repeat("a", 4097), 10, 5, 42)
		sum := sha256.Sum256([]byte(got))
		if gotHash := hex.EncodeToString(sum[:]); gotHash != "86243d6c8fb63f0479b1418400c7e76e1a1b8bbe927c7d5683080934c358e7e4" {
			t.Fatalf("multi chunk sha256 = %s", gotHash)
		}
	})
}

func TestEncodeITerm2_ExactByteParity(t *testing.T) {
	got := EncodeITerm2("Zm9v", 10, "auto", "x.png", true)
	want := "\x1b]1337;File=inline=1;width=10;height=auto;name=eC5wbmc=:Zm9v\x07"
	if got != want {
		t.Fatalf("iterm2 mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestDeleteKittySequences_ExactByteParity(t *testing.T) {
	if got := DeleteKittyImage(42); got != "\x1b_Ga=d,d=I,i=42,q=2\x1b\\" {
		t.Fatalf("DeleteKittyImage mismatch: %q", got)
	}
	if got := DeleteAllKittyImages(); got != "\x1b_Ga=d,d=A,q=2\x1b\\" {
		t.Fatalf("DeleteAllKittyImages mismatch: %q", got)
	}
}

func TestIsImageLine(t *testing.T) {
	if !IsImageLine("\x1b_Ga=T;abc\x1b\\") {
		t.Fatal("kitty line not detected")
	}
	if !IsImageLine("\x1b[2A\x1b]1337;File=inline=1:Zm9v\x07") {
		t.Fatal("iterm2 line not detected")
	}
	if IsImageLine("plain text") {
		t.Fatal("false positive image line")
	}
}

func TestAllocateImageID_NonZero(t *testing.T) {
	for range 128 {
		if got := AllocateImageID(); got <= 0 {
			t.Fatalf("AllocateImageID() = %d, want > 0", got)
		}
	}
}

func TestCalculateImageCellSize(t *testing.T) {
	dims := ImageDimensions{WidthPx: 200, HeightPx: 100}
	cell := CellDimensions{WidthPx: 10, HeightPx: 20}

	got := CalculateImageCellSize(dims, 10, 0, cell)
	if got.Columns != 10 || got.Rows != 3 {
		t.Fatalf("CalculateImageCellSize() = %+v want {Columns:10 Rows:3}", got)
	}

	clamped := CalculateImageCellSize(dims, 10, 2, cell)
	if clamped.Columns != 8 || clamped.Rows != 2 {
		t.Fatalf("CalculateImageCellSize() with maxHeight = %+v want {Columns:8 Rows:2}", clamped)
	}
}

func TestRenderImage_UsesMaxHeightAndOmitsITermName(t *testing.T) {
	pngData := makePNGBase64(t, 200, 100)
	dims := GetImageDimensions(pngData, "image/png")
	if dims == nil {
		t.Fatal("nil dims")
		return
	}

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	kitty := RenderImage(pngData, *dims, ImageRenderOptions{MaxWidthCells: 10, MaxHeightCells: 1, ImageID: 7})
	if kitty == nil {
		t.Fatal("kitty render nil")
		return
	}
	if kitty.Rows != 1 {
		t.Fatalf("Rows = %d, want clamped 1", kitty.Rows)
	}
	if !strings.Contains(kitty.Sequence, "c=4") || !strings.Contains(kitty.Sequence, "r=1") {
		t.Fatalf("kitty sequence should use clamped size: %q", kitty.Sequence)
	}

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true})
	iterm := RenderImage(pngData, *dims, ImageRenderOptions{MaxWidthCells: 10, Name: "ignored.png", PreserveAspectRatio: true})
	if iterm == nil {
		t.Fatal("iterm render nil")
		return
	}
	if strings.Contains(iterm.Sequence, "name=") {
		t.Fatalf("iTerm2 sequence should not include filename metadata: %q", iterm.Sequence)
	}
	ResetCapabilitiesCache()
}

func TestImageComponent_RenderPlacementByProtocol(t *testing.T) {
	pngData := makePNGBase64(t, 20, 10)
	dims := GetImageDimensions(pngData, "image/png")
	if dims == nil {
		t.Fatal("nil dims")
	}

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	kitty := NewImage(pngData, "image/png", ImageOptions{MaxWidthCells: 10}, dims)
	kittyLines := kitty.Render(20)
	if len(kittyLines) == 0 {
		t.Fatal("kitty render returned no lines")
	}
	if !strings.Contains(kittyLines[0], "\x1b_G") {
		t.Fatalf("first kitty line missing image sequence: %q", kittyLines[0])
	}
	for _, line := range kittyLines[1:] {
		if line != "" {
			t.Fatalf("kitty continuation lines should be empty: %q", line)
		}
	}

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true})
	iterm := NewImage(pngData, "image/png", ImageOptions{MaxWidthCells: 10}, dims)
	itermLines := iterm.Render(20)
	if len(itermLines) == 0 {
		t.Fatal("iterm render returned no lines")
	}
	if !strings.Contains(itermLines[len(itermLines)-1], "\x1b]1337;File=") {
		t.Fatalf("last iterm line missing image sequence: %q", itermLines[len(itermLines)-1])
	}
	if len(itermLines) > 1 && !strings.HasPrefix(itermLines[len(itermLines)-1], "\x1b[") {
		t.Fatalf("iterm last line should move cursor up before drawing: %q", itermLines[len(itermLines)-1])
	}
	ResetCapabilitiesCache()
}

func TestRenderImageAndImageComponent(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	defer ResetCapabilitiesCache()
	pngData := makePNGBase64(t, 20, 10)
	dims := GetImageDimensions(pngData, "image/png")
	if dims == nil {
		t.Fatal("nil dims")
		return
	}
	got := RenderImage(pngData, *dims, ImageRenderOptions{MaxWidthCells: 10, ImageID: 7})
	if got == nil || got.Rows < 1 || !strings.Contains(got.Sequence, "\x1b_G") {
		t.Fatalf("RenderImage() = %+v", got)
	}
	img := NewImage(pngData, "image/png", ImageOptions{MaxWidthCells: 10}, dims)
	lines := img.Render(20)
	if len(lines) == 0 {
		t.Fatal("image render returned no lines")
	}
	if !strings.Contains(lines[0], "\x1b_G") {
		t.Fatalf("first image line missing kitty sequence: %q", lines[0])
	}
}

func TestImageFallbackAndHyperlink(t *testing.T) {
	got := ImageFallback("image/png", &ImageDimensions{WidthPx: 10, HeightPx: 5}, "a.png")
	if got != "[Image: a.png [image/png] 10x5]" {
		t.Fatalf("ImageFallback() = %q", got)
	}
	link := Hyperlink("x", "https://example.com")
	if !strings.Contains(link, "https://example.com") || !strings.Contains(link, "x") {
		t.Fatalf("Hyperlink() malformed: %q", link)
	}
}
