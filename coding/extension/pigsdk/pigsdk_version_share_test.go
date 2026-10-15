package pigsdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/contentdirs"
)

// writeCacheEntry publishes a complete extension build named digest under cacheRoot, compiled against the SDK with fingerprint.
func writeCacheEntry(t *testing.T, cacheRoot, digest, fingerprint string) string {
	t.Helper()
	entry := filepath.Join(cacheRoot, "ext", digest)
	if err := os.MkdirAll(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := []byte("artifact")
	if err := os.WriteFile(filepath.Join(entry, "bin"), artifact, 0o755); err != nil {
		t.Fatal(err)
	}
	ready, err := json.Marshal(map[string]any{"inputDigest": digest, "size": len(artifact), "artifact": "bin", "target": "t", "created": time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, "ready.json"), ready, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, ".sdk-fingerprint"), []byte(fingerprint), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry
}

// writeForeignGoSDK stands in for a Go SDK another PiG build staged as name under root's state/pigsdk, last used age ago, and returns its directory and fingerprint.
func writeForeignGoSDK(t *testing.T, root, name string, age time.Duration) (string, string) {
	t.Helper()
	dir := filepath.Join(root, "state", "pigsdk", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\n\n// "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, markerFile), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := subprocess.StagedSDKFingerprint(dir, "go")
	if err != nil {
		t.Fatal(err)
	}
	return dir, fingerprint
}

// ownGoSDKDir is the directory this build stages its Go SDK in.
func ownGoSDKDir(t *testing.T, root string) string {
	t.Helper()
	b, ok := bundleFor("go")
	if !ok {
		t.Skip("this build strips the Go extension SDK")
	}
	hash, err := b.hash()
	if err != nil {
		t.Fatal(err)
	}
	name, err := contentdirs.Name("sdk", hash)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "state", "pigsdk", name)
}

func requireExists(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func requireGone(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s survived: %v", what, err)
	}
}

// Two PiG builds share a config root: staging this build's SDKs leaves the other build's SDK and the extension builds made against it in place, and stages into this build's own directory.
func TestStagingKeepsAnotherBuildsSDKAndItsBuilds(t *testing.T) {
	root := t.TempDir()
	own := ownGoSDKDir(t, root)
	other, otherFingerprint := writeForeignGoSDK(t, root, "sdk-aaaaaaaaaaaaaaaa", time.Minute)
	otherBuild := writeCacheEntry(t, filepath.Join(root, "cache"), "other-build", otherFingerprint)
	obsolete := writeCacheEntry(t, filepath.Join(root, "cache"), "obsolete-build", "obsolete-sdk")

	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(other, markerFile)); err != nil || string(data) != "sdk-aaaaaaaaaaaaaaaa\n" {
		t.Fatalf("other build's SDK marker = %q, %v; want it untouched", data, err)
	}
	requireExists(t, otherBuild, "the other build's extension build")
	requireExists(t, filepath.Join(own, markerFile), "this build's staged Go SDK")
	requireGone(t, obsolete, "a build against no staged SDK")
}

// A warm start writes nothing, even with another build's SDK beside this build's.
func TestWarmStartBesideAnotherBuildWritesNothing(t *testing.T) {
	root := t.TempDir()
	own := ownGoSDKDir(t, root)
	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	other, _ := writeForeignGoSDK(t, root, "sdk-bbbbbbbbbbbbbbbb", time.Minute)
	marker := filepath.Join(own, markerFile)
	before, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("this build's Go SDK was not staged in its own directory: %v", err)
	}
	otherBefore, err := os.Stat(filepath.Join(other, markerFile))
	if err != nil {
		t.Fatal(err)
	}

	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(marker)
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("warm EnsureSynced rewrote this build's marker: %v", err)
	}
	otherAfter, err := os.Stat(filepath.Join(other, markerFile))
	if err != nil || !otherAfter.ModTime().Equal(otherBefore.ModTime()) {
		t.Fatalf("warm EnsureSynced rewrote the other build's marker: %v", err)
	}
}

// An older PiG stages into the unhashed state/pigsdk/sdk and may still be running: staging leaves that directory and its extension builds alone.
func TestStagingKeepsTheLegacyUnhashedSDKAndItsBuilds(t *testing.T) {
	root := t.TempDir()
	own := ownGoSDKDir(t, root)
	legacy, legacyFingerprint := writeForeignGoSDK(t, root, "sdk", time.Minute)
	legacyBuild := writeCacheEntry(t, filepath.Join(root, "cache"), "legacy-build", legacyFingerprint)

	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(legacy, markerFile)); err != nil || string(data) != "sdk\n" {
		t.Fatalf("legacy SDK marker = %q, %v; want it untouched", data, err)
	}
	requireExists(t, legacyBuild, "the older PiG's extension build")
	requireExists(t, filepath.Join(own, markerFile), "this build's staged Go SDK")
}

// Staging evicts other builds' SDKs beyond the contentdirs.Keep most recently used, never its own, and drops the extension builds of the evicted ones.
func TestStagingEvictsLeastRecentlyUsedSDKsAndTheirBuilds(t *testing.T) {
	root := t.TempDir()
	own := ownGoSDKDir(t, root)
	cacheRoot := filepath.Join(root, "cache")
	type foreign struct{ dir, build string }
	var others []foreign
	for i, name := range []string{"sdk-1111111111111111", "sdk-2222222222222222", "sdk-3333333333333333", "sdk-4444444444444444", "sdk-5555555555555555"} {
		dir, fingerprint := writeForeignGoSDK(t, root, name, time.Duration(i+1)*time.Hour)
		others = append(others, foreign{dir, writeCacheEntry(t, cacheRoot, "build-"+name, fingerprint)})
	}

	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	requireExists(t, filepath.Join(own, markerFile), "this build's staged Go SDK")
	for _, kept := range others[:contentdirs.Keep-1] {
		requireExists(t, kept.dir, "a recently used SDK")
		requireExists(t, kept.build, "a build of a recently used SDK")
	}
	for _, evicted := range others[contentdirs.Keep-1:] {
		requireGone(t, evicted.dir, "a least recently used SDK")
		requireGone(t, evicted.build, "a build of an evicted SDK")
	}
}

// A warm start records that this build's staged SDK directories are in use, so eviction by another build ranks them by their last use rather than by when they were staged. A build in daily use whose SDK was staged long ago must not be the least recently used.
func TestWarmStartRecordsTheUseOfThisBuildsSDKDirectories(t *testing.T) {
	root := t.TempDir()
	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	staged := time.Now().Add(-48 * time.Hour)
	var dirs []string
	for _, b := range bundles() {
		dir, err := b.stagedDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dir, staged, staged); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	if len(dirs) == 0 {
		t.Skip("this build embeds no extension SDK")
	}
	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.ModTime().After(staged.Add(47*time.Hour)) {
			t.Fatalf("a warm start did not record the use of %s: %v", dir, err)
		}
	}
	// The single-language path, used by `pig extension init` and /reload, records the use too.
	own := ownGoSDKDir(t, root)
	if err := os.Chtimes(own, staged, staged); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSyncedLang(root, "go"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(own); err != nil || !info.ModTime().After(staged.Add(47*time.Hour)) {
		t.Fatalf("EnsureSyncedLang did not record the use of %s: %v", own, err)
	}
}
