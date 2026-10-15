//go:build windows || darwin

package nativeplatform

import (
	"os"
	"testing"
)

// nativeClipboardWritable reports whether a test may overwrite the system clipboard: a CI runner (its own session) or an
// explicit opt-in on a desktop. Upstream gates the Windows write test on PI_TEST_NATIVE_CLIPBOARD=1 for the same reason.
func nativeClipboardWritable() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true" || os.Getenv("PI_TEST_NATIVE_CLIPBOARD") == "1"
}

// The native platform helper is the clipboard API on macOS and Windows (native-platform.ts): text written through it reads back
// exactly, including non-ASCII text, an empty string and a second write, and a text clipboard has no image.
func TestNativeClipboardTextRoundTrip(t *testing.T) {
	if !nativeClipboardWritable() {
		t.Skip("set PI_TEST_NATIVE_CLIPBOARD=1 to overwrite the system clipboard")
	}
	clipboard := GetNativeClipboard()
	if clipboard == nil || clipboard.GetText == nil || clipboard.SetText == nil || clipboard.GetImage == nil {
		t.Fatalf("clipboard = %+v", clipboard)
	}
	for _, text := range []string{"clipboard café 日本語 😀", "line one\nline two", "", "second write"} {
		if err := clipboard.SetText(t.Context(), text); err != nil {
			t.Fatalf("SetText(%q): %v", text, err)
		}
		value, ok, err := clipboard.GetText(t.Context())
		if err != nil || !ok {
			t.Fatalf("GetText after %q = %v, %v, %v", text, value, ok, err)
		}
		// An empty write leaves no text on some pasteboards; a present value must still be the empty string.
		if text == "" && value == nil {
			continue
		}
		if value == nil || *value != text {
			t.Fatalf("GetText after %q = %v", text, value)
		}
		if image, _, err := clipboard.GetImage(t.Context()); err != nil || image != nil {
			t.Fatalf("GetImage after text = %v, %v", image, err)
		}
	}
}
