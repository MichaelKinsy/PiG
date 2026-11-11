package app

// pi: packages/chord/src/node/app/blobs.ts

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
)

// Ports packages/chord/test/node/app/blobs.test.ts: a host's blob files on their own (decision "Files v2"): written
// whole and moved into place (mode 0600), read by range, removed, swept when the index does not know them, and put into
// another host's directory as a link.

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	sort.Strings(out)
	return out
}

func TestBlobFilesWriteThroughTheirStagingFileReadRangesRemove(t *testing.T) {
	base := t.TempDir()
	blobs := NewBlobs(filepath.Join(base, "blobs"))
	if err := blobs.Write("b1", []byte("hello blob"), filepath.Join(base, "staging", "b1")); err != nil {
		t.Fatal(err)
	}
	if got := names(t, filepath.Join(base, "staging")); len(got) != 0 {
		t.Fatalf("staging = %v", got)
	}
	if mode := modeOf(t, blobs.Path("b1")); mode != 0o600 {
		t.Fatalf("mode = %o", mode)
	}
	if got, err := blobs.Read("b1", 10, 6, 100); err != nil || string(got) != "blob" {
		t.Fatalf("Read = %q, %v", got, err)
	}
	if got, err := blobs.Read("b1", 10, 20, 5); err != nil || len(got) != 0 {
		t.Fatalf("Read past the end = %q, %v", got, err)
	}
	if err := blobs.Remove([]string{"b1", "missing"}); err != nil {
		t.Fatal(err)
	}
	if got := names(t, filepath.Join(base, "blobs")); len(got) != 0 {
		t.Fatalf("blobs = %v", got)
	}
}

func TestBlobFilesSweepTheOnesTheIndexDoesNotKnow(t *testing.T) {
	base := t.TempDir()
	blobs := NewBlobs(filepath.Join(base, "blobs"))
	if err := blobs.Prepare(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"kept", "stray"} {
		if err := os.WriteFile(blobs.Path(id), []byte(id), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := blobs.Sweep(func(id string) (bool, error) { return id == "kept", nil }); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(t, filepath.Join(base, "blobs")), ","); got != "kept" {
		t.Fatalf("blobs = %v", got)
	}
	// No directory yet: nothing to sweep.
	if err := NewBlobs(filepath.Join(base, "none")).Sweep(func(string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
}

func TestBlobFilesPutABlobIntoAnotherHostsDirectoryAsAHardLinkNeverOverAnExistingOne(t *testing.T) {
	base := t.TempDir()
	blobs := NewBlobs(filepath.Join(base, "a"))
	if err := blobs.Write("b1", []byte("shared"), filepath.Join(base, "staging")); err != nil {
		t.Fatal(err)
	}
	target := NewBlobs(filepath.Join(base, "b"))
	if err := target.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := blobs.LinkInto("b1", target.Dir); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(target.Path("b1")); string(raw) != "shared" {
		t.Fatalf("linked blob = %q", raw)
	}
	a, _ := os.Stat(blobs.Path("b1"))
	b, _ := os.Stat(target.Path("b1"))
	if !os.SameFile(a, b) {
		t.Fatal("the target is not a hard link of the blob")
	}
	if err := blobs.LinkInto("b1", target.Dir); err == nil || !os.IsExist(err) && !isEEXIST(err) {
		t.Fatalf("second LinkInto = %v, want EEXIST", err)
	}
}

func isEEXIST(err error) bool {
	for err != nil {
		if errno, ok := errors.AsType[syscall.Errno](err); ok {
			return errno == syscall.EEXIST
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}
