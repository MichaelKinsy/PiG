package plugins

// pi: packages/chord/src/node/plugins/watcher.ts

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Ports the cases of packages/chord/test/node/plugins/watcher.test.ts "base code compared with what the running bases read
// (p3 step 0a)" and "compares a recorded base file, not its siblings; never under node_modules": `base` is true exactly while
// a file differs from what a running base artifact read, is gone, or is a source file new in a base directory.

// based is a watcher of a base directory holding files (the server's own code, hashed at its start), and no roots.
func based(t *testing.T, files map[string]string) (string, *watcherFixture) {
	t.Helper()
	base := realOf(t, tempRoot(t))
	writeFiles(t, base, files)
	return base, watcherOver(t, nil, base)
}

// baseIs waits until the watcher's `base` mark is value.
func (f *watcherFixture) baseIs(value bool) {
	f.t.Helper()
	eventually(f.t, 5*time.Second, "base to be "+map[bool]string{true: "set", false: "clear"}[value], func() bool { return f.marks().Base == value })
}

// settled lets the watch events and the comparisons of the last writes run: the barrier's events, then the comparisons they
// started.
func (f *watcherFixture) settled() {
	f.t.Helper()
	f.settle()
	f.joinComparisons()
}

// joinComparisons returns once no comparison of a base file is under way.
func (f *watcherFixture) joinComparisons() {
	f.t.Helper()
	eventually(f.t, 5*time.Second, "the comparisons of base files to end", func() bool {
		var idle bool
		f.run(func() { idle = len(f.watcher.comparing) == 0 })
		return idle
	})
}

func (f *watcherFixture) recordBase(inputs BaseInputs) {
	f.run(func() { f.watcher.recordBase(inputs) })
}

func hashOf(text string) string { return ContentHash([]byte(text)) }

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Ports watcher.test.ts "compares a recorded base file, not its siblings; never under node_modules".
func TestTheWatchersMarksComparesARecordedBaseFileNotItsSiblingsNeverUnderNodeModules(t *testing.T) {
	_, f := based(t, nil)
	elsewhere := realOf(t, tempRoot(t))
	writeFiles(t, elsewhere, map[string]string{"entry.ts": "", "sibling.ts": "", "node_modules/react/index.js": ""})
	f.recordBase(BaseInputs{
		filepath.Join(elsewhere, "entry.ts"):                          hashOf(""),
		filepath.Join(elsewhere, "node_modules", "react", "index.js"): hashOf(""),
	})
	write(t, filepath.Join(elsewhere, "sibling.ts"), "export {};")
	write(t, filepath.Join(elsewhere, "node_modules", "react", "index.js"), "export {};")
	f.mark(filepath.Join(elsewhere, "sibling.ts"))
	f.mark(filepath.Join(elsewhere, "node_modules", "react", "index.js"))
	f.settled()
	if f.marks().Base {
		t.Fatal("base set by a sibling or a file under node_modules")
	}
	// A real event on the file: its directory is watched.
	write(t, filepath.Join(elsewhere, "entry.ts"), "export {};")
	f.baseIs(true)
}

// Ports watcher.test.ts "ignores a rewrite with the same bytes; marks a change and clears at its revert".
func TestTheBaseMarkIgnoresARewriteWithTheSameBytesMarksAChangeAndClearsAtItsRevert(t *testing.T) {
	base, f := based(t, map[string]string{"core.ts": "export const a = 1;\n"})
	write(t, filepath.Join(base, "core.ts"), "export const a = 1;\n")
	f.settled()
	if f.marks().Base {
		t.Fatal("base set by a rewrite with the same bytes")
	}
	write(t, filepath.Join(base, "core.ts"), "export const a = 2;\n")
	f.baseIs(true)
	write(t, filepath.Join(base, "core.ts"), "export const a = 1;\n")
	f.baseIs(false)
}

