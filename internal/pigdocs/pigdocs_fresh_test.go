package pigdocs

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

// snapshotTree records every file's bytes and permissions and every directory's permissions.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if entry.IsDir() {
			tree[rel+"/"] = info.Mode().Perm().String()
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		tree[rel] = info.Mode().Perm().String() + " " + string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// A first start stages the whole docs tree and renames it into place. The result is the tree the per-file path writes into an existing directory, with no staging directory left beside it.
func TestSyncIntoMissingDirMatchesSyncIntoExistingDir(t *testing.T) {
	fresh := t.TempDir()
	written, err := Sync(fresh)
	if err != nil {
		t.Fatalf("Sync into a missing docs directory: %v", err)
	}
	existing := t.TempDir()
	if err := os.MkdirAll(DocsDir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	wantWritten, err := Sync(existing)
	if err != nil {
		t.Fatalf("Sync into an existing docs directory: %v", err)
	}
	if !slices.Equal(written, wantWritten) || !slices.IsSorted(written) {
		t.Fatalf("written = %v, want sorted %v", written, wantWritten)
	}
	got, want := snapshotTree(t, DocsDir(fresh)), snapshotTree(t, DocsDir(existing))
	if len(got) == 0 || len(got) != len(want) {
		t.Fatalf("fresh tree has %d entries, existing-dir tree has %d", len(got), len(want))
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s differs between the fresh and existing-directory syncs", name)
		}
	}
	siblings, err := os.ReadDir(fresh)
	if err != nil {
		t.Fatal(err)
	}
	for _, sibling := range siblings {
		if strings.HasPrefix(sibling.Name(), ".docs-stage-") {
			t.Errorf("staging directory %s survived", sibling.Name())
		}
	}
}

// When the config root itself does not exist yet, the first sync creates it.
func TestSyncCreatesMissingConfigRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "config")
	if _, err := Sync(root); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(DocsDir(root), markerFile))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := contentDigest()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(marker), []byte(digest)) {
		t.Fatalf("marker = %q, want %q", marker, digest)
	}
}

// A first sync that died before its rename strands a full docs tree. The next sync removes it, on the fresh path and on the existing-directory path, so repeated first-start crashes do not accumulate trees.
func TestSyncRemovesStagingTreesStrandedByDeadSyncs(t *testing.T) {
	for _, name := range []string{"missing docs dir", "existing docs dir"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if name == "existing docs dir" {
				if err := os.MkdirAll(DocsDir(root), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, stranded := range []string{".docs-stage-1111", ".docs-stage-2222"} {
				if err := os.MkdirAll(filepath.Join(root, stranded), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, stranded, "partial.md"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Sync(root); err != nil {
				t.Fatal(err)
			}
			if err := EnsureSynced(root); err != nil {
				t.Fatal(err)
			}
			siblings, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, sibling := range siblings {
				if strings.HasPrefix(sibling.Name(), stagingPrefix) {
					t.Errorf("stranded staging tree %s survived Sync", sibling.Name())
				}
			}
		})
	}
}

// Startup sync is best-effort and bounded. Another process holding the staging lock (a live first sync) must not stall this one: it writes the docs itself, leaves the holder's staging tree alone, and returns while the lock is still held.
func TestEnsureSyncedDoesNotWaitForStagingLock(t *testing.T) {
	root := t.TempDir()
	holder := flock.New(filepath.Join(root, stagingLockFile))
	if err := holder.Lock(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Unlock() })
	live := filepath.Join(root, stagingPrefix+"live")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- EnsureSynced(root) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("EnsureSynced is blocked on the staging lock held by another process")
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("staging tree of the lock holder was removed: %v", err)
	}
	digest, err := contentDigest()
	if err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(DocsDir(root), markerFile))
	if err != nil || strings.TrimSpace(string(marker)) != digest {
		t.Fatalf("docs were not synced while the lock was held: marker %q, err %v", marker, err)
	}
}
