package imageprocessing

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Fixtures are the unchanged base64-decoded constants from packages/coding-agent/test/image-processing.test.ts:10-24.
func upstreamImageFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "upstream-image-processing", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestResizeImageUpstream(t *testing.T) {
	// packages/coding-agent/test/image-processing.test.ts:99,116
	t.Run("caller input remains intact and image within limits is unchanged", func(t *testing.T) {
		input := upstreamImageFixture(t, "TINY_PNG.png")
		original := bytes.Clone(input)
		result, err := prepareImageForLLM(input, &ai.ModelImageResizeOptions{MaxWidth: 100, MaxHeight: 100, MaxBytes: 1024 * 1024})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(input, original) {
			t.Fatal("caller bytes changed")
		}
		if result.WasResized || !bytes.Equal(result.Data, original) || result.OriginalWidth != 2 || result.OriginalHeight != 2 || result.Width != 2 || result.Height != 2 {
			t.Fatalf("result=%+v", result)
		}
	})
	// packages/coding-agent/test/image-processing.test.ts:133
	t.Run("resizes image exceeding dimension limits", func(t *testing.T) {
		result, err := prepareImageForLLM(upstreamImageFixture(t, "MEDIUM_PNG_100x100.png"), &ai.ModelImageResizeOptions{MaxWidth: 50, MaxHeight: 50, MaxBytes: 1024 * 1024})
		if err != nil {
			t.Fatal(err)
		}
		if !result.WasResized || result.OriginalWidth != 100 || result.OriginalHeight != 100 || result.Width > 50 || result.Height > 50 {
			t.Fatalf("result=%+v", result)
		}
	})
	// packages/coding-agent/test/image-processing.test.ts:148
	t.Run("resizes image exceeding byte limit", func(t *testing.T) {
		input := upstreamImageFixture(t, "LARGE_PNG_200x200.png")
		// Pi measures the fixture's base64 length and floors its 90% limit.
		result, err := prepareImageForLLM(input, &ai.ModelImageResizeOptions{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: encodedSizeBase64(input) * 9 / 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Data) >= len(input) || encodedSizeBase64(result.Data) >= encodedSizeBase64(input) {
			t.Fatalf("size=%d, original=%d", len(result.Data), len(input))
		}
	})
	// packages/coding-agent/test/image-processing.test.ts:168. Go returns an error/zero result for Pi's null, never oversized image bytes.
	t.Run("returns no image when maxBytes cannot be satisfied", func(t *testing.T) {
		result, err := prepareImageForLLM(upstreamImageFixture(t, "LARGE_PNG_200x200.png"), &ai.ModelImageResizeOptions{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 1})
		if err == nil || len(result.Data) != 0 {
			t.Fatalf("result=%+v err=%v, want no image", result, err)
		}
	})
	// packages/coding-agent/test/image-processing.test.ts:178
	t.Run("handles JPEG input", func(t *testing.T) {
		result, err := prepareImageForLLM(upstreamImageFixture(t, "TINY_JPEG.jpg"), &ai.ModelImageResizeOptions{MaxWidth: 100, MaxHeight: 100, MaxBytes: 1024 * 1024})
		if err != nil {
			t.Fatal(err)
		}
		if result.WasResized || result.OriginalWidth != 2 || result.OriginalHeight != 2 {
			t.Fatalf("result=%+v", result)
		}
	})
}

// Ports packages/coding-agent/test/image-processing.test.ts:193,206.
func TestFormatDimensionNoteUpstream(t *testing.T) {
	if note := formatDimensionNote(preparedImageResult{MIME: "image/png", OriginalWidth: 100, OriginalHeight: 100, Width: 100, Height: 100}); note != "" {
		t.Fatalf("non-resized note=%q", note)
	}
	note := formatDimensionNote(preparedImageResult{MIME: "image/png", OriginalWidth: 2000, OriginalHeight: 1000, Width: 1000, Height: 500, WasResized: true})
	for _, want := range []string{"original 2000x1000", "displayed at 1000x500", "2.00"} {
		if !strings.Contains(note, want) {
			t.Errorf("note=%q, lacks %q", note, want)
		}
	}
}
