package codingagent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// clipboard-image.ts:96 and :180 decode the type listing with Buffer.toString("utf-8"), which replaces each invalid UTF-8 sequence with U+FFFD, and the
// image/* fallback then requests that decoded type. The Pi oracle passes listings through JSON, so it cannot send invalid bytes; this pins the decoding.
func TestClipboardTypeListingDecodesInvalidUTF8LikeNode(t *testing.T) {
	oldRun, oldEnv, oldPlatform, oldNative := clipboardRun, clipboardEnv, clipboardGOOS, getNativeClipboard
	t.Cleanup(func() {
		clipboardRun, clipboardEnv, clipboardGOOS, getNativeClipboard = oldRun, oldEnv, oldPlatform, oldNative
	})
	clipboardGOOS = "linux"
	getNativeClipboard = func() *tui.NativeClipboard { return nil }
	for _, wayland := range []bool{true, false} {
		env := map[string]string{"DISPLAY": ":0"}
		if wayland {
			env = map[string]string{"WAYLAND_DISPLAY": "wayland-0"}
		}
		clipboardEnv = func(key string) string { return env[key] }
		requested := ""
		clipboardRun = func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if slices.Contains(args, "--list-types") || slices.Contains(args, "TARGETS") {
				return []byte("text/plain\nimage/x-\xff\xe6\x80\n"), nil
			}
			for j, arg := range args {
				if (arg == "--type" || arg == "-t") && j+1 < len(args) {
					requested = args[j+1]
				}
			}
			return []byte("\x89PNG\r\n\x1a\n0000"), nil
		}
		if _, _, err := ReadClipboardImageContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		if want := "image/x-\ufffd\ufffd"; requested != want {
			t.Errorf("wayland=%v: requested %q, want %q", wayland, requested, want)
		}
	}
}
