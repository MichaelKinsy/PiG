//go:build !pig_strip_extension_sdk_python

package pigsdk

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

// Staging builds the new tree beside the old one and swaps it in: the marker only ever appears with every file it vouches for, files and directories get the documented modes, and no staging tree survives.
func TestSyncSwapsCompleteTreeAndRemovesStagingLeftovers(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state", "pigsdk")
	crashed := filepath.Join(stateDir, stagePrefix(pythonSDKDir(t, root))+"crashed")
	if err := os.MkdirAll(crashed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(crashed, "partial.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Sync(root, false); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(stateDir, ".stage-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("staging trees after Sync = %v, want none", leftovers)
	}
}

// pythonSDKDir is the directory this build stages its Python SDK in under root.
func pythonSDKDir(t *testing.T, root string) string {
	t.Helper()
	dir, err := SDKDirFor(root, "python")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// The documented modes are 0755 for a directory and 0644 for a file. Windows
// keeps only a read-only attribute, which neither mode sets, so Go reports a
// directory there as 0777 and a writable file as 0666.
func TestSyncedTreeHoldsExactlyTheBundleWithDocumentedModes(t *testing.T) {
	dirMode, fileMode := fs.FileMode(0o755), fs.FileMode(0o644)
	if runtime.GOOS == "windows" {
		dirMode, fileMode = 0o777, 0o666
	}
	for _, bundle := range bundles() {
		t.Run(bundle.lang, func(t *testing.T) {
			root := t.TempDir()
			if _, err := bundle.sync(root); err != nil {
				t.Fatalf("sync: %v", err)
			}
			dir, err := bundle.stagedDir(root)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if entry.IsDir() {
					if info.Mode().Perm() != dirMode {
						t.Errorf("%s mode = %v, want %v", path, info.Mode().Perm(), dirMode)
					}
					return nil
				}
				if info.Mode().Perm() != fileMode {
					t.Errorf("%s mode = %v, want %v", path, info.Mode().Perm(), fileMode)
				}
				rel, _ := filepath.Rel(dir, path)
				got = append(got, filepath.ToSlash(rel))
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			want := append(slices.Clone(bundle.files), markerFile)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("staged files = %v, want %v", got, want)
			}
			siblings, err := os.ReadDir(filepath.Dir(dir))
			if err != nil {
				t.Fatal(err)
			}
			for _, sibling := range siblings {
				if strings.HasPrefix(sibling.Name(), ".stage-") {
					t.Errorf("staging tree %s survived a successful sync", sibling.Name())
				}
			}
		})
	}
}

// A failed sync leaves the previously staged tree intact and removes its staging tree.
func TestSyncFailureKeepsPreviousTreeAndCleansStaging(t *testing.T) {
	root := t.TempDir()
	bundle, ok := bundleFor("python")
	if !ok {
		t.Fatal("no python bundle")
	}
	if _, err := bundle.sync(root); err != nil {
		t.Fatal(err)
	}
	dir := pythonSDKDir(t, root)
	before, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil {
		t.Fatal(err)
	}
	broken := bundle
	broken.files = append(slices.Clone(bundle.files), "missing-from-embedded-fs.py")
	if _, err := broken.sync(root); err == nil {
		t.Fatal("sync of a missing embedded file succeeded")
	}
	after, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("previous marker = %q, %v; want %q", after, err, before)
	}
	siblings, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, sibling := range siblings {
		if strings.HasPrefix(sibling.Name(), ".stage-") {
			t.Errorf("failed sync left staging tree %s", sibling.Name())
		}
	}
}

// With no cached extension builds, Prune classifies nothing and must not hash every staged SDK tree. It still fails when no SDK is staged and still creates the cache root and GC lock.
func TestPruneWithoutCacheEntriesDoesNotFingerprintStagedSDKs(t *testing.T) {
	root := t.TempDir()
	if _, err := Prune(root, false, false); err == nil || !strings.Contains(err.Error(), "no staged SDK found") {
		t.Fatalf("Prune without a staged SDK = %v, want the no-staged-SDK error", err)
	}
	if _, _, err := Sync(root, false); err != nil {
		t.Fatal(err)
	}
	report, err := Prune(root, false, false)
	if err != nil {
		t.Fatalf("Prune with staged SDK and no cache entries: %v", err)
	}
	if len(report.Entries) != 0 {
		t.Fatalf("entries = %v, want none", report.Entries)
	}
	if _, err := os.Stat(filepath.Join(root, "cache", ".gc.lock")); err != nil {
		t.Fatalf("Prune must still take the cache GC lock: %v", err)
	}
	const fingerprintAllocations = 300
	got := testing.AllocsPerRun(5, func() { _, _ = Prune(root, false, false) })
	fingerprinting := testing.AllocsPerRun(5, func() { _, _ = LiveFingerprints(root) })
	if got >= fingerprinting/2 || got > fingerprintAllocations {
		t.Fatalf("Prune with no cache entries = %.0f allocations, fingerprinting alone = %.0f", got, fingerprinting)
	}
}

