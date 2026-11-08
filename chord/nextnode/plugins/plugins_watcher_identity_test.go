package plugins

import (
	"os"
	"path/filepath"
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
