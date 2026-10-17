package nativeplatform

import "testing"

// native-platform.ts loads a helper only for x64 and arm64 (loadNativePlatformHelper); getNativePlatformHelper answers only on
// darwin and win32; getNativeClipboard answers on linux (when a display is set) and, through the platform helper, on darwin and
// win32. Everything else is undefined.
func TestNativePlatformSelectionMatrix(t *testing.T) {
	loaded := &NativeClipboard{}
	load := func() *NativeClipboard { return loaded }
	display := func(key string) string {
		if key == "DISPLAY" {
			return ":0"
		}
		return ""
	}
	noDisplay := func(string) string { return "" }
	for _, tc := range []struct {
		name, goos, arch string
		getenv           func(string) string
		clipboard        bool
		platformHelper   bool
	}{
		{"linux amd64 with a display", "linux", "amd64", display, true, false},
		{"linux arm64 with a display", "linux", "arm64", display, true, false},
		{"linux without a display", "linux", "amd64", noDisplay, false, false},
		{"darwin arm64", "darwin", "arm64", noDisplay, true, true},
		{"darwin amd64", "darwin", "amd64", noDisplay, true, true},
		{"windows amd64", "windows", "amd64", noDisplay, true, true},
		{"windows arm64", "windows", "arm64", noDisplay, true, true},
		{"windows 386", "windows", "386", noDisplay, false, false},
		{"linux riscv64", "linux", "riscv64", display, false, false},
		{"linux arm", "linux", "arm", display, false, false},
		{"freebsd amd64", "freebsd", "amd64", display, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nativeClipboardFor(tc.goos, tc.arch, tc.getenv, load); (got != nil) != tc.clipboard {
				t.Errorf("clipboard = %v, want present %v", got, tc.clipboard)
			}
			if got := nativePlatformHelperFor(tc.goos, tc.arch, tc.getenv, load); (got != nil) != tc.platformHelper {
				t.Errorf("platform helper = %v, want present %v", got, tc.platformHelper)
			}
		})
	}
}
