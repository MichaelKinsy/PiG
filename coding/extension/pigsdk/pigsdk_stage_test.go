package pigsdk

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

// Staging builds the new tree beside the old one and swaps it in: the marker only ever appears with every file it vouches for, files and directories get the documented modes, and no staging tree survives.
func TestSyncSwapsCompleteTreeAndRemovesStagingLeftovers(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state", "pigsdk")
	if err := os.MkdirAll(filepath.Join(stateDir, ".stage-python-crashed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, ".stage-python-crashed", "partial.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(root); err != nil {
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

func TestSyncedTreeHoldsExactlyTheBundleWithDocumentedModes(t *testing.T) {
	for _, bundle := range bundles() {
		t.Run(bundle.lang, func(t *testing.T) {
			root := t.TempDir()
			if _, err := bundle.sync(root); err != nil {
				t.Fatalf("sync: %v", err)
			}
			dir := filepath.Join(root, filepath.FromSlash(bundle.relDir))
			var got []string
			err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if entry.IsDir() {
					if info.Mode().Perm() != 0o755 {
						t.Errorf("%s mode = %v, want 0755", path, info.Mode().Perm())
					}
					return nil
				}
				if info.Mode().Perm() != 0o644 {
					t.Errorf("%s mode = %v, want 0644", path, info.Mode().Perm())
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
	dir := filepath.Join(root, filepath.FromSlash(bundle.relDir))
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
	if err != nil || string(after) != string(before) {
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
	if _, err := Sync(root); err != nil {
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
	if _, err := Sync(root); err != nil {
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
	entry := filepath.Join(cacheRoot, "ext", "obsolete")
	if err := os.MkdirAll(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := []byte("artifact")
	if err := os.WriteFile(filepath.Join(entry, "bin"), artifact, 0o755); err != nil {
		t.Fatal(err)
	}
	ready, err := json.Marshal(map[string]any{"inputDigest": "obsolete", "size": len(artifact), "artifact": "bin", "target": "t", "created": time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, "ready.json"), ready, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, ".sdk-fingerprint"), []byte("obsolete-sdk"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	dir := filepath.Join(root, filepath.FromSlash(bundle.relDir))
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
				if data, err := os.ReadFile(filepath.Join(filepath.Dir(dir), sibling.Name(), markerFile)); err == nil && string(data) == string(previous) {
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
	if err != nil || string(after) != string(previous) {
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
	dir := filepath.Join(root, filepath.FromSlash(bundle.relDir))
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
		run     func(root string) error
		removed string
		kept    string
	}{
		"all languages": {EnsureSynced, ".stage-python-crashed.old", ""},
		"one language":  {func(root string) error { return EnsureSyncedLang(root, "python") }, ".stage-python-crashed.old", ".stage-rust-crashed"},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := Sync(root); err != nil {
				t.Fatal(err)
			}
			stateDir := filepath.Join(root, "state", "pigsdk")
			for _, leftover := range []string{operation.removed, operation.kept} {
				if leftover == "" {
					continue
				}
				if err := os.MkdirAll(filepath.Join(stateDir, leftover), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := operation.run(root); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(stateDir, operation.removed)); !os.IsNotExist(err) {
				t.Errorf("stranded %s survived a start with current SDKs: %v", operation.removed, err)
			}
			if operation.kept != "" {
				if _, err := os.Stat(filepath.Join(stateDir, operation.kept)); err != nil {
					t.Errorf("start for one language removed another language's tree: %v", err)
				}
			}
		})
	}
}
