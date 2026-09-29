package codingagent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/test/clipboard-image-bmp-conversion.test.ts:59, including both command and native backend rows. The native facade is the production mechanism owned by the native-platform lane, not a test-only implementation.
func TestClipboardBMPConversionUpstream(t *testing.T) {
	for _, platform := range []string{"linux", "win32"} {
		t.Run(platform+": converts command/native BMP to PNG", func(t *testing.T) {
			oldPlatform, oldRun, oldNative := clipboardGOOS, clipboardRun, getNativeClipboard
			t.Cleanup(func() { clipboardGOOS, clipboardRun, getNativeClipboard = oldPlatform, oldRun, oldNative })
			clipboardGOOS = platform
			if platform == "win32" {
				clipboardGOOS = "windows"
			}
			withEnv(t, map[string]string{"WAYLAND_DISPLAY": "wayland-0"})
			clipboardRun = func(_ context.Context, command string, args ...string) ([]byte, error) {
				if command == "wl-paste" && slices.Contains(args, "--list-types") {
					return []byte("image/bmp\n"), nil
				}
				if command == "wl-paste" && slices.Contains(args, "image/bmp") {
					return clipboardBMPFixture(), nil
				}
				t.Fatalf("unexpected clipboard command %s %q", command, args)
				return nil, nil
			}
			getNativeClipboard = func() *tui.NativeClipboard {
				return &tui.NativeClipboard{GetImage: func(context.Context) ([]byte, bool, error) { return clipboardBMPFixture(), true, nil }}
			}
			data, mime, err := ReadClipboardImageContext(t.Context())
			if err != nil || data == nil {
				t.Fatalf("clipboard image=%x MIME=%q error=%v", data, mime, err)
			}
			if mime != "image/png" {
				t.Errorf("MIME=%q, want image/png", mime)
			}
			if len(data) < 4 || !slices.Equal(data[:4], []byte{0x89, 0x50, 0x4e, 0x47}) {
				t.Errorf("PNG signature missing: %x", data)
			}
		})
	}
}
