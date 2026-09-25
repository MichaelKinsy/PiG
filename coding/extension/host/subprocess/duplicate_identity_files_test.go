package subprocess

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// windowsReservedFileNameChars are the printable characters Windows rejects in
// a file name. A duplicate identity is keyed name:N, so ':' reaches every file
// named after an extension.
const windowsReservedFileNameChars = `<>:"/\|?*`

// The second copy of a duplicate identity builds under its name:N key. On
// Windows that key used to name the cache entry, and publishing it failed with
// "The parameter is incorrect".
func TestBuildCachesDuplicateIdentityUnderPortableFileName(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "ask.mjs")
	if err := os.WriteFile(src, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	builder := NewBuilderWithConfigRoot(root, root)
	result, err := builder.Build("ask:2", src)
	if err != nil {
		t.Fatalf("build ask:2: %v", err)
	}
	entry := filepath.Dir(result.BinaryPath)
	if base := filepath.Base(entry); strings.ContainsAny(base, windowsReservedFileNameChars) {
		t.Fatalf("cache entry %q contains a character Windows rejects in file names", base)
	}
	current, valid, err := builder.CurrentCacheEntry("ask:2", src)
	if err != nil {
		t.Fatal(err)
	}
	if current != entry || !valid {
		t.Fatalf("CurrentCacheEntry = %q (valid %v), want the published entry %q", current, valid, entry)
	}
}

// Each copy of a duplicate identity writes its stderr log under its name:N key.
// On Windows a ':' in that name makes NTFS create an alternate data stream of
// a different file instead of the reported log.
func TestDuplicateIdentityStderrLogHasPortableFileName(t *testing.T) {
	host := NewHost(t.TempDir())
	defer host.Shutdown("test done")
	dir := t.TempDir()
	_, errs := host.LoadAll(t.Context(), []ExtConfig{
		{Name: "duplicate", Path: filepath.Join(dir, "first"), Enabled: true},
		{Name: "duplicate", Path: filepath.Join(dir, "second"), Enabled: true},
	})
	if len(errs) != 2 {
		t.Fatalf("LoadAll errors = %v, want one per copy", errs)
	}
	for _, err := range errs {
		loadErr, ok := errors.AsType[*LoadError](err)
		if !ok || loadErr.StderrLog == "" {
			t.Fatalf("error = %v, want a spawn failure that reports its stderr log", err)
		}
		base := filepath.Base(loadErr.StderrLog)
		if strings.ContainsAny(base, windowsReservedFileNameChars) {
			t.Fatalf("stderr log %q contains a character Windows rejects in file names", base)
		}
		if _, err := os.Stat(loadErr.StderrLog); err != nil {
			t.Fatalf("reported stderr log is not a file: %v", err)
		}
	}
}
