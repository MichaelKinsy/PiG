//go:build darwin

package nativeplatform

import (
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// putImageOnDesktopClipboard copies img to the pasteboard as PNG data, as Finder's and a screenshot's copy do.
func putImageOnDesktopClipboard(t *testing.T, img image.Image) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clipboard.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	script := `set the clipboard to (read (POSIX file "` + path + `") as «class PNGf»)`
	if out, err := exec.CommandContext(t.Context(), "osascript", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("osascript: %v\n%s", err, out)
	}
}
