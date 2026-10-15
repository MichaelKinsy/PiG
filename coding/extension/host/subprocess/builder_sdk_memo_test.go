package subprocess

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// writeStagedGoSDK writes a small Go SDK tree at sdkDir: sources in the root and in a package below it, and a file the hash skips.
func writeStagedGoSDK(t *testing.T, sdkDir, body string) {
	t.Helper()
	files := map[string]string{
		"go.mod":           "module github.com/MichaelKinsy/PiG/extensions/sdk\ngo 1.26\n",
		"sdk.go":           "package sdk\n" + body,
		"json/encode.go":   "package json\n",
		"README.md":        "not a source\n",
		"lib.rs":           "// a Rust source in a Go SDK\n",
		"crate/Cargo.toml": "[package]\nname = \"pig-sdk\"\n",
	}
	for name, content := range files {
		path := filepath.Join(sdkDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// writeGoExtension writes a Go extension that builds against the staged SDK.
func writeGoExtension(t *testing.T, name string) string {
	t.Helper()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module "+name+"\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\nimport _ \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return srcDir
}

// walkFingerprint is StagedSDKFingerprint as a walk of the tree: the first 16 hex digits of the SHA-256 of every source file's relative
// path and content.
func walkFingerprint(t *testing.T, sdkDir string, exts []string) string {
	t.Helper()
	h := sha256.New()
	if err := hashDirSources(h, sdkDir, exts); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Every Go or Rust extension's cache key folds in the staged SDK. The first extension reads and hashes it; a second extension of the same
// process reads none of its files (they are unreadable here), and the fingerprint a build records reads none either.
func TestSecondStagedSDKBuildReadsNoSDKFile(t *testing.T) {
	configRoot := t.TempDir()
	sdkDir := stagedSDKDir(t, configRoot, "go")
	writeStagedGoSDK(t, sdkDir, "")
	builder := NewBuilderWithConfigRoot(t.TempDir(), configRoot)
	if _, _, err := builder.CurrentCacheEntry("first", writeGoExtension(t, "first")); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := StagedSDKFingerprint(sdkDir, "go")
	if err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(sdkDir, "sdk.go")
	if err := os.Chmod(source, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(source, 0o644) })
	if _, err := os.ReadFile(source); err == nil {
		t.Skip("file permissions do not apply to this user")
	}

	if _, _, err := builder.CurrentCacheEntry("second", writeGoExtension(t, "second")); err != nil {
		t.Fatalf("second extension's cache key read the staged SDK again: %v", err)
	}
	if again, err := StagedSDKFingerprint(sdkDir, "go"); err != nil || again != fingerprint {
		t.Fatalf("fingerprint after a build = %q, %v; want %q without reading the SDK", again, err, fingerprint)
	}
}

// The memoized fingerprint is the walk of the staged tree, for each language's sources, and a changed tree is hashed again: an edit in
// place, a file added, and a restage that renames a new tree over the directory. The fingerprint and every extension's cache key follow.
func TestStagedSDKFingerprintIsTheWalkAndARestageRehashes(t *testing.T) {
	sdkDir := filepath.Join(t.TempDir(), "go-sdk-0123456789abcdef")
	writeStagedGoSDK(t, sdkDir, "")
	for buildType, exts := range map[string][]string{
		"go":   {".go", ".mod", ".sum"},
		"rust": {".rs", ".toml", ".lock"},
	} {
		got, err := StagedSDKFingerprint(sdkDir, buildType)
		if want := walkFingerprint(t, sdkDir, exts); err != nil || got != want {
			t.Fatalf("%s fingerprint = %q, %v; want the walk %q", buildType, got, err, want)
		}
	}
	link := filepath.Join(t.TempDir(), "link")
	testenv.RequireDirectoryLink(t, sdkDir, link)
	base := hex.EncodeToString(make([]byte, 32))
	key, err := hashWithStagedSDK(base, sdkDir, "go")
	if err != nil {
		t.Fatal(err)
	}
	if viaLink, err := hashWithStagedSDK(base, link, "go"); err != nil || viaLink != key {
		t.Fatalf("cache key through a symlink = %q, %v; want %q", viaLink, err, key)
	}
	// Each change moves the stamps past the file system's time granularity, as a later write does.
	stamp := time.Now()
	touch := func(path string) {
		stamp = stamp.Add(2 * time.Second)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	expectRehash := func(change string) {
		t.Helper()
		got, err := StagedSDKFingerprint(sdkDir, "go")
		if want := walkFingerprint(t, sdkDir, []string{".go", ".mod", ".sum"}); err != nil || got != want {
			t.Fatalf("fingerprint after %s = %q, %v; want the new walk %q", change, got, err, want)
		}
		after, err := hashWithStagedSDK(base, sdkDir, "go")
		if err != nil || after == key {
			t.Fatalf("cache key after %s = %q, %v; want a new key (was %q)", change, after, err, key)
		}
		key = after
	}

	// An edit in place that keeps the size.
	source := filepath.Join(sdkDir, "json", "encode.go")
	if err := os.WriteFile(source, []byte("package jsox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touch(source)
	expectRehash("an edit in place")

	// A source file added in a package below the root.
	added := filepath.Join(sdkDir, "json", "decode.go")
	if err := os.WriteFile(added, []byte("package jsox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touch(added)
	expectRehash("a file added")

	// A restage: a new tree written beside the directory and renamed over it.
	next := sdkDir + ".next"
	writeStagedGoSDK(t, next, "func Restaged() {}\n")
	for _, name := range []string{"go.mod", "sdk.go", "json/encode.go"} {
		touch(filepath.Join(next, name))
	}
	if err := os.Rename(sdkDir, sdkDir+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, sdkDir); err != nil {
		t.Fatal(err)
	}
	expectRehash("a restage")
}

// The Go SDK tree of this repository stands in for a staged SDK (about 200 sources, 1.4 MB).
const benchSDKDir = "../../../../extensions/sdk"

// BenchmarkStagedSDKWalkHash is what every Go extension's cache key cost before the memo: read and hash the SDK.
func BenchmarkStagedSDKWalkHash(b *testing.B) {
	for b.Loop() {
		if err := hashDirSources(sha256.New(), benchSDKDir, sdkSourceExts("go")); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStagedSDKKeyMemoized is a cache key after the first: stat the SDK sources, hash nothing of them.
func BenchmarkStagedSDKKeyMemoized(b *testing.B) {
	base := hex.EncodeToString(make([]byte, 32))
	if _, err := hashWithStagedSDK(base, benchSDKDir, "go"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := hashWithStagedSDK(base, benchSDKDir, "go"); err != nil {
			b.Fatal(err)
		}
	}
}