// A build published while Prune waits for the cache GC lock must be classified against the staged SDK fingerprints (cache_lifecycle.go: one classification under .gc.lock), not against the empty answer read before the lock.
func TestPruneClassifiesBuildPublishedWhileWaitingForGCLock(t *testing.T) {
	root := t.TempDir()
	if _, _, err := Sync(root, false); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(root, "cache")
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	gc := flock.New(filepath.Join(cacheRoot, ".gc.lock"))
	if err := gc.Lock(); err != nil {
		t.Fatal(err)
	}
	type result struct {
		removed int
		err     error
	}
	done := make(chan result, 1)
	go func() {
		report, err := Prune(root, false, false)
		done <- result{report.Removed, err}
	}()
	select {
	case got := <-done:
		t.Fatalf("Prune finished while the GC lock was held: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
	entry := writeCacheEntry(t, cacheRoot, "obsolete", "obsolete-sdk")
	if err := gc.Unlock(); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) || got.removed != 1 {
		t.Fatalf("obsolete build survived Prune: stat = %v, removed = %d", err, got.removed)
	}
}

// A rename failure or a crash during the swap must not lose the previously valid SDK: until the new tree is in place, the previous tree still exists beside the target, and a failed swap puts it back.
func TestSyncSwapNeverLosesPreviousTree(t *testing.T) {
	root := t.TempDir()
	bundle, ok := bundleFor("python")
	if !ok {
		t.Fatal("no python bundle")
	}
	if _, err := bundle.sync(root); err != nil {
		t.Fatal(err)
	}
	dir := pythonSDKDir(t, root)
	previous, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, markerFile), []byte("older\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous = []byte("older\n")

	original := renameDir
	t.Cleanup(func() { renameDir = original })
	var recoverable bool
	renameDir = func(from, to string) error {
		if to == dir && !strings.HasSuffix(from, ".old") {
			// The instant before the new tree is in place: a crash here must leave the previous SDK on disk.
			siblings, err := os.ReadDir(filepath.Dir(dir))
			if err != nil {
				t.Fatal(err)
			}
			for _, sibling := range siblings {
				if data, err := os.ReadFile(filepath.Join(filepath.Dir(dir), sibling.Name(), markerFile)); err == nil && bytes.Equal(data, previous) {
					recoverable = true
				}
			}
			return errors.New("injected rename failure")
		}
		return original(from, to)
	}
	if _, err := bundle.sync(root); err == nil {
		t.Fatal("sync succeeded despite a failed swap")
	}
	if !recoverable {
		t.Fatal("previous SDK tree was not on disk when the new tree was renamed into place")
	}
	after, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil || !bytes.Equal(after, previous) {
		t.Fatalf("previous marker after failed swap = %q, %v; want %q", after, err, previous)
	}
	siblings, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, sibling := range siblings {
		if strings.HasPrefix(sibling.Name(), ".stage-") {
			t.Errorf("failed swap left %s", sibling.Name())
		}
	}
}

// A swap whose rollback also failed leaves the previous SDK only beside the target. The next sync must put it back before it removes staging leftovers, so a retry that fails still leaves the previous SDK in place.
func TestSyncRestoresPreviousTreeStrandedByFailedRollback(t *testing.T) {
	root := t.TempDir()
	bundle, ok := bundleFor("python")
	if !ok {
		t.Fatal("no python bundle")
	}
	if _, err := bundle.sync(root); err != nil {
		t.Fatal(err)
	}
	dir := pythonSDKDir(t, root)
	if err := os.WriteFile(filepath.Join(dir, markerFile), []byte("older\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	original := renameDir
	t.Cleanup(func() { renameDir = original })
	renameDir = func(from, to string) error {
		if to == dir {
			return errors.New("injected rename failure")
		}
		return original(from, to)
	}
	if _, err := bundle.sync(root); err == nil {
		t.Fatal("sync succeeded although both renames into place failed")
	}
	renameDir = original
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("setup: target exists after a failed rollback: %v", err)
	}

	broken := bundle
	broken.files = append(slices.Clone(bundle.files), "missing-from-embedded-fs.py")
	if _, err := broken.sync(root); err == nil {
		t.Fatal("sync of a missing embedded file succeeded")
	}
	after, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil || string(after) != "older\n" {
		t.Fatalf("previous SDK after a failed retry = %q, %v; want it restored", after, err)
	}
}

// A process killed after the swap but before it removed the previous tree strands that tree. A start that finds every SDK current must still remove it, and only the languages it was asked about.
func TestEnsureSyncedRemovesStagingLeftoversWhenSDKIsCurrent(t *testing.T) {
	operations := map[string]struct {
		run      func(root string) error
		keepRust bool
	}{
		"all languages": {EnsureSynced, false},
		"one language":  {func(root string) error { return EnsureSyncedLang(root, "python") }, true},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if _, _, err := Sync(root, false); err != nil {
				t.Fatal(err)
			}
			stateDir := filepath.Join(root, "state", "pigsdk")
			removed := stagePrefix(pythonSDKDir(t, root)) + "crashed.old"
			leftovers := []string{removed}
			var kept string
			if operation.keepRust {
				rustDir, err := SDKDirFor(root, "rust")
				if err != nil {
					t.Fatal(err)
				}
				kept = stagePrefix(rustDir) + "crashed"
				leftovers = append(leftovers, kept)
			}
			for _, leftover := range leftovers {
				if err := os.MkdirAll(filepath.Join(stateDir, leftover), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := operation.run(root); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(stateDir, removed)); !os.IsNotExist(err) {
				t.Errorf("stranded %s survived a start with current SDKs: %v", removed, err)
			}
			if kept != "" {
				if _, err := os.Stat(filepath.Join(stateDir, kept)); err != nil {
					t.Errorf("start for one language removed another language's tree: %v", err)
				}
			}
		})
	}
}

// A staging tree another build left beside its own SDK directory belongs to that build: this build's staging never removes or restores it.
func TestStagingLeavesAnotherBuildsStagingTrees(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state", "pigsdk")
	foreign := []string{".stage-sdk-py-aaaaaaaaaaaaaaaa-crashed", ".stage-sdk-py-aaaaaaaaaaaaaaaa-crashed.old", ".stage-python-crashed.old"}
	for _, name := range foreign {
		if err := os.MkdirAll(filepath.Join(stateDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := Sync(root, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range foreign {
		if _, err := os.Stat(filepath.Join(stateDir, name)); err != nil {
			t.Errorf("another build's staging tree %s was removed: %v", name, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(pythonSDKDir(t, root), markerFile)); err != nil || len(data) == 0 {
		t.Fatalf("this build's Python SDK marker = %q, %v", data, err)
	}
}

// The prune that follows a startup stage reads no byte counts, so it must not stat every file of every cached extension build: a cache of large builds made the first start after an upgrade take seconds. pig reload prints the bytes it dropped, so Sync still sizes the entries.
func TestPruneAfterAStartupStageDoesNotSizeCachedBuilds(t *testing.T) {
	root := t.TempDir()
	if _, _, err := Sync(root, false); err != nil {
		t.Fatal(err)
	}
	build := filepath.Join(root, "cache", "ext", "build")
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatal(err)
	}
	// Removing this build's staged SDKs makes every EnsureSynced a stage, which the prune follows.
	cost := func() float64 {
		return testing.AllocsPerRun(3, func() {
			for _, b := range bundles() {
				dir, err := b.stagedDir(root)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
			}
			if err := EnsureSynced(root); err != nil {
				t.Fatal(err)
			}
		})
	}
	const added = 1000
	before := cost()
	for i := range added {
		if err := os.WriteFile(filepath.Join(build, "f"+strconv.Itoa(i)), []byte("01234567"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if grown := cost() - before; grown > 20 {
		t.Errorf("the startup prune allocated %.0f more for %d more files in a cached build", grown, added)
	}
	_, report, err := Sync(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalBytes < 8*added {
		t.Errorf("Sync report = %d bytes, want the cached build's %d bytes counted", report.TotalBytes, 8*added)
	}
	// pig reload --dry-run prints the megabytes Prune would drop, so Prune sizes the entries too.
	report, err = Prune(root, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalBytes < 8*added {
		t.Errorf("Prune report = %d bytes, want the cached build's %d bytes counted", report.TotalBytes, 8*added)
	}
}
