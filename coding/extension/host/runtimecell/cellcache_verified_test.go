package runtimecell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const verifiedDigest = "feedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedface"

// verifiedEntry publishes an entry whose canonical artifact is "marker" and whose other content, data/file, only verify vouches for.
type verifiedEntry struct {
	finalDir string
	builds   int
}

func newVerifiedEntry(t *testing.T) *verifiedEntry {
	t.Helper()
	return &verifiedEntry{finalDir: filepath.Join(t.TempDir(), "cells", "node", verifiedDigest)}
}

func (e *verifiedEntry) verify(dir string) error {
	body, err := os.ReadFile(filepath.Join(dir, "data", "file"))
	if err != nil {
		return fmt.Errorf("missing: data/file: %w", err)
	}
	if string(body) != "good" {
		return errors.New("changed: data/file")
	}
	return nil
}

func (e *verifiedEntry) publish(ctx context.Context) (PublishedEntry, error) {
	return PublishVerifiedArtifact(ctx, e.finalDir, "marker", verifiedDigest, "node", e.verify, func(scratch string) (string, error) {
		e.builds++
		if err := os.MkdirAll(filepath.Join(scratch, "data"), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(scratch, "data", "file"), []byte("good"), 0o644); err != nil {
			return "", err
		}
		marker := filepath.Join(scratch, "marker")
		return marker, os.WriteFile(marker, []byte(verifiedDigest), 0o644)
	}, "data")
}

func (e *verifiedEntry) beside(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, name := range dirNames(t, filepath.Dir(e.finalDir)) {
		if strings.HasPrefix(name, ".build-") {
			names = append(names, name)
		}
	}
	return names
}

// A ready entry whose content ready.json does not vouch for is checked wherever an entry would be reused: a clean one is reused as it is, a differing one is replaced and the difference is reported.
func TestPublishVerifiedArtifactReplacesAReadyEntryThatDiffers(t *testing.T) {
	e := newVerifiedEntry(t)
	first, err := e.publish(t.Context())
	if err != nil || first.Reused || first.Replaced != nil || e.builds != 1 {
		t.Fatalf("first publish = %+v, %v after %d builds", first, err, e.builds)
	}
	before, err := os.Stat(e.finalDir)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := e.publish(t.Context())
	if err != nil || !clean.Reused || clean.Replaced != nil || e.builds != 1 {
		t.Fatalf("publish of a clean entry = %+v, %v after %d builds; want it reused", clean, err, e.builds)
	}
	if now, err := os.Stat(e.finalDir); err != nil || !os.SameFile(before, now) {
		t.Fatalf("a clean entry was replaced: %v", err)
	}

	if err := os.WriteFile(filepath.Join(e.finalDir, "data", "file"), []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	repaired, err := e.publish(t.Context())
	if err != nil || repaired.Reused || e.builds != 2 {
		t.Fatalf("publish of a differing entry = %+v, %v after %d builds; want a new build", repaired, err, e.builds)
	}
	if repaired.Replaced == nil || !strings.Contains(repaired.Replaced.Error(), "changed: data/file") {
		t.Fatalf("Replaced = %v, want the difference", repaired.Replaced)
	}
	if body, err := os.ReadFile(filepath.Join(e.finalDir, "data", "file")); err != nil || string(body) != "good" {
		t.Fatalf("data/file = %q, %v after the replacement", body, err)
	}
	if left := e.beside(t); len(left) != 0 {
		t.Fatalf("the replacement left %v beside the entry", left)
	}
	if again, err := e.publish(t.Context()); err != nil || !again.Reused || again.Replaced != nil {
		t.Fatalf("publish after the replacement = %+v, %v", again, err)
	}
}

// A ready.json that vouches for an entry whose content differs does not stop the replacement; and a replacement is not announced for an entry that is not ready at all, which is a first publication or a leftover.
func TestPublishVerifiedArtifactDoesNotTrustReadyJSON(t *testing.T) {
	e := newVerifiedEntry(t)
	if _, err := e.publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.finalDir, "data", "file"), []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.finalDir, cellReadyFile)); err != nil {
		t.Fatal(err)
	}
	entry, err := e.publish(t.Context())
	if err != nil || entry.Reused || entry.Replaced == nil {
		t.Fatalf("publish of an unready, differing entry = %+v, %v; want it replaced and the difference reported", entry, err)
	}
	if err := os.Remove(filepath.Join(e.finalDir, cellReadyFile)); err != nil {
		t.Fatal(err)
	}
	entry, err = e.publish(t.Context())
	if err != nil || entry.Reused || entry.Replaced != nil {
		t.Fatalf("publish of an unready entry with the right content = %+v, %v; want it replaced without a difference", entry, err)
	}
}

