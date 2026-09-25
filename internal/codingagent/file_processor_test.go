package codingagent

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/bmp"
)

func TestProcessCLIFileArguments_WrapsTextFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ProcessCLIFileArguments([]string{"note.txt"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := `<file name="` + path + `">` + "\nhello\nworld\n</file>\n"
	if got.Text != want {
		t.Fatalf("ProcessCLIFileArguments().Text = %q, want %q", got.Text, want)
	}
	if len(got.Images) != 0 {
		t.Fatalf("ProcessCLIFileArguments().Images = %d, want 0", len(got.Images))
	}
}

func TestProcessCLIFileArguments_GIFPrefixedTextRemainsText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	content := "GIF is the first word in this text file.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ProcessCLIFileArguments([]string{"note.txt"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := `<file name="` + path + `">` + "\n" + content + "</file>\n"
	if got.Text != want {
		t.Fatalf("ProcessCLIFileArguments().Text = %q, want %q", got.Text, want)
	}
	if len(got.Images) != 0 {
		t.Fatalf("ProcessCLIFileArguments().Images = %d, want 0", len(got.Images))
	}
}

func TestProcessCLIFileArguments_SkipsEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ProcessCLIFileArguments([]string{"empty.txt"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "" || len(got.Images) != 0 {
		t.Fatalf("ProcessCLIFileArguments() = %+v, want empty", got)
	}
}

func TestProcessCLIFileArguments_MissingFileErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := ProcessCLIFileArguments([]string{"missing.txt"}, dir)
	if err == nil || !strings.Contains(err.Error(), "File not found:") {
		t.Fatalf("err = %v, want missing-file error", err)
	}
}

func TestProcessCLIFileArguments_AttachesImageFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.png")
	if err := os.WriteFile(path, makePNGImage(t, 64, 64, color.RGBA{255, 0, 0, 255}), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ProcessCLIFileArguments([]string{"sample.png"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := `<file name="` + path + `"></file>` + "\n"
	if got.Text != want {
		t.Fatalf("ProcessCLIFileArguments().Text = %q, want %q", got.Text, want)
	}
	if len(got.Images) != 1 {
		t.Fatalf("ProcessCLIFileArguments().Images = %d, want 1", len(got.Images))
	}
	if got.Images[0].MimeType != "image/png" || got.Images[0].Data == "" {
		t.Fatalf("image attachment = %+v, want non-empty image/png", got.Images[0])
	}
}

func TestProcessCLIFileArguments_AttachesBMPAsPNG(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.bmp")
	if err := os.WriteFile(path, makeBMPImage(t, 4, 4, color.RGBA{0, 255, 0, 255}), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ProcessCLIFileArguments([]string{"sample.bmp"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	// Upstream file-processor.ts emits processImage's hints inside the <file>
	// note; a BMP converted to PNG reports the format conversion.
	want := `<file name="` + path + `">[Image converted from image/bmp to image/png.]</file>` + "\n"
	if got.Text != want {
		t.Fatalf("ProcessCLIFileArguments().Text = %q, want %q", got.Text, want)
	}
	if len(got.Images) != 1 {
		t.Fatalf("ProcessCLIFileArguments().Images = %d, want 1", len(got.Images))
	}
	if got.Images[0].MimeType != "image/png" || got.Images[0].Data == "" {
		t.Fatalf("BMP attachment = %+v, want converted image/png", got.Images[0])
	}
}

func TestProcessCLIFileArguments_ResizedImageAddsDimensionNote(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.png")
	if err := os.WriteFile(path, makePNGImage(t, 4000, 2000, color.RGBA{0, 128, 255, 255}), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ProcessCLIFileArguments([]string{"large.png"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := `<file name="` + path + `">[Image: original 4000x2000, displayed at 2000x1000. Multiply coordinates by 2.00 to map to original image.]</file>` + "\n"
	if got.Text != want {
		t.Fatalf("ProcessCLIFileArguments().Text = %q, want %q", got.Text, want)
	}
	if len(got.Images) != 1 {
		t.Fatalf("ProcessCLIFileArguments().Images = %d, want 1", len(got.Images))
	}
}

func makeBMPImage(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := bmp.Encode(&buf, img); err != nil {
		t.Fatalf("encode bmp: %v", err)
	}
	return buf.Bytes()
}

func makePNGImage(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func makeJPEGImage(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}
