package coding

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// upstream: packages/coding-agent/src/core/extensions/loader.ts discoverExtensionsInDir over readdirSync(dir, { withFileTypes: true }): Node decodes each name as UTF-8, so a file named with invalid
// UTF-8 is discovered under its decoded path (which does not exist, and Pi's loader then reports "Cannot find module") and a directory so named is entered at its decoded path and found empty.
// Probed with Pi 1.1.0's discoverAndLoadExtensions: ok.ts loaded, "b\uFFFDd.ts" reported as an error.
func TestDiscoverExtensionsInDirListsNamesAsNodeDecodesThem(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("file names are arbitrary bytes only on Linux")
	}
	dir := t.TempDir()
	for _, name := range []string{"ok.ts", "b\xffd.ts", "sub\xff/index.ts", "sub/index.ts"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("export default function(){}"), 0o644); err != nil {
			t.Skipf("file system refuses the name %q: %v", name, err)
		}
	}
	want := []string{filepath.Join(dir, "b\uFFFDd.ts"), filepath.Join(dir, "ok.ts"), filepath.Join(dir, "sub", "index.ts")}
	if got := discoverExtensionsInDir(dir); !slices.Equal(got, want) {
		t.Fatalf("discoverExtensionsInDir = %q, want %q", got, want)
	}
}