// A crash between the two renames of a replacement leaves the old entry aside and no entry at the final name; a crash after them leaves the old entry aside beside a good one. The next publication, which holds the entry's lock, publishes the entry and removes what the replacement left, and only that: another entry's aside is not its to remove.
func TestPublishVerifiedArtifactCleansWhatACrashedReplacementLeft(t *testing.T) {
	e := newVerifiedEntry(t)
	if _, err := e.publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(e.finalDir)
	own := filepath.Join(parent, replacedEntryPrefix+"0123456789abcdef-"+verifiedDigest)
	other := filepath.Join(parent, replacedEntryPrefix+"0123456789abcdef-"+"c"+verifiedDigest)
	if err := os.Rename(e.finalDir, own); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(other, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	entry, err := e.publish(t.Context())
	if err != nil || entry.Reused || entry.Replaced != nil || e.builds != 2 {
		t.Fatalf("publish after a crash between the renames = %+v, %v after %d builds", entry, err, e.builds)
	}
	if _, err := os.Lstat(own); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the entry's own aside is still there: %v", err)
	}
	if _, err := os.Lstat(other); err != nil {
		t.Errorf("another entry's aside was removed: %v", err)
	}
	if err := e.verify(e.finalDir); err != nil {
		t.Errorf("published entry: %v", err)
	}
	// An aside left beside a good entry is removed by the next replacement.
	if err := os.MkdirAll(filepath.Join(own, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.finalDir, "data", "file"), []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(own); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the stale aside survived a replacement: %v", err)
	}
}

// The rename that moves the old entry aside goes through the publishing retry: a transient refusal (Windows while a scanner holds a file) is retried, and a refusal that lasts (Windows while a running program holds the tree) fails the publication with the entry's path named and the old entry in place, as it was.
func TestPublishVerifiedArtifactMovesTheOldEntryAsideWithTheRetry(t *testing.T) {
	denied := &os.LinkError{Op: "rename", Err: syscall.EACCES}
	restore := publishRename
	t.Cleanup(func() { publishRename = restore })
	tamper := func(t *testing.T, e *verifiedEntry) {
		t.Helper()
		if _, err := e.publish(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(e.finalDir, "data", "file"), []byte("evil"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("transient", func(t *testing.T) {
		e := newVerifiedEntry(t)
		tamper(t, e)
		failures := 0
		publishRename = renameRetry{
			rename: func(oldpath, newpath string) error {
				if oldpath == e.finalDir && failures < 3 {
					failures++
					return denied
				}
				return os.Rename(oldpath, newpath)
			},
			retryable: func(error) bool { return true },
			wait:      func(context.Context, time.Duration) error { return nil },
		}
		entry, err := e.publish(t.Context())
		if err != nil || entry.Replaced == nil || failures != 3 {
			t.Fatalf("publish = %+v, %v after %d refusals; want the entry replaced after the retries", entry, err, failures)
		}
		if err := e.verify(e.finalDir); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("lasting", func(t *testing.T) {
		e := newVerifiedEntry(t)
		tamper(t, e)
		publishRename = renameRetry{
			rename: func(oldpath, newpath string) error {
				if oldpath == e.finalDir {
					return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EACCES}
				}
				return os.Rename(oldpath, newpath)
			},
			retryable: func(error) bool { return false },
			wait:      func(context.Context, time.Duration) error { return nil },
		}
		_, err := e.publish(t.Context())
		if !errors.Is(err, syscall.EACCES) || !strings.Contains(err.Error(), e.finalDir) {
			t.Fatalf("publish = %v; want the refusal naming %s", err, e.finalDir)
		}
		if body, err := os.ReadFile(filepath.Join(e.finalDir, "data", "file")); err != nil || string(body) != "evil" {
			t.Fatalf("the old entry was changed by the refused replacement: %q, %v", body, err)
		}
		if left := e.beside(t); len(left) != 0 {
			t.Fatalf("the refused replacement left %v beside the entry", left)
		}
	})
}
