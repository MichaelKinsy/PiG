package nativeplatform

import (
	"context"
	"debug/elf"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// .upstream/v0.87.1/packages/tui/test/native-platform.test.ts:60 (uses the native platform helper directly as the clipboard API). Pi runs this on macOS and Windows for arm64 and x64; the platform helper table is exercised for each of those targets, and the running host is asserted through the production getters.
func TestUpstreamNativePlatformHelperIsClipboardAPI(t *testing.T) {
	for _, platform := range []string{"darwin", "windows"} {
		for _, arch := range []string{"arm64", "amd64"} {
			t.Run(platform+"/"+arch, func(t *testing.T) {
				helper := &NativeClipboard{
					GetText:                    func(context.Context) (*string, bool, error) { return nil, true, nil },
					GetImage:                   func(context.Context) ([]byte, bool, error) { return nil, true, nil },
					SetText:                    func(context.Context, string) error { return nil },
					EnableVirtualTerminalInput: func() bool { return true },
				}
				clipboard := nativeClipboardFor(platform, arch, func(string) string { return "" }, func() *NativeClipboard { return helper })
				if clipboard == nil || clipboard.GetText == nil || clipboard.GetImage == nil || clipboard.SetText == nil {
					t.Fatalf("clipboard = %+v lacks getText/getImage/setText", clipboard)
				}
				if clipboard != helper {
					t.Fatal("clipboard is not the platform helper object")
				}
			})
		}
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		if GetNativePlatformHelper() != nil {
			t.Fatal("platform helper exists outside macOS and Windows")
		}
		return
	}
	clipboard := GetNativeClipboard()
	if clipboard == nil || clipboard.GetText == nil || clipboard.GetImage == nil || clipboard.SetText == nil {
		t.Fatalf("clipboard = %+v", clipboard)
	}
	if clipboard != GetNativePlatformHelper() || clipboard != GetNativeClipboard() {
		t.Fatal("clipboard is not the cached platform helper")
	}
}

// .upstream/v0.87.1/packages/tui/test/native-platform.test.ts:83 (Linux loads X11 lazily and rechecks DISPLAY), replayed through the production getter's DISPLAY check with a fake helper loader. Wayland-only sessions have no DISPLAY and paste uses wl-paste, so the helper is absent.
func TestUpstreamNativePlatformLinuxRechecksDisplay(t *testing.T) {
	display := ""
	getenv := func(name string) string {
		switch name {
		case "DISPLAY":
			return display
		case "WAYLAND_DISPLAY":
			return "wayland-0"
		}
		return ""
	}
	available := false
	getTextCalls, loads := 0, 0
	helper := &NativeClipboard{
		GetText: func(context.Context) (*string, bool, error) {
			getTextCalls++
			if !available {
				return nil, false, nil
			}
			value := "X11"
			return &value, true, nil
		},
		GetImage: func(context.Context) ([]byte, bool, error) { return nil, true, nil },
	}
	load := func() *NativeClipboard { loads++; return helper }
	get := func() *NativeClipboard { return nativeClipboardFor("linux", "amd64", getenv, load) }

	if get() != nil || loads != 0 {
		t.Fatal("helper loaded without DISPLAY")
	}
	display = ":0"
	clipboard := get()
	if clipboard != helper {
		t.Fatalf("clipboard = %p, want helper %p", clipboard, helper)
	}
	if getTextCalls != 0 {
		t.Fatalf("getText called %d times while loading", getTextCalls)
	}
	if value, ok, err := clipboard.GetText(t.Context()); value != nil || ok || err != nil {
		t.Fatalf("unavailable getText = %v, %v, %v", value, ok, err)
	}
	available = true
	if value, ok, err := clipboard.GetText(t.Context()); err != nil || !ok || value == nil || *value != "X11" {
		t.Fatalf("available getText = %v, %v, %v", value, ok, err)
	}
	if image, ok, err := clipboard.GetImage(t.Context()); image != nil || !ok || err != nil {
		t.Fatalf("getImage = %v, %v, %v", image, ok, err)
	}
	display = ""
	if get() != nil {
		t.Fatal("helper returned after DISPLAY was removed")
	}
	display = ":1"
	if get() != clipboard {
		t.Fatal("helper identity changed after DISPLAY returned")
	}
}

// packages/tui/test/native-platform.test.ts:8. PiG ships this same Node helper to extensions; its PT_LOAD segments must also load on 64 KB ARM64 hosts.
func TestUpstreamNativePlatformARM64PageAlignment(t *testing.T) {
	path := filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "runtime-node", "shims", "pi-dist", "pi-tui", "native", "linux", "prebuilds", "linux-arm64", "linux-platform-x11.node")
	binary, err := elf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = binary.Close() }()
	loads := 0
	for _, segment := range binary.Progs {
		if segment.Type != elf.PT_LOAD {
			continue
		}
		loads++
		if segment.Vaddr%65536 != segment.Off%65536 || segment.Align < 65536 {
			t.Errorf("PT_LOAD address=%#x offset=%#x alignment=%#x does not support 64 KB pages", segment.Vaddr, segment.Off, segment.Align)
		}
	}
	if loads == 0 {
		t.Fatal("ELF has no load segments")
	}
}

