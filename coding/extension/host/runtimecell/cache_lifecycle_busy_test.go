package runtimecell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// An entry whose files the file system reports busy is skipped and counted, not an error: NFS renames a file
// that is open elsewhere to `.nfs*` and answers EBUSY when it is removed, and Windows refuses to remove a file
// another process holds open. The entry is already renamed to a tombstone, so the next run retries it.
func TestCacheLifecycleBusyEntriesAreSkippedNotErrors(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	busy := &os.PathError{Op: "unlink", Path: ".nfs0000000000000001", Err: syscall.EBUSY}
	for name, options := range map[string]func(string) CacheLifecycleOptions{
		"EBUSY while removing the tombstone": func(root string) CacheLifecycleOptions {
			return CacheLifecycleOptions{CacheRoot: root, RemoveAll: func(string) error { return busy }}
		},
		"EBUSY while renaming": func(root string) CacheLifecycleOptions {
			return CacheLifecycleOptions{CacheRoot: root, Rename: func(string, string) error { return busy }}
		},
		"a joined error whose leaves are all busy": func(root string) CacheLifecycleOptions {
			return CacheLifecycleOptions{CacheRoot: root, RemoveAll: func(string) error {
				return fmt.Errorf("remove: %w", errors.Join(busy, &os.PathError{Op: "unlink", Path: "x", Err: syscall.ETXTBSY}))
			}}
		},
		"a .nfs file whatever the error number": func(root string) CacheLifecycleOptions {
			return CacheLifecycleOptions{CacheRoot: root, RemoveAll: func(string) error {
				return &os.PathError{Op: "unlink", Path: filepath.Join(root, "ext", ".nfs00ab"), Err: syscall.EIO}
			}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeLifecycleEntry(t, root, "ext", "expired", now.Add(-31*24*time.Hour))
			opts := options(root)
			opts.Now = func() time.Time { return now }
			report, err := PruneCaches(opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Errors) != 0 || report.Skipped != 1 || report.Removed != 0 {
				t.Fatalf("report = %#v, want one skipped entry and no errors", report)
			}
		})
	}
}

