//go:build !windows

package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi clipboard-image.ts uses the native helper on macOS, so a temporary path is never executable AppleScript or needed for the transfer.
func TestMacClipboardTemporaryPathIsDataNotAppleScript(t *testing.T) {
	root := filepath.Join(t.TempDir(), "clip \" & (do shell script \"echo INJECTED\") & \"\nback\\slash")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	f := newClipboardImageFixture(t, "darwin", map[string]string{})
	assertClipboardImage(t, upstreamClipboardPNG)
	if len(f.commands) != 0 || f.imageCalls != 1 {
		t.Fatalf("commands=%v native reads=%d; path must not enter a shell", f.commands, f.imageCalls)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("native transfer left temporary files: %v, %v", entries, err)
	}
}