// packages/tui/test/native-platform.test.ts:31. Like Pi, this test only replaces the desktop clipboard on explicit Windows opt-in.
func TestUpstreamNativePlatformWindowsClipboardText(t *testing.T) {
	if runtime.GOOS != "windows" || os.Getenv("PI_TEST_NATIVE_CLIPBOARD") != "1" {
		t.Skip("upstream: Windows test desktop with PI_TEST_NATIVE_CLIPBOARD=1 required; replaces system clipboard contents")
	}
	clipboard := GetNativeClipboard()
	if clipboard == nil || clipboard.SetText == nil {
		t.Fatal("native clipboard has no setText")
	}
	for _, text := range []string{"clipboard café 日本語", "", "second write"} {
		if err := clipboard.SetText(t.Context(), text); err != nil {
			t.Fatal(err)
		}
		got, available, err := clipboard.GetText(t.Context())
		if err != nil || !available || got == nil || *got != text {
			t.Fatalf("getText after %q: text=%v available=%v error=%v", text, got, available, err)
		}
		image, available, err := clipboard.GetImage(t.Context())
		if err != nil || !available || image != nil {
			t.Fatalf("getImage after %q: image=%v available=%v error=%v", text, image, available, err)
		}
	}
}

// packages/tui/test/native-platform.test.ts:47.
func TestUpstreamNativePlatformClipboardIdentity(t *testing.T) {
	if (runtime.GOOS != "darwin" && runtime.GOOS != "windows") || (runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64") {
		t.Skip("upstream: native platform helper requires macOS/Windows arm64/x64")
	}
	clipboard := GetNativeClipboard()
	if clipboard == nil || clipboard.GetText == nil || clipboard.GetImage == nil || clipboard.SetText == nil {
		t.Fatal("native clipboard must expose getText, getImage and setText")
	}
	if clipboard != GetNativePlatformHelper() || clipboard != GetNativeClipboard() {
		t.Fatal("clipboard getters did not retain native helper identity")
	}
}

// packages/tui/test/native-platform.test.ts:63. Inject the Linux platform and cached helper as Pi replaces process.platform and require.cache; no real display or clipboard read is needed.
func TestUpstreamNativePlatformLinuxLazyDisplay(t *testing.T) {
	if runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" {
		t.Skip("upstream: native platform helper requires arm64/x64")
	}
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	available, textCalls, loads := false, 0, 0
	helper := &NativeClipboard{
		GetText: func(context.Context) (*string, bool, error) {
			textCalls++
			if !available {
				return nil, false, nil
			}
			text := "X11"
			return &text, true, nil
		},
		GetImage: func(context.Context) ([]byte, bool, error) { return nil, true, nil },
	}
	getClipboard := func() *NativeClipboard {
		return nativeClipboardFor("linux", runtime.GOARCH, os.Getenv, func() *NativeClipboard { loads++; return helper })
	}
	if getClipboard() != nil || loads != 0 {
		t.Fatal("Wayland-only lookup must not load X11")
	}
	t.Setenv("DISPLAY", ":0")
	clipboard := getClipboard()
	if clipboard != helper || textCalls != 0 {
		t.Fatal("lookup must return the cached helper without reading the display")
	}
	if text, available, err := clipboard.GetText(t.Context()); text != nil || available || err != nil {
		t.Fatalf("unavailable X11 read: %v, %v, %v", text, available, err)
	}
	available = true
	if text, available, err := clipboard.GetText(t.Context()); text == nil || *text != "X11" || !available || err != nil {
		t.Fatalf("recovered X11 read: %v, %v, %v", text, available, err)
	}
	if image, available, err := clipboard.GetImage(t.Context()); image != nil || !available || err != nil {
		t.Fatalf("empty X11 image: %v, %v, %v", image, available, err)
	}
	t.Setenv("DISPLAY", "")
	if getClipboard() != nil {
		t.Fatal("lookup must recheck removed DISPLAY")
	}
	t.Setenv("DISPLAY", ":1")
	if getClipboard() != clipboard {
		t.Fatal("new DISPLAY must reuse the helper")
	}
}