// Ports watcher.test.ts "takes the latest of a burst of writes: a change and its quick revert leave no mark".
func TestTheBaseMarkTakesTheLatestOfABurstOfWritesAChangeAndItsQuickRevertLeaveNoMark(t *testing.T) {
	base, f := based(t, map[string]string{"core.ts": "v1"})
	for round := range 20 {
		write(t, filepath.Join(base, "core.ts"), "v"+string(rune('2'+round%8)))
		f.mark(filepath.Join(base, "core.ts"))
	}
	write(t, filepath.Join(base, "core.ts"), "v1")
	f.mark(filepath.Join(base, "core.ts"))
	f.settled()
	if f.marks().Base {
		t.Fatal("base set after the revert")
	}
}

// Ports watcher.test.ts "ignores a swap file; marks a new source file and clears once it is removed".
func TestTheBaseMarkIgnoresASwapFileMarksANewSourceFileAndClearsOnceItIsRemoved(t *testing.T) {
	base, f := based(t, map[string]string{"core.ts": ""})
	write(t, filepath.Join(base, ".core.ts.swp"), "swap")
	write(t, filepath.Join(base, "notes.md"), "notes")
	f.settled()
	if f.marks().Base {
		t.Fatal("base set by a swap file or a note")
	}
	write(t, filepath.Join(base, "extra.ts"), "export {};")
	f.baseIs(true)
	if err := os.Remove(filepath.Join(base, "extra.ts")); err != nil {
		t.Fatal(err)
	}
	f.baseIs(false)
}

// Ports watcher.test.ts "finds an edit made between a base build and its watch, with no later event".
func TestTheBaseMarkFindsAnEditMadeBetweenABaseBuildAndItsWatchWithNoLaterEvent(t *testing.T) {
	_, f := based(t, nil)
	elsewhere := realOf(t, tempRoot(t))
	writeFiles(t, elsewhere, map[string]string{"entry.ts": "edited after the build"})
	f.recordBase(BaseInputs{filepath.Join(elsewhere, "entry.ts"): hashOf("as built")})
	f.baseIs(true)
}

// A record arriving after the close watches nothing and marks nothing (Pi 284e63d9 watcher.ts recordBase's closed guard).
func TestARecordedBaseAfterTheCloseWatchesNothing(t *testing.T) {
	_, f := based(t, nil)
	elsewhere := realOf(t, tempRoot(t))
	writeFiles(t, elsewhere, map[string]string{"entry.ts": "edited after the build"})
	records := 0
	f.run(func() { records = len(f.watcher.records) })
	f.close()
	f.recordBase(BaseInputs{filepath.Join(elsewhere, "entry.ts"): hashOf("as built")})
	if f.marks().Base {
		t.Fatal("base set by a record that arrived after the close")
	}
	f.run(func() {
		if len(f.watcher.records) != records {
			t.Fatalf("records = %d after the close, want %d", len(f.watcher.records), records)
		}
	})
}

