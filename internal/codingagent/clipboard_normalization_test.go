package codingagent

import (
	"bytes"
	"context"
	"errors"
	"image"
	"os"
	"slices"
	"testing"

	"golang.org/x/image/bmp"
)

func BenchmarkClipboardBMPNormalization(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 1024, 768))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 17)
	}
	var encoded bytes.Buffer
	if err := bmp.Encode(&encoded, img); err != nil {
		b.Fatal(err)
	}
	oldRun, oldOS, oldEnv, oldRead := clipboardRun, clipboardGOOS, clipboardEnv, clipboardReadFile
	b.Cleanup(func() { clipboardRun, clipboardGOOS, clipboardEnv, clipboardReadFile = oldRun, oldOS, oldEnv, oldRead })
	clipboardGOOS = "linux"
	clipboardEnv = func(key string) string {
		if key == "WAYLAND_DISPLAY" {
			return "wayland-0"
		}
		return ""
	}
	clipboardReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	clipboardRun = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if slices.Contains(args, "--list-types") {
			return []byte("image/bmp\n"), nil
		}
		return encoded.Bytes(), nil
	}
	b.ReportAllocs()
	b.SetBytes(int64(encoded.Len()))
	for b.Loop() {
		data, mime, err := ReadClipboardImageContext(b.Context())
		if err != nil || mime != "image/png" || len(data) == 0 {
			b.Fatalf("MIME=%q bytes=%d err=%v", mime, len(data), err)
		}
	}
}

// Pi clipboard-image.ts:70-86,238-249 decodes unsupported formats without EXIF rotation, omits failed conversions, and passes declared supported formats through unchanged.
func TestClipboardImageNormalization(t *testing.T) {
	jpeg := decodeNodeBase64(jpegWithXmpBeforeOrientation(t))
	for _, tc := range []struct {
		name, mime        string
		data              []byte
		width, height     int
		converted, absent bool
	}{
		{name: "unsupported BMP", mime: "image/bmp", data: clipboardBMPFixture(), width: 1, height: 1, converted: true},
		{name: "corrupt unsupported image", mime: "image/bmp", data: []byte("not an image"), absent: true},
		{name: "unsupported JPEG does not rotate", mime: "image/x-example", data: jpeg, width: 2, height: 1, converted: true},
		{name: "supported JPEG stays byte-identical", mime: "image/jpeg", data: jpeg, width: 2, height: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withEnv(t, map[string]string{"WAYLAND_DISPLAY": "wayland-0"})
			oldRun, oldOS, oldRead := clipboardRun, clipboardGOOS, clipboardReadFile
			t.Cleanup(func() { clipboardRun, clipboardGOOS, clipboardReadFile = oldRun, oldOS, oldRead })
			clipboardGOOS = "linux"
			clipboardReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
			clipboardRun = func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "wl-paste" {
					t.Errorf("unexpected backend %s after conversion", name)
					return nil, errors.New("unexpected backend")
				}
				if slices.Contains(args, "--list-types") {
					return []byte(tc.mime + "\n"), nil
				}
				return tc.data, nil
			}
			data, mime, err := ReadClipboardImageContext(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if tc.absent {
				if data != nil || mime != "" {
					t.Fatalf("invalid conversion returned %q (%q)", data, mime)
				}
				return
			}
			wantMIME := tc.mime
			if tc.converted {
				wantMIME = "image/png"
			} else if !bytes.Equal(data, tc.data) {
				t.Fatal("supported image bytes changed")
			}
			if mime != wantMIME {
				t.Fatalf("MIME=%q want=%q", mime, wantMIME)
			}
			decoded, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if bounds := decoded.Bounds(); bounds.Dx() != tc.width || bounds.Dy() != tc.height {
				t.Fatalf("image bounds=%v want=%dx%d", bounds, tc.width, tc.height)
			}
		})
	}
}
