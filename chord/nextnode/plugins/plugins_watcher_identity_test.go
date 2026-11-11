package plugins

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/fsnotify/fsnotify"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Not a port of a case of upstream's tests, cases of its behavior (the review of this slice ran them on Pi's `PluginWatcher`):
// an entry on the way to base code created, removed or moved renews the watches under it only when it is another directory
// than the one watched (`#replaced`: another inode or birth time), never for one moved away and back or for a change of its
// attributes. The events of the steps wait in the watcher while the test holds the host, so the watcher finds the directory
// as the last step left it, as Pi's callbacks do (they run after the synchronous steps of a test).

// seesHardlinkEdits reports whether the platform's watch of a file sees a write through another hardlink of it: kqueue's does (it
// watches the file itself). inotify and ReadDirectoryChangesW (Linux, Windows) report a change only in the directory of the
// name written, so the edit stays unseen. [INFERENCE] for Windows: not run there; Pi's own tests have no hardlink case, and this
// is the review's scenario run on Pi on Linux and macOS.
func seesHardlinkEdits() bool {
	switch runtime.GOOS {
	case "darwin", "freebsd", "netbsd", "openbsd", "dragonfly":
		return true
	}
	return false
}

// A recorded file edited through a hardlink elsewhere raises no event in its directory's watch. The directory moved away and
// back is the one watched: nothing is renewed or compared, so the edit stays unseen (Pi: `[false, false]`), and the watch
// still sees a later write in place.
func TestTheBaseMarkDoesNotRenewTheWatchesOfADirectoryMovedAwayAndBackWhichIsTheSameOne(t *testing.T) {
	_, f := based(t, nil)
	top := realOf(t, tempRoot(t))
	dir := filepath.Join(top, "d")
	file := filepath.Join(dir, "entry.ts")
	writeFiles(t, dir, map[string]string{"entry.ts": "A"})
	f.recordBase(model.BaseInputs{file: hashOf("A")})
	f.settled()
	if err := os.Link(file, filepath.Join(top, "g.ts")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(top, "g.ts"), "B")
	f.settled()
	if seesHardlinkEdits() {
		// kqueue (Pi on macOS gives `[true, true]`) sees an edit through a hardlink as a change of the watched file: the base
		// is set at once, and the rest of this case has nothing left to show (the next test shows it without the link).
		if !f.marks().Base {
			t.Fatal("base not set by an edit through a hardlink, which the watch of the file sees where it is kqueue")
		}
		return
	}
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

// The same without the link, on every platform: the directory moved away and back is the one watched, so its watches go on
// (kqueue drops the watch of a renamed directory, `keepWatches` puts it back) and a write in place is seen, with no base set
// by the move itself.
func TestTheBaseMarkIsNotSetByADirectoryMovedAwayAndBackAndItsWatchesGoOn(t *testing.T) {
	_, f := based(t, nil)
	top := realOf(t, tempRoot(t))
	dir := filepath.Join(top, "d")
	file := filepath.Join(dir, "entry.ts")
	writeFiles(t, dir, map[string]string{"entry.ts": "A"})
	f.recordBase(model.BaseInputs{file: hashOf("A")})
	f.settled()
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

// A base directory (a tree watch, not one held alone) moved away and back in one burst keeps every watch of its tree: kqueue
// drops the watch of a renamed directory, and `keepWatches` puts back the ones of the tree. An edit of a file in the directory
// itself and one in its subdirectory are seen afterwards, and so is a new file (the directory's own watch).
func TestTheBaseMarkKeepsTheTreeWatchesOfABaseDirectoryMovedAwayAndBackInOneBurst(t *testing.T) {
	parent := realOf(t, tempRoot(t))
	base := filepath.Join(parent, "app")
	writeFiles(t, base, map[string]string{"core.ts": "v1", "sub/inner.ts": "inner"})
	f := watcherOver(t, nil, base)
	f.settled()
	f.run(func() {
		if err := os.Rename(base, filepath.Join(parent, "moved")); err != nil {
			t.Error(err)
		}
		if err := os.Rename(filepath.Join(parent, "moved"), base); err != nil {
			t.Error(err)
		}
	})
	f.settled()
	if f.marks().Base {
		t.Fatal("base set by the move away and back")
	}
	write(t, filepath.Join(base, "sub", "inner.ts"), "edited")
	f.baseIs(true)
	write(t, filepath.Join(base, "sub", "inner.ts"), "inner")
	f.baseIs(false)
	write(t, filepath.Join(base, "core.ts"), "v2")
	f.baseIs(true)
	write(t, filepath.Join(base, "core.ts"), "v1")
	f.baseIs(false)
	write(t, filepath.Join(base, "new.ts"), "x")
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
	f.recordBase(model.BaseInputs{file: hashOf("A")})
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

// loseWatch has the watcher hold no watch of dir, as when fsnotify ends the watch of a directory: inotify's ends with the removal
// of its directory, kqueue's with a rename of it. It runs in a section.
func (f *watcherFixture) loseWatch(dir string) {
	f.t.Helper()
	_ = f.watcher.fs.Remove(dir) // an error: the watch has ended already
	if slices.Contains(f.watcher.fs.WatchList(), dir) {
		f.t.Errorf("the watcher still holds a watch of %s", dir)
	}
}

// An event of an ancestor (here a late one: the creation of the directory the case works in) has `keepWatches` put back the
// watches that ended under it. One of them ended with its directory's removal and cannot be put back while the directory is
// missing; it must leave nothing that stands for it. The directory made again can have the identity of the one removed (a file
// system that reuses the inode and stamps the birth time in coarse ticks: ext4 gave it exactly that, in the failures of
// TestTheBaseMarkRenewsTheWatchOfAnAncestorRemovedAndMadeAgainWhileTheRecordedDirectoryIsMissing); moved away and back it has it
// for certain. It is then no other directory than the one watched, and with a record of its identity and no watch, nothing
// renews it: the files under it are never watched again.
func TestTheBaseMarkWatchesADirectoryBackWhoseWatchCouldNotBePutBackWhileItWasAway(t *testing.T) {
	_, f := based(t, nil)
	top := realOf(t, tempRoot(t))
	dir := filepath.Join(top, "d")
	file := filepath.Join(dir, "sub", "entry.ts")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.recordBase(model.BaseInputs{file: hashOf("as built")})
	f.baseIs(true)
	f.run(func() {
		away := filepath.Join(top, "away")
		if err := os.Rename(dir, away); err != nil {
			t.Error(err)
		}
		f.loseWatch(dir)
		f.watcher.event(f.watcher.fs, fsnotify.Event{Name: top, Op: fsnotify.Create})
		if _, watched := f.watcher.baseWatched[dir]; watched {
			t.Errorf("%s is watched, though it is missing", dir)
		}
		if _, known := f.watcher.identities[dir]; known {
			t.Errorf("the watcher holds the identity of %s, which it does not watch", dir)
		}
		if err := os.Rename(away, dir); err != nil {
			t.Error(err)
		}
	})
	f.settled()
	writeFiles(t, filepath.Join(dir, "sub"), map[string]string{"entry.ts": "as built"})
	f.baseIs(false)
	write(t, file, "edited")
	f.baseIs(true)
}

// The same for a base directory, a tree: the watches of its directories that ended while it was away cannot be put back, and
// are put back when it is back, whatever its identity then.
func TestTheBaseMarkWatchesTheTreeOfABaseDirectoryBackWhoseWatchesCouldNotBePutBackWhileItWasAway(t *testing.T) {
	parent := realOf(t, tempRoot(t))
	base := filepath.Join(parent, "app")
	writeFiles(t, base, map[string]string{"core.ts": "v1", "sub/inner.ts": "inner"})
	f := watcherOver(t, nil, base)
	f.settled()
	f.run(func() {
		away := filepath.Join(parent, "away")
		if err := os.Rename(base, away); err != nil {
			t.Error(err)
		}
		f.loseWatch(base)
		f.loseWatch(filepath.Join(base, "sub"))
		f.watcher.event(f.watcher.fs, fsnotify.Event{Name: parent, Op: fsnotify.Create})
		for _, dir := range []string{base, filepath.Join(base, "sub")} {
			if _, watched := f.watcher.watched[dir]; watched {
				t.Errorf("%s is watched, though it is missing", dir)
			}
			if _, known := f.watcher.identities[dir]; known {
				t.Errorf("the watcher holds the identity of %s, which it does not watch", dir)
			}
		}
		if err := os.Rename(away, base); err != nil {
			t.Error(err)
		}
	})
	f.settled()
	if f.marks().Base {
		t.Fatal("base set by the move away and back")
	}
	write(t, filepath.Join(base, "sub", "inner.ts"), "edited")
	f.baseIs(true)
	write(t, filepath.Join(base, "sub", "inner.ts"), "inner")
	f.baseIs(false)
	write(t, filepath.Join(base, "core.ts"), "v2")
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
}