// Ports watcher.test.ts "watches a base directory removed and recreated, and compares its files".
func TestTheBaseMarkWatchesABaseDirectoryRemovedAndRecreatedAndComparesItsFiles(t *testing.T) {
	parent := realOf(t, tempRoot(t))
	base := filepath.Join(parent, "app")
	writeFiles(t, base, map[string]string{"core.ts": "v1", "sub/inner.ts": "inner"})
	f := watcherOver(t, nil, base)
	if err := os.Rename(base, filepath.Join(parent, "moved")); err != nil {
		t.Fatal(err)
	}
	f.baseIs(true)
	if err := os.Rename(filepath.Join(parent, "moved"), base); err != nil {
		t.Fatal(err)
	}
	f.baseIs(false)
	// Watched again: an edit after it came back is seen, in a subdirectory too.
	write(t, filepath.Join(base, "sub", "inner.ts"), "edited")
	f.baseIs(true)
	write(t, filepath.Join(base, "sub", "inner.ts"), "inner")
	f.baseIs(false)
	// A subdirectory removed and made again with its file.
	if err := os.RemoveAll(filepath.Join(base, "sub")); err != nil {
		t.Fatal(err)
	}
	f.baseIs(true)
	if err := os.Mkdir(filepath.Join(base, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(base, "sub", "inner.ts"), "inner")
	f.baseIs(false)
}

// Ports watcher.test.ts "follows an ancestor of a recorded file moved away and its subtree made again: edit, revert".
func TestTheBaseMarkFollowsAnAncestorOfARecordedFileMovedAwayAndItsSubtreeMadeAgainEditRevert(t *testing.T) {
	_, f := based(t, nil)
	root := realOf(t, tempRoot(t))
	file := filepath.Join(root, "parent", "child", "entry.ts")
	writeFiles(t, filepath.Join(root, "parent", "child"), map[string]string{"entry.ts": "v1"})
	f.recordBase(BaseInputs{file: hashOf("v1")})
	f.settled()
	if f.marks().Base {
		t.Fatal("base set at the record")
	}
	if err := os.Rename(filepath.Join(root, "parent"), filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	f.baseIs(true)
	writeFiles(t, filepath.Join(root, "parent", "child"), map[string]string{"entry.ts": "v2"})
	f.settled()
	if !f.marks().Base {
		t.Fatal("base cleared by the file made again with other bytes")
	}
	write(t, file, "v1")
	f.baseIs(false)
	write(t, file, "v3")
	f.baseIs(true)
	write(t, file, "v1")
	f.baseIs(false)
}

// Ports watcher.test.ts "renews the watch of an ancestor removed and made again while the recorded directory is missing".
func TestTheBaseMarkRenewsTheWatchOfAnAncestorRemovedAndMadeAgainWhileTheRecordedDirectoryIsMissing(t *testing.T) {
	_, f := based(t, nil)
	root := realOf(t, tempRoot(t))
	if err := os.Mkdir(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "a", "b", "x.ts")
	f.recordBase(BaseInputs{file: hashOf("as built")})
	f.baseIs(true)
	if err := os.RemoveAll(filepath.Join(root, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.settled()
	writeFiles(t, filepath.Join(root, "a", "b"), map[string]string{"x.ts": "as built"})
	f.baseIs(false)
	write(t, file, "edited")
	f.baseIs(true)
}

// Ports watcher.test.ts "keeps a file two bases read with different bytes marked whatever it holds".
func TestTheBaseMarkKeepsAFileTwoBasesReadWithDifferentBytesMarkedWhateverItHolds(t *testing.T) {
	_, f := based(t, nil)
	elsewhere := realOf(t, tempRoot(t))
	file := filepath.Join(elsewhere, "shared.ts")
	writeFiles(t, elsewhere, map[string]string{"shared.ts": "first"})
	f.recordBase(BaseInputs{file: hashOf("first")})
	f.recordBase(BaseInputs{file: hashOf("second")})
	f.baseIs(true)
	write(t, file, "second")
	f.settled()
	if !f.marks().Base {
		t.Fatal("base cleared though a record holds the other bytes")
	}
}

// Not upstream's: PiG reads base files on goroutines, which are bounded and end with the watcher (a read's goroutine never
// outlives close and join, the way upstream's promises settle).
func TestTheBaseMarkReadsRecordedFilesOnBoundedGoroutinesThatEndWithTheWatcher(t *testing.T) {
	base, f := based(t, nil)
	files := map[string]string{}
	inputs := BaseInputs{}
	for i := range 200 {
		name := "f" + strconv.Itoa(i) + ".ts"
		files[name] = "export {};"
		inputs[filepath.Join(base, name)] = hashOf("export {};")
	}
	writeFiles(t, base, files)
	var started int
	f.run(func() {
		f.watcher.recordBase(inputs)
		started = f.watcher.comparers
	})
	if started == 0 || started > maxComparers {
		t.Fatalf("%d goroutines reading %d files, want 1 to %d", started, len(inputs), maxComparers)
	}
	f.joinComparisons()
	if f.marks().Base {
		t.Fatal("base set by files as they were recorded")
	}
	f.run(f.watcher.close)
	f.watcher.join()
	f.run(func() {
		if f.watcher.comparers != 0 || len(f.watcher.comparing) != 0 {
			t.Errorf("comparers = %d, comparing = %d after join", f.watcher.comparers, len(f.watcher.comparing))
		}
	})
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	if strings.Contains(string(buf), "(*pluginWatcher).compareQueued") || strings.Contains(string(buf), "(*pluginWatcher).readEvents") {
		t.Fatalf("a watcher goroutine outlived join:\n%s", buf)
	}
}
