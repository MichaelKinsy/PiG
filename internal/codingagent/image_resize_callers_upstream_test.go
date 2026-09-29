package codingagent

import (
	"encoding/base64"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

const upstreamTinyRedPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

func TestFileProcessorContinuesAfterRealImageConversionFailure(t *testing.T) {
	dir := t.TempDir()
	before, bad, after := filepath.Join(dir, "before.txt"), filepath.Join(dir, "bad.bmp"), filepath.Join(dir, "after.txt")
	for path, data := range map[string][]byte{
		before: []byte("before"),
		bad:    makeBMPImage(t, 1, 1, color.RGBA{A: 255})[:54], // Valid BMP header, missing pixel data.
		after:  []byte("after"),
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want := `<file name="` + before + `">` + "\nbefore\n</file>\n" +
		`<file name="` + bad + `">[Image omitted: could not be converted to a supported inline image format.]</file>` + "\n" +
		`<file name="` + after + `">` + "\nafter\n</file>\n"
	for _, autoResize := range []bool{true, false} {
		got, err := ProcessCLIFileArguments([]string{before, bad, after}, dir, ProcessFileOptions{AutoResizeImages: &autoResize})
		if err != nil {
			t.Fatalf("autoResize=%v: %v", autoResize, err)
		}
		if got.Text != want || len(got.Images) != 0 {
			t.Fatalf("autoResize=%v: got %#v, want %q without images", autoResize, got, want)
		}
	}
}

// Ports packages/coding-agent/test/image-resize-callers.test.ts:46.
func TestFileProcessorOmitsImageAttachmentsWhenAutoResizeCannotProduceSafeImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.png")
	data, err := base64.StdEncoding.DecodeString(upstreamTinyRedPNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	old := processFileImage
	t.Cleanup(func() { processFileImage = old })
	processFileImage = func(_ []byte, _ string, autoResize bool, _ *ai.ModelImageResizeOptions) ([]byte, string, string, error) {
		if !autoResize {
			t.Error("default file processing did not request the mocked resize")
		}
		return nil, "", "", errors.New("[Image omitted: could not be resized below the inline image size limit.]")
	}
	got, err := ProcessCLIFileArguments([]string{path}, dir)
	if err != nil {
		t.Fatalf("image processing failure aborted file arguments: %v", err)
	}
	if len(got.Images) != 0 || !strings.Contains(got.Text, "Image omitted") {
		t.Fatalf("processed=%#v", got)
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/image-resize-callers.test.ts:86
func TestFileProcessorCanDeferResizingFileAttachmentsUntilPromptDispatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.png")
	data, err := base64.StdEncoding.DecodeString(upstreamTinyRedPNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	old := processFileImage
	t.Cleanup(func() { processFileImage = old })
	resizes := 0
	processFileImage = func(data []byte, mime string, autoResize bool, options *ai.ModelImageResizeOptions) ([]byte, string, string, error) {
		if autoResize {
			resizes++
		}
		return old(data, mime, autoResize, options)
	}
	got, err := ProcessCLIFileArguments([]string{path}, dir, ProcessFileOptions{AutoResizeImages: new(false)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Images) != 1 || resizes != 0 {
		t.Fatalf("images=%d resize requests=%d", len(got.Images), resizes)
	}
}
