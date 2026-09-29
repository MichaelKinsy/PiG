//go:build windows

package nativeplatform

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// .upstream/v0.87.1/packages/tui/test/native-platform.test.ts:37 (writes Windows clipboard text through the native helper without command fallbacks). Opt in on a Windows test desktop: this replaces the system clipboard contents.
func TestUpstreamNativeWindowsClipboardWrite(t *testing.T) {
	if testenv.RequireLiveEnv(t, "PI_TEST_NATIVE_CLIPBOARD") != "1" {
		t.Skip("set PI_TEST_NATIVE_CLIPBOARD=1 to overwrite the system clipboard")
	}
	clipboard := GetNativeClipboard()
	if clipboard == nil || clipboard.SetText == nil {
		t.Fatalf("clipboard = %+v", clipboard)
	}
	for _, text := range []string{"clipboard café 日本語", "", "second write"} {
		if err := clipboard.SetText(t.Context(), text); err != nil {
			t.Fatal(err)
		}
		value, ok, err := clipboard.GetText(t.Context())
		if err != nil || !ok || value == nil || *value != text {
			t.Fatalf("getText after %q = %v, %v, %v", text, value, ok, err)
		}
		if image, ok, err := clipboard.GetImage(t.Context()); err != nil || !ok || image != nil {
			t.Fatalf("getImage = %v, %v, %v", image, ok, err)
		}
	}
}
