package plugins

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Not a port of a case of upstream's tests, cases of its behavior (the review of this slice ran them on Pi's `PluginWatcher`):
// an entry on the way to base code created, removed or moved renews the watches under it only when it is another directory
// than the one watched (`#replaced`: another inode or birth time), never for one moved away and back or for a change of its
// attributes. The events of the steps wait in the watcher while the test holds the host, so the watcher finds the directory
// as the last step left it, as Pi's callbacks do (they run after the synchronous steps of a test).

// A recorded file edited through a hardlink elsewhere raises no event in its directory's watch. The directory moved away and
// back is the one watched: nothing is renewed or compared, so the edit stays unseen (Pi: `[false, false]`), and the watch
// still sees a later write in place.
func TestTheBaseMarkDoesNotRenewTheWatchesOfADirectoryMovedAwayAndBackWhichIsTheSameOne(t *testing.T) {
	_, f := based(t, nil)
	top := realOf(t, tempRoot(t))
	dir := filepath.Join(top, "d")
	file := filepath.Join(dir, "entry.ts")
	writeFiles(t, dir, map[string]string{"entry.ts": "A"})
	f.recordBase(BaseInputs{file: hashOf("A")})
	f.settled()
	if err := os.Link(file, filepath.Join(top, "g.ts")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(top, "g.ts"), "B")
	f.settled()
	if f.marks().Base {
		t.Fatal("base set by an edit through a hardlink, which no watch of the directory sees")
	}
	f.run(func() {
		if err := os.Rename(dir, filepath.Join(top, "x")); err != nil {
			t.Error(err)
		}
		if err := os.Rename(filepath.Join(top, "x"), dir); err != nil {
			t.Error(err)
		}
	})
	f.settled()
	if f.marks().Base {
		t.Fatal("base set: the directory moved away and back, the same one, was renewed and its files compared")
	}
	write(t, file, "C")
	f.baseIs(true)
}

// A directory moved away and another made in its place is another directory: its watches are renewed and its files compared,
// so an edit in the new one is seen (Pi: `[false, true]`).
func TestTheBaseMarkRenewsTheWatchesOfADirectoryReplacedByAnotherOfTheSameName(t *testing.T) {
	_, f := based(t, nil)
	top := realOf(t, tempRoot(t))
	dir := filepath.Join(top, "d")
	file := filepath.Join(dir, "entry.ts")
	writeFiles(t, dir, map[string]string{"entry.ts": "A"})
	f.recordBase(BaseInputs{file: hashOf("A")})
	f.settled()
	f.run(func() {
		if err := os.Rename(dir, filepath.Join(top, "old")); err != nil {
			t.Error(err)
		}
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(file, []byte("A"), 0o644); err != nil {
			t.Error(err)
		}
	})
	f.settled()
	if f.marks().Base {
		t.Fatal("base set by a directory made again with the same bytes")
	}
	write(t, file, "B")
	f.baseIs(true)
}

// The identity of a directory is its inode and birth time, never a followed link: the one at a path is the same after the
// directory is moved away and back or its attributes change, another when another directory is made at its path, and none when there is
// no entry.
func TestIdentityOfADirectoryIsItsInodeAndBirthTimeAndNoneForNoEntry(t *testing.T) {
	top := realOf(t, tempRoot(t))
	dir := filepath.Join(top, "d")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	first := identityOf(dir)
	if !first.present || first.ino == 0 {
		t.Fatalf("identity of an existing directory = %+v", first)
	}
	if got := identityOf(filepath.Join(top, "missing")); got != (dirIdentity{}) {
		t.Fatalf("identity of no entry = %+v, want none", got)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := identityOf(dir); got != first {
		t.Errorf("identity after a change of attributes = %+v, was %+v", got, first)
	}
	moved := filepath.Join(top, "x")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if got := identityOf(dir); got != (dirIdentity{}) {
		t.Errorf("identity of the path the directory left = %+v, want none", got)
	}
	if got := identityOf(moved); got != first {
		t.Errorf("identity at the new path = %+v, was %+v", got, first)
	}
	if err := os.Rename(moved, dir); err != nil {
		t.Fatal(err)
	}
	if got := identityOf(dir); got != first {
		t.Errorf("identity after moved away and back = %+v, was %+v", got, first)
	}
	// The old one stays, so the new one cannot have its inode.
	if err := os.Rename(dir, filepath.Join(top, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := identityOf(dir); got == first || !got.present {
		t.Errorf("identity of another directory at the path = %+v, was %+v", got, first)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(top, "link")
		if err := os.Symlink(dir, link); err != nil {
			t.Fatal(err)
		}
		if got := identityOf(link); !got.present || got == identityOf(dir) {
			t.Errorf("identity of a link = %+v, the target's is %+v: a link is not followed", got, identityOf(dir))
		}
	}
}
