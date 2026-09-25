package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHashSourceDir_IgnoresTransientBuildModfiles guards the parallel-build
// cache-thrashing fix. buildGo writes a uniquely-named temp go.mod/go.sum
// (.pig-build-*) into the extension source dir for staged-SDK builds. Because
// hashDirSources includes .mod/.sum files, a temp modfile from a concurrent
// build in another process would pollute the content hash and mint a divergent
// cache key for identical source. The hash must ignore these transient
// artifacts so N concurrent pig instances converge on one cache entry.
func TestHashSourceDir_IgnoresTransientBuildModfiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package x\n")
	write("go.mod", "module x\n\ngo 1.26\n")

	base, err := hashSourceDir(dir, "go")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate another process's in-flight staged-SDK build leaving its temp
	// modfile+sum in the shared source dir while we hash.
	write(".pig-build-1234567890.mod", "module x\n\nreplace pig/sdk => /tmp/staged-abc\n")
	write(".pig-build-1234567890.sum", "example.com/dep v1.0.0 h1:deadbeef=\n")

	withTemp, err := hashSourceDir(dir, "go")
	if err != nil {
		t.Fatal(err)
	}
	if base != withTemp {
		t.Fatalf("transient temp modfile polluted the source hash (%s != %s): concurrent builds would mint divergent cache keys for identical source", base[:12], withTemp[:12])
	}
}
