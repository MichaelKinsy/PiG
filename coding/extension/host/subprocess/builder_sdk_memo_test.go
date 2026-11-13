package subprocess

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

// benchSDKDir is the Go SDK tree of this repository.
const benchSDKDir = "../../../../extensions/sdk"

// benchSDKTree is the shape of the repository's Go SDK at 3388cdeb2, when the rows of the two benchmarks below were first taken: every directory
// that holds Go sources, with the number it holds (338 in all; the root holds the go.mod as well).
var benchSDKTree = []struct {
	dir     string
	sources int
}{
	{".", 225}, {"frontend", 3}, {"internal/jsloop", 5}, {"internal/kitwire", 3}, {"json", 23}, {"kit", 14}, {"tui", 10},
	{"tui/internal/engine", 11}, {"tui/internal/jsnumber", 1}, {"tui/internal/jsstring", 5}, {"tui/internal/lazyregexp", 1}, {"tui/internal/widthx", 12},
	{"upgrade", 16}, {"upgrade/testdata/fixtures/sdk-0.2.0/input", 1}, {"upgrade/testdata/fixtures/sdk-0.2.0/want", 1},
	{"upgrade/testdata/fixtures/sdk-0.4.1-config-home/input", 2}, {"upgrade/testdata/fixtures/sdk-0.4.1-config-home/want", 2},
	{"upgrade/testdata/fixtures/sdk-0.4.1/input", 1}, {"upgrade/testdata/fixtures/sdk-dev/input", 1}, {"upgrade/testdata/fixtures/sdk-dev/want", 1},
}

// benchSDKSourceBytes is the mean size of a Go source of that SDK (2.65 MB in 338 files).
const benchSDKSourceBytes = 7840

// writeBenchSDK writes the staged SDK the two benchmarks below read: a generated tree of benchSDKTree's shape, with the four files beside the
// sources that the hash does not read and the hidden directory it does not enter. The tree is fixed, so the rows do not move when the
// repository's SDK gains a file.
func writeBenchSDK(b *testing.B) string {
	b.Helper()
	root := b.TempDir()
	write := func(rel string, content []byte) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	write("go.mod", []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\ngo 1.26\n"))
	line := []byte("// a line of a generated SDK source\n")
	source := bytes.Repeat(line, benchSDKSourceBytes/len(line)+1)[:benchSDKSourceBytes]
	for _, dir := range benchSDKTree {
		for i := range dir.sources {
			write(filepath.Join(dir.dir, fmt.Sprintf("source%03d.go", i)), source)
		}
	}
	for _, other := range []string{"LICENSE", "README.md", "json/LICENSE", "json/README.md", ".gz/record.gz"} {
		write(other, []byte("not a source\n"))
	}
	return root
}

// BenchmarkStagedSDKWalkHash is what every Go extension's cache key cost before the memo: read and hash the SDK.
func BenchmarkStagedSDKWalkHash(b *testing.B) {
	sdk := writeBenchSDK(b)
	for b.Loop() {
		if err := hashDirSources(sha256.New(), sdk, sdkSourceExts("go")); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStagedSDKKeyMemoized is a cache key after the first: stat the SDK sources, hash nothing of them.
//
// allocs/sdk-file is the same key over the repository's own SDK, for each of its sources: what the benchmarks that start a real Go extension
// against that SDK (BenchmarkNodeAdmissionStartup/node-go-node, BenchmarkDuplicateGoFolderStartup, BenchmarkReload_PackedCellWarmCache) pay for
// every key they compute, so their rows grow by a multiple of it with every file the SDK gains.
func BenchmarkStagedSDKKeyMemoized(b *testing.B) {
	base := hex.EncodeToString(make([]byte, 32))
	sdk := writeBenchSDK(b)
	if _, err := hashWithStagedSDK(base, sdk, "go"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := hashWithStagedSDK(base, sdk, "go"); err != nil {
			b.Fatal(err)
		}
	}
	sources := 0
	if err := walkDirSources(benchSDKDir, sdkSourceExts("go"), func(string, string) error { sources++; return nil }); err != nil {
		b.Fatal(err)
	}
	perKey := testing.AllocsPerRun(1, func() {
		if _, err := hashWithStagedSDK(base, benchSDKDir, "go"); err != nil {
			b.Fatal(err)
		}
	})
	b.ReportMetric(perKey/float64(sources), "allocs/sdk-file")
}
