package codemode

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports execute.ts saveImages (Pi 1.0.3, #10310): each image gets a text item with its saved path before it, a repeated
// image is saved once, a failed write becomes part of the label, and an image type without a file extension fails.

const savePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="
const saveGIF = "R0lGODlhAQABAAAAACwAAAAAAQABAAA="

func useSaveTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	return dir
}

func labelText(t *testing.T, item ai.ToolResultMessageContent) string {
	t.Helper()
	text, ok := item.(ai.TextContent)
	if !ok {
		t.Fatalf("item = %#v, want a text label", item)
	}
	return text.Text
}

func TestSaveImagesPutsEachSavedPathBeforeItsImage(t *testing.T) {
	dir := useSaveTempDir(t)
	png := ai.ImageContent{Data: savePNG, MimeType: "image/png"}
	gif := ai.ImageContent{Data: saveGIF, MimeType: "image/gif"}
	out, err := saveImages([]ai.ToolResultMessageContent{ai.TextContent{Text: "a"}, png, ai.TextContent{Text: "b"}, gif, png})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 8 {
		t.Fatalf("items = %#v, want 8", out)
	}
	pngLabel := regexp.MustCompile(`^\[Image saved to (\S+/pi-codemode-[0-9a-f]{16}\.png) \(image/png, 70B\)\]$`).FindStringSubmatch(labelText(t, out[1]))
	gifLabel := regexp.MustCompile(`^\[Image saved to (\S+/pi-codemode-[0-9a-f]{16}\.gif) \(image/gif, 23B\)\]$`).FindStringSubmatch(labelText(t, out[4]))
	if pngLabel == nil || gifLabel == nil {
		t.Fatalf("labels = %q, %q", labelText(t, out[1]), labelText(t, out[4]))
	}
	if out[2] != ai.ToolResultMessageContent(png) || out[5] != ai.ToolResultMessageContent(gif) || labelText(t, out[6]) != labelText(t, out[1]) || out[7] != ai.ToolResultMessageContent(png) {
		t.Errorf("items = %#v", out)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("temp dir = %v, %v, want one file per distinct image", entries, err)
	}
	for path, want := range map[string]string{pngLabel[1]: savePNG, gifLabel[1]: saveGIF} {
		data, err := os.ReadFile(path)
		if err != nil || base64.StdEncoding.EncodeToString(data) != want || filepath.Dir(path) != dir {
			t.Fatalf("saved %s: %v, %v, want %s", path, data, err, want)
		}
		if runtime.GOOS != "windows" {
			if info, _ := os.Stat(path); info.Mode().Perm()&0o077 != 0 {
				t.Errorf("%s mode = %o, want user-only", path, info.Mode().Perm())
			}
		}
	}
}

func TestSaveImagesKeepsTheResultWhenAWriteFails(t *testing.T) {
	dir := useSaveTempDir(t)
	missing := filepath.Join(dir, "missing")
	t.Setenv("TMPDIR", missing)
	t.Setenv("TMP", missing)
	t.Setenv("TEMP", missing)
	png := ai.ImageContent{Data: savePNG, MimeType: "image/png"}
	out, err := saveImages([]ai.ToolResultMessageContent{png})
	if err != nil {
		t.Fatal(err)
	}
	label := labelText(t, out[0])
	if !strings.HasPrefix(label, "[Image (image/png, 70B) could not be saved: ENOENT: no such file or directory, open '") || !strings.HasSuffix(label, ".png']") || out[1] != ai.ToolResultMessageContent(png) {
		t.Errorf("out = %#v", out)
	}
}

func TestSaveImagesFailsForAnImageTypeWithoutAnExtension(t *testing.T) {
	useSaveTempDir(t)
	_, err := saveImages([]ai.ToolResultMessageContent{ai.ImageContent{Data: savePNG, MimeType: "image/bmp"}})
	if err == nil || err.Error() != "No file extension for image type image/bmp" {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveImagesLeavesTextOnlyOutputAlone(t *testing.T) {
	dir := useSaveTempDir(t)
	items := []ai.ToolResultMessageContent{ai.TextContent{Text: "only text"}}
	out, err := saveImages(items)
	if err != nil || len(out) != 1 || out[0] != items[0] {
		t.Fatalf("out = %#v, %v", out, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("temp dir = %v", entries)
	}
}
