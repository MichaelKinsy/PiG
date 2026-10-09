package codingagent

// pi: packages/coding-agent/src/utils/clipboard.ts

// pi: packages/coding-agent/src/utils/clipboard-image.ts

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// Upstream bounds every clipboard command: DEFAULT_LIST_TIMEOUT_MS (1 s) for type listings and wslpath,
// DEFAULT_POWERSHELL_TIMEOUT_MS (5 s) for the WSL PowerShell read, and runClipboardCommand's own 3 s for every data read
// (clipboard-image.ts, clipboard-command.ts). A longer bound would let a hung compositor stall a paste.
func TestClipboardImageCommandsKeepUpstreamTimeouts(t *testing.T) {
	budgets := map[string]time.Duration{}
	record := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Errorf("%s %v has no deadline", name, args)
		}
		key := name + " " + strings.Join(args, " ")
		if name == "wslpath" || name == "powershell.exe" {
			key = name
		}
		budgets[key] = time.Until(deadline)
		switch name {
		case "wl-paste":
			if args[0] == "--list-types" {
				return []byte("text/plain\n"), nil
			}
		case "xclip":
			if slices.Contains(args, "TARGETS") {
				return []byte("image/png\n"), nil
			}
			return linuxClipboardPNG, nil
		case "wslpath":
			return []byte(`C:\clip.png` + "\n"), nil
		case "powershell.exe":
			return []byte("empty\n"), nil
		}
		return nil, os.ErrNotExist
	}
	withEnv(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	previous := clipboardRun
	clipboardRun = record
	t.Cleanup(func() { clipboardRun = previous })

	// WSL: wl-paste lists no image, so nothing falls through to xclip; PowerShell runs after wslpath.
	if _, _, err := readClipboardImageLinux(); err != nil {
		t.Fatal(err)
	}
	// Wayland that fails: xclip lists, then reads the data.
	clipboardRun = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "wl-paste" {
			return nil, os.ErrNotExist
		}
		return record(ctx, name, args...)
	}
	if _, _, err := readClipboardImageLinux(); err != nil {
		t.Fatal(err)
	}
	// Wayland with an image: the listing, then the data read.
	clipboardRun = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "wl-paste" && args[0] == "--list-types" {
			deadline, _ := ctx.Deadline()
			budgets["wl-paste --list-types"] = time.Until(deadline)
			return []byte("image/png\n"), nil
		}
		return record(ctx, name, args...)
	}
	if _, _, err := readClipboardImageLinux(); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]time.Duration{
		"wl-paste --type image/png --no-newline":     3 * time.Second,
		"xclip -selection clipboard -t image/png -o": 3 * time.Second,
		"wl-paste --list-types":                      time.Second,
		"wslpath":                                    time.Second,
		"powershell.exe":                             5 * time.Second,
		"xclip -selection clipboard -t TARGETS -o":   time.Second,
	} {
		got, ok := budgets[key]
		if !ok || got > want || got < want-500*time.Millisecond {
			t.Errorf("%s budget = %v (seen %v), want %v", key, got, ok, want)
		}
	}
}

// Upstream readClipboardImageViaNativeClipboard labels bytes that no supported format matches "application/octet-stream", and
// readClipboardImage then tries to convert them to PNG and returns null when they do not decode. The bytes never reach the
// caller labelled as an image.
func TestClipboardImageNativeBytesOfUnknownFormatAreNotReturnedAsAnImage(t *testing.T) {
	oldPlatform, oldNative := clipboardGOOS, getNativeClipboard
	t.Cleanup(func() { clipboardGOOS, getNativeClipboard = oldPlatform, oldNative })
	clipboardGOOS = "darwin"
	getNativeClipboard = func() *tui.NativeClipboard {
		return &tui.NativeClipboard{GetImage: func(context.Context) ([]byte, bool, error) {
			return []byte("not an image at all"), true, nil
		}}
	}
	data, mime, err := ReadClipboardImage()
	if err != nil || data != nil || mime != "" {
		t.Fatalf("ReadClipboardImage = %q, %q, %v; want nothing", data, mime, err)
	}
}

// Upstream accepts the PowerShell read only when its output is "ok": an "empty" answer leaves a stale temp file that must not
// be returned as the clipboard image.
func TestReadClipboardImageWSLIgnoresAFileWhenPowerShellReportsEmpty(t *testing.T) {
	withEnv(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "WAYLAND_DISPLAY": "wayland-0"})
	w := withWindowsClipboard(t, &windowsClipboard{
		scriptedClipboard: scriptedClipboard{responses: map[string]fakeCall{"wl-paste --list-types": {out: []byte("text/plain\n")}}},
		winPath:           `C:\Users\x\clip.png`,
		image:             linuxClipboardPNG,
		output:            "empty",
	})
	data, mime, err := readClipboardImageLinux()
	if err != nil || data != nil || mime != "" {
		t.Fatalf("read = %q, %q, %v; want nothing (calls %v)", data, mime, err, w.log)
	}
}

// Upstream extensionForImageMimeType maps the four supported types to png, jpg, webp and gif after stripping parameters and
// case, and returns null for anything else; the paste caller then names the file ".png" (clipboard-image.ts, interactive-mode.ts).
func TestClipboardImageExtensions(t *testing.T) {
	for mime, want := range map[string]string{
		"image/png": "png", "image/jpeg": "jpg", "IMAGE/WEBP": "webp", "image/gif; q=1": "gif", "image/bmp": "", "text/plain": "", "": "",
	} {
		if got := ExtensionForImageMIME(mime); got != want {
			t.Errorf("ExtensionForImageMIME(%q) = %q, want %q", mime, got, want)
		}
	}
	t.Setenv("TMPDIR", t.TempDir())
	for mime, want := range map[string]string{"image/jpeg": ".jpg", "image/bmp": ".png", "": ".png"} {
		path, err := SaveClipboardImageToTempFile([]byte("x"), mime)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(path, want) {
			t.Errorf("SaveClipboardImageToTempFile(%q) = %s, want suffix %s", mime, path, want)
		}
	}
}