func TestCacheLifecycleOtherErrorsStayErrors(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	root := t.TempDir()
	writeLifecycleEntry(t, root, "ext", "expired", now.Add(-31*24*time.Hour))
	report, err := PruneCaches(CacheLifecycleOptions{
		CacheRoot: root, Now: func() time.Time { return now },
		RemoveAll: func(string) error {
			return errors.Join(&os.PathError{Op: "unlink", Path: "a", Err: syscall.EBUSY}, &os.PathError{Op: "unlink", Path: "b", Err: syscall.EACCES})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Errors) != 1 || report.Skipped != 0 {
		t.Fatalf("report = %#v, want a real error for a non-busy leaf", report)
	}
}

// An entry an extension leases after the collection classified it is skipped and counted. It is not an
// error, so it does not stop a collection from recording that it finished. Entry bbbb is classified
// expired; the lease arrives while aaaa's tombstone is being removed, before the collection reaches bbbb.
func TestCacheLifecycleEntryLeasedAfterClassificationIsSkippedNotAnError(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	root := t.TempDir()
	first := writeLifecycleEntry(t, root, "ext", "aaaa", now.Add(-31*24*time.Hour))
	second := writeLifecycleEntry(t, root, "ext", "bbbb", now.Add(-31*24*time.Hour))
	var lease *UsageLease
	defer func() { _ = lease.Release() }()
	report, err := PruneCaches(CacheLifecycleOptions{
		CacheRoot: root, Now: func() time.Time { return now },
		RemoveAll: func(path string) error {
			var leaseErr error
			if lease, leaseErr = AcquireUsageLease(second); leaseErr != nil {
				return leaseErr
			}
			return os.RemoveAll(path)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(second); statErr != nil {
		t.Fatalf("leased entry was removed: %v", statErr)
	}
	if _, statErr := os.Stat(first); !os.IsNotExist(statErr) {
		t.Fatalf("unleased entry survived: %v", statErr)
	}
	if len(report.Errors) != 0 || report.Removed != 1 || report.Skipped != 1 {
		t.Fatalf("report = %#v, want no errors, one removed and one skipped entry", report)
	}
}

// A collection bound to a context stops when it is cancelled, between entries and inside an entry's tree,
// and reports the cancellation instead of finishing silently.
func TestCacheLifecycleStopsWhenItsContextIsCancelled(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	root := t.TempDir()
	var entries []string
	for _, name := range []string{"aaaa", "bbbb", "cccc"} {
		entries = append(entries, writeLifecycleEntry(t, root, "ext", name, now.Add(-31*24*time.Hour)))
	}
	ctx, cancel := context.WithCancel(context.Background())
	renames := 0
	report, err := PruneCaches(CacheLifecycleOptions{
		Context: ctx, CacheRoot: root, Now: func() time.Time { return now },
		Rename: func(source, target string) error {
			renames++
			cancel()
			return os.Rename(source, target)
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if renames != 1 {
		t.Fatalf("renamed %d entries after cancellation, want 1", renames)
	}
	kept := 0
	for _, entry := range entries {
		if _, statErr := os.Stat(entry); statErr == nil {
			kept++
		}
	}
	if kept != 2 || report.Removed > 1 {
		t.Fatalf("kept %d entries, report = %#v", kept, report)
	}
}

func TestCacheLifecycleCancelledBeforeStartTouchesNothing(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	root := t.TempDir()
	entry := writeLifecycleEntry(t, root, "ext", "expired", now.Add(-31*24*time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PruneCaches(CacheLifecycleOptions{Context: ctx, CacheRoot: root, Now: func() time.Time { return now }})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(entry); statErr != nil {
		t.Fatalf("entry removed by a cancelled collection: %v", statErr)
	}
}

// The default tree removal under a context keeps removing what it can past a busy file and reports only
// the busy leaf, so one `.nfs*` file does not hide the rest of the entry's data from the collection.
func TestRemoveTreeContextContinuesPastBusyFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tree")
	for _, p := range []string{"a/one", "a/two", "b/three"} {
		path := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeTreeContext(context.Background(), root, func(path string) error {
		if filepath.Base(path) == "one" {
			return &os.PathError{Op: "unlink", Path: path, Err: syscall.EBUSY}
		}
		return os.Remove(path)
	}); err == nil || !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("err = %v, want EBUSY", err)
	}
	for _, gone := range []string{"a/two", "b/three", "b"} {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(gone))); !os.IsNotExist(err) {
			t.Errorf("%s survived: %v", gone, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "a", "one")); err != nil {
		t.Errorf("busy file was removed: %v", err)
	}
}

// NFS removes a file another process on the same client holds open by renaming it to `.nfs*`, and unlink reports
// success. The collection's own removal therefore leaves the `.nfs*` file in the directory, and removing the
// directory fails with ENOTEMPTY rather than EBUSY. That directory is busy, not a failure: the collection that
// first meets an open file must skip it and still write its marker, not only the collection after it.
func TestRemoveTreeContextReportsADirectoryHeldByAnNFSSillyRenameAsBusy(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tree")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bin/runner", "metadata.json"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sillyRename := func(path string) error {
		if filepath.Base(path) == "runner" {
			return os.Rename(path, filepath.Join(filepath.Dir(path), ".nfs000000000000002a00000001"))
		}
		return os.Remove(path)
	}
	err := removeTreeContext(context.Background(), root, sillyRename)
	if err == nil {
		t.Fatal("removal of a directory that still holds a .nfs file reported success")
	}
	if !isBusyError(err) {
		t.Fatalf("err = %v, want a busy error", err)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "metadata.json")); !os.IsNotExist(statErr) {
		t.Fatalf("the rest of the entry survived: %v", statErr)
	}

	// A directory that stays non-empty for any other reason is still a failure.
	other := filepath.Join(t.TempDir(), "tree")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "kept"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	keep := func(path string) error {
		if filepath.Base(path) == "kept" {
			return nil
		}
		return os.Remove(path)
	}
	if err := removeTreeContext(context.Background(), other, keep); err == nil || isBusyError(err) {
		t.Fatalf("err = %v, want a failure that is not busy", err)
	}
}

// Only an error whose every failure is busy is busy, however the joined failures are wrapped.
func TestIsBusyErrorLooksThroughWrappedJoins(t *testing.T) {
	busy := &os.PathError{Op: "unlink", Path: ".nfs01", Err: syscall.EBUSY}
	denied := &os.PathError{Op: "unlink", Path: "a", Err: syscall.EACCES}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"busy", busy, true},
		{"wrapped busy", fmt.Errorf("remove: %w", busy), true},
		{"rename of a busy entry", &os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.EBUSY}, true},
		{"join of busy failures", errors.Join(busy, busy), true},
		{"wrapped join of busy failures", fmt.Errorf("remove: %w", errors.Join(busy, busy)), true},
		{"join with a denied failure", errors.Join(busy, denied), false},
		{"wrapped join with a denied failure", fmt.Errorf("remove: %w", errors.Join(busy, denied)), false},
		{"nested join with a denied failure", errors.Join(busy, fmt.Errorf("x: %w", errors.Join(busy, denied))), false},
		{"denied", denied, false},
		{"nil", nil, false},
	} {
		if got := isBusyError(tc.err); got != tc.want {
			t.Errorf("%s: isBusyError(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}
