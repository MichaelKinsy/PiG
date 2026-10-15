package pigdocs

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/MichaelKinsy/PiG/internal/contentdirs"
)

// writeBundle creates parent/name with one README and a marker, used age ago.
func writeBundle(t *testing.T, parent, name, readme, digest string, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string]string{"README.md": readme, markerFile: digest + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Two PiG builds that share a config root each keep their own docs. A start of this build writes its own bundle and leaves another build's bundle and the flat pages an older PiG keeps directly in <config root>/docs byte-identical, so neither build rewrites the other's docs on every switch and a running session's docs never change under it.
func TestEnsureSyncedLeavesAnotherBuildsDocsUntouched(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, SubDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string]string{"README.md": "older pig README\n", markerFile: "older-digest\n"} {
		if err := os.WriteFile(filepath.Join(parent, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeBundle(t, parent, "bundle-0123456789abcdef", "other build README\n", "0123456789abcdef-other", time.Hour)
	before := snapshotTree(t, parent)

	for range 2 {
		if err := EnsureSynced(root); err != nil {
			t.Fatal(err)
		}
	}
	after := snapshotTree(t, parent)
	for name, value := range before {
		if after[name] != value {
			t.Errorf("%s changed:\nbefore %q\nafter  %q", name, value, after[name])
		}
	}
	digest, err := contentDigest()
	if err != nil {
		t.Fatal(err)
	}
	if !current(DocsDir(root), digest) {
		t.Fatalf("this build's bundle %s was not written", DocsDir(root))
	}
	if filepath.Dir(DocsDir(root)) != parent {
		t.Fatalf("this build's bundle %s is not in %s", DocsDir(root), parent)
	}
}

// A sync that writes this build's bundle keeps the most recently used bundles of other builds, up to contentdirs.Keep with its own, and removes the rest. The flat pages of an older PiG are not a bundle and stay.
func TestSyncEvictsTheLeastRecentlyUsedBundlesOfOtherBuilds(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, SubDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(parent, "README.md")
	if err := os.WriteFile(legacy, []byte("older pig README\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var others []string
	for i, hash := range []string{"1111111111111111", "2222222222222222", "3333333333333333", "4444444444444444", "5555555555555555"} {
		others = append(others, writeBundle(t, parent, "bundle-"+hash, "other\n", hash, time.Duration(i+1)*time.Hour))
	}
	if _, err := Sync(root); err != nil {
		t.Fatal(err)
	}
	for i, dir := range others {
		_, err := os.Stat(dir)
		if kept := i < contentdirs.Keep-1; kept != (err == nil) {
			t.Errorf("bundle %s (used %dh ago): kept = %t, want %t", filepath.Base(dir), i+1, err == nil, kept)
		}
	}
	for _, path := range []string{legacy, DocsDir(root)} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was removed: %v", path, err)
		}
	}
}

// Concurrent first starts of one build on a fresh config root each find this build's bundle. A sync that wins the staging lock sweeps stranded staging trees and may replace the bundle directory; neither may remove a tree another live sync is still writing or leave that sync without a bundle. Before the staging lock was held shared for each sync, about one sync in forty failed here with a missing staging file or a failed replace.
func TestConcurrentSyncsOfOneBuildAllSucceed(t *testing.T) {
	digest, err := contentDigest()
	if err != nil {
		t.Fatal(err)
	}
	const syncs = 8
	for range 50 {
		root := t.TempDir()
		errs := make([]error, syncs)
		var wg sync.WaitGroup
		for i := range syncs {
			wg.Go(func() { errs[i] = EnsureSynced(root) })
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatal(err)
		}
		if !current(DocsDir(root), digest) {
			t.Fatalf("%s holds no complete bundle after concurrent syncs", DocsDir(root))
		}
		if leftovers, _ := filepath.Glob(filepath.Join(root, SubDir, stagingPrefix+"*")); len(leftovers) != 0 {
			t.Fatalf("concurrent syncs left staging trees %v", leftovers)
		}
	}
}

// A forced sync replaces a bundle directory already there, as `pig docs sync` does to repair edited or removed pages.
func TestSyncReplacesAnExistingBundle(t *testing.T) {
	root := t.TempDir()
	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(DocsDir(root), "README.md")
	if err := os.WriteFile(readme, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(root); err != nil {
		t.Fatal(err)
	}
	want, err := Read("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(readme); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("README.md after a forced sync = %q, %v; want the embedded page", got, err)
	}
}

// Housekeeping sweeps staging trees only under the exclusive staging lock. A live sync holds that lock shared while its tree exists, so a sweep started meanwhile leaves the tree alone; once no sync holds it, the tree is stranded and the next sweep removes it.
func TestHousekeepingLeavesTheTreeOfASyncHoldingTheLockShared(t *testing.T) {
	parent := filepath.Join(t.TempDir(), SubDir)
	tree := filepath.Join(parent, stagingPrefix+"live")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	live := flock.New(filepath.Join(parent, stagingLockFile))
	if held, err := live.TryRLock(); err != nil || !held {
		t.Fatalf("shared staging lock: held %t, err %v", held, err)
	}
	housekeep(parent, filepath.Join(parent, "bundle-0000000000000000"))
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("housekeeping removed the tree of a live sync: %v", err)
	}
	if err := live.Unlock(); err != nil {
		t.Fatal(err)
	}
	housekeep(parent, filepath.Join(parent, "bundle-0000000000000000"))
	if _, err := os.Stat(tree); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("housekeeping kept the stranded tree: %v", err)
	}
}

// A warm start records that this build's bundle is in use, so another build's sync evicts bundles by their last use rather than by when they were written. A bundle in daily use that was written long ago must not be the least recently used.
func TestWarmStartRecordsTheUseOfThisBuildsBundle(t *testing.T) {
	root := t.TempDir()
	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	written := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(DocsDir(root), written, written); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSynced(root); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(DocsDir(root)); err != nil || !info.ModTime().After(written.Add(47*time.Hour)) {
		t.Fatalf("a warm start did not record the use of %s: %v", DocsDir(root), err)
	}
}
