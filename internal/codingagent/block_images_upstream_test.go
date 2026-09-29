package codingagent

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

const blockImagesTinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

// Exact 1x1 red 24bpp BMP fixture from packages/coding-agent/test/block-images.test.ts:13.
func upstreamTinyBMP() []byte {
	b := make([]byte, 58)
	copy(b, "BM")
	binary.LittleEndian.PutUint32(b[2:], uint32(len(b)))
	binary.LittleEndian.PutUint32(b[10:], 54)
	binary.LittleEndian.PutUint32(b[14:], 40)
	binary.LittleEndian.PutUint32(b[18:], 1)
	binary.LittleEndian.PutUint32(b[22:], 1)
	binary.LittleEndian.PutUint16(b[26:], 1)
	binary.LittleEndian.PutUint16(b[28:], 24)
	binary.LittleEndian.PutUint32(b[34:], 4)
	b[56] = 255
	return b
}

// Ports packages/coding-agent/test/block-images.test.ts:32-58.
func TestBlockImagesSettingsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, json    string
		block, resize bool
	}{
		{"should default blockImages to false", `{}`, false, true},
		{"should return true when blockImages is set to true", `{"images":{"blockImages":true}}`, true, true},
		{"should handle blockImages alongside autoResize", `{"images":{"autoResize":true,"blockImages":true}}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var settings Settings
			if err := json.Unmarshal([]byte(tc.json), &settings); err != nil {
				t.Fatal(err)
			}
			if settings.GetBlockImages() != tc.block || settings.GetImageAutoResize() != tc.resize {
				t.Fatalf("block=%v resize=%v", settings.GetBlockImages(), settings.GetImageAutoResize())
			}
		})
	}
	t.Run("should persist blockImages setting via setBlockImages", func(t *testing.T) {
		cwd, dir := t.TempDir(), t.TempDir()
		manager := NewSettingsManager(cwd, dir)
		if manager.GetBlockImages() {
			t.Fatal("default blockImages=true")
		}
		for _, block := range []bool{true, false} {
			if err := manager.SetBlockImages(block); err != nil {
				t.Fatal(err)
			}
			if manager.GetBlockImages() != block {
				t.Fatalf("blockImages != %v", block)
			}
		}
	})
}

// Ports packages/coding-agent/test/block-images.test.ts:79-98,122-151. Filtering belongs to convertToLlm, not the file readers.
func TestBlockImagesReadersUpstream(t *testing.T) {
	png, err := base64.StdEncoding.DecodeString(blockImagesTinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("Read tool", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			data  []byte
			image bool
		}{
			{"test.png", png, true}, {"test.txt", []byte("Hello, world!"), false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, tc.name)
				if err := os.WriteFile(path, tc.data, 0o600); err != nil {
					t.Fatal(err)
				}
				params, err := json.Marshal(map[string]string{"path": path})
				if err != nil {
					t.Fatal(err)
				}
				tool := tools.ReadTool{CWD: dir}
				result, err := tool.Execute(t.Context(), "test-1", params, nil)
				if err != nil {
					t.Fatal(err)
				}
				if tc.image {
					if len(result.Images()) == 0 {
						t.Fatalf("missing image: %+v", result)
					}
				} else if result.Text() == "" || !strings.Contains(result.Text(), "Hello, world!") || len(result.Content) != 1 {
					t.Fatalf("text result: %+v", result)
				}
			})
		}
	})
	t.Run("processFileArguments", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			data   []byte
			images int
			note   string
		}{
			{"test.png", png, 1, ""},
			{"test.bmp", upstreamTinyBMP(), 1, "[Image converted from image/bmp to image/png.]"},
			{"test.txt", []byte("Hello, world!"), 0, "Hello, world!"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, tc.name)
				if err := os.WriteFile(path, tc.data, 0o600); err != nil {
					t.Fatal(err)
				}
				result, err := ProcessCLIFileArguments([]string{path}, dir)
				if err != nil {
					t.Fatal(err)
				}
				if len(result.Images) != tc.images {
					t.Fatalf("images=%d, want %d: %+v", len(result.Images), tc.images, result)
				}
				if tc.name == "test.bmp" && result.Images[0].MimeType != "image/png" {
					t.Fatalf("BMP MIME=%q", result.Images[0].MimeType)
				}
				if tc.note != "" && !strings.Contains(result.Text, tc.note) {
					t.Fatalf("text=%q lacks %q", result.Text, tc.note)
				}
			})
		}
	})
}
