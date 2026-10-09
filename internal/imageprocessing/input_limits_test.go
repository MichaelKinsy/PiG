package imageprocessing

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The model profile replaces individual image-resize-core.ts defaults, preserving
// the other dimensions and the source aspect ratio.
func TestResizeModelInputLimits(t *testing.T) {
	input := makePNGImage(t, 320, 160, color.RGBA{10, 20, 30, 255})
	for _, tc := range []struct {
		name          string
		options       *ai.ModelImageResizeOptions
		width, height int
	}{
		{"default", nil, 320, 160},
		{"width", &ai.ModelImageResizeOptions{MaxWidth: 100}, 100, 50},
		{"height", &ai.ModelImageResizeOptions{MaxHeight: 40}, 80, 40},
		{"both", &ai.ModelImageResizeOptions{MaxWidth: 100, MaxHeight: 40}, 80, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := resizeDetected(input, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			if result.Width != tc.width || result.Height != tc.height {
				t.Fatalf("size=%dx%d, want %dx%d", result.Width, result.Height, tc.width, tc.height)
			}
			if tc.options == nil && !bytes.Equal(result.Bytes(), input) {
				t.Fatal("within-limit input was re-encoded")
			}
		})
	}
}

func TestResizeModelInputLimitImpossibleEncodedSize(t *testing.T) {
	input := makePNGImage(t, 8, 8, color.RGBA{10, 20, 30, 255})
	if _, err := resizeDetected(input, &ai.ModelImageResizeOptions{MaxBytes: 1}); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("error=%v, want ErrImageTooLarge", err)
	}
}

func TestProcessImageModelProfileAndDisabledResize(t *testing.T) {
	input := makeBMPImage(t, 320, 160, color.RGBA{10, 20, 30, 255})
	options := &ai.ModelImageResizeOptions{MaxWidth: 100, MaxHeight: 40}
	for _, autoResize := range []bool{true, false} {
		data, mime, hint, err := ProcessImage(input, "image/bmp", autoResize, options)
		if err != nil {
			t.Fatal(err)
		}
		width, height := decodeBoundsPNG(t, data)
		wantWidth, wantHeight := 320, 160
		if autoResize {
			wantWidth, wantHeight = 80, 40
		}
		if width != wantWidth || height != wantHeight || mime != "image/png" {
			t.Fatalf("resize %v: %dx%d %s", autoResize, width, height, mime)
		}
		if !strings.Contains(hint, "[Image converted from image/bmp to image/png.]") {
			t.Fatalf("missing conversion hint: %q", hint)
		}
		if strings.Contains(hint, "displayed at") != autoResize {
			t.Fatalf("resize %v hint: %q", autoResize, hint)
		}
	}
	if options.MaxWidth != 100 || options.MaxHeight != 40 {
		t.Fatal("profile was mutated")
	}
}

func TestProcessImageOmissionHints(t *testing.T) {
	for _, tc := range []struct{ mime, want string }{
		{"image/unknown", "[Image omitted: could not be converted to a supported inline image format.]"},
		{"image/png", "[Image omitted: could not be resized below the inline image size limit.]"},
	} {
		_, _, _, err := ProcessImage([]byte("invalid"), tc.mime, true, nil)
		if err == nil || err.Error() != tc.want {
			t.Fatalf("%s error=%v, want %s", tc.mime, err, tc.want)
		}
	}
}

func TestResizeModelProfileRoundsAspectRatio(t *testing.T) {
	result, err := resizeDetected(makePNGImage(t, 30, 17, color.RGBA{10, 20, 30, 255}), &ai.ModelImageResizeOptions{MaxWidth: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 10 || result.Height != 6 {
		t.Fatalf("size=%dx%d, want 10x6", result.Width, result.Height)
	}
}

func TestResizeModelProfileJPEGQualityAndSizeOnlyHint(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 96, 96))
	random := rand.New(rand.NewPCG(1, 2))
	for y := range 96 {
		for x := range 96 {
			source.SetRGBA(x, y, color.RGBA{uint8(random.Uint32()), uint8(random.Uint32()), uint8(random.Uint32()), 255})
		}
	}
	input, err := encodePNG(source)
	if err != nil {
		t.Fatal(err)
	}
	want, err := encodeJPEG(source, 20)
	if err != nil {
		t.Fatal(err)
	}
	limit := encodedSizeBase64(want) + 1
	if encodedSizeBase64(input) < limit {
		t.Fatal("fixture does not exercise JPEG re-encoding")
	}
	result, err := resizeDetected(input, &ai.ModelImageResizeOptions{MaxBytes: limit, JPEGQuality: 20})
	if err != nil {
		t.Fatal(err)
	}
	if result.MimeType != "image/jpeg" || !bytes.Equal(result.Bytes(), want) {
		t.Fatalf("custom quality not selected: %s, %d bytes", result.MimeType, len(result.Bytes()))
	}
	if !result.WasResized || !strings.Contains(formatDimensionNote(result), "displayed at 96x96") {
		t.Fatal("size-only re-encode must carry upstream dimension hint")
	}
}

func BenchmarkProcessImageModelProfile(b *testing.B) {
	input, err := encodePNG(image.NewRGBA(image.Rect(0, 0, 320, 160)))
	if err != nil {
		b.Fatal(err)
	}
	options := &ai.ModelImageResizeOptions{MaxWidth: 100, MaxHeight: 40}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, _, _, err := ProcessImage(input, "image/png", true, options); err != nil {
			b.Fatal(err)
		}
	}
}

func TestProcessImagePreservesNormalizedDeclaredMIMEWithoutResize(t *testing.T) {
	input := makePNGImage(t, 8, 8, color.RGBA{10, 20, 30, 255})
	data, mime, hint, err := ProcessImage(input, " IMAGE/JPG; charset=binary ", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/jpeg" || hint != "" || !bytes.Equal(data, input) {
		t.Fatalf("unmodified image: mime=%s hint=%q bytes=%d", mime, hint, len(data))
	}
}

// resizeDetected is ResizeImage declared with the MIME type its bytes carry.
func resizeDetected(in []byte, options *ai.ModelImageResizeOptions) (ResizedImage, error) {
	return ResizeImage(in, mimeForFormat(detectFormat(in)), options)
}

// upstream: packages/coding-agent/src/utils/image-resize-core.ts:80 returns the caller's mimeType (image/png when none)
// for an image already within every limit, and the encoder's type once it has to resize.
func TestResizeImageReportsTheDeclaredMimeType(t *testing.T) {
	input := makePNGImage(t, 8, 8, color.RGBA{10, 20, 30, 255})
	for _, tc := range []struct{ declared, want string }{
		{"image/webp", "image/webp"},
		{"image/jpeg", "image/jpeg"},
		{"", "image/png"},
	} {
		result, err := ResizeImage(input, tc.declared, nil)
		if err != nil || result.WasResized || result.MimeType != tc.want {
			t.Fatalf("ResizeImage(declared %q) = %q resized=%v err=%v, want %q unresized", tc.declared, result.MimeType, result.WasResized, err, tc.want)
		}
	}
	resized, err := ResizeImage(makePNGImage(t, 30, 17, color.RGBA{10, 20, 30, 255}), "image/webp", &ai.ModelImageResizeOptions{MaxWidth: 10})
	if err != nil || !resized.WasResized || resized.MimeType == "image/webp" {
		t.Fatalf("resized image reports %q resized=%v err=%v; it must carry the encoder's type", resized.MimeType, resized.WasResized, err)
	}
}
