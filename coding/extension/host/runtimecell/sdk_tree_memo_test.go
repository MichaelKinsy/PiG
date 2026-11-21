package runtimecell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// sdkFixtureFiles is a small SDK as a staged copy holds it: sources in the root and in packages below it, a file that is not a source, and the
// marker file the host stages with it. Every one of them is covered by the tree hash.
var sdkFixtureFiles = map[string]string{
	"go.mod":                "module github.com/MichaelKinsy/PiG/extensions/sdk\n\ngo 1.26\n",
	"sdk.go":                "package sdk\n\nconst Protocol = 1\n",
	"extension.go":          "package sdk\n\ntype Extension struct{}\n",
	"json/encode.go":        "package json\n\nfunc Encode() {}\n",
	"json/decode.go":        "package json\n\nfunc Decode() {}\n",
	"internal/util/util.go": "package util\n",
	"README.md":             "the SDK\n",
	".pig-sdk-version":      "0123456789abcdef\n",
}

var sdkFixtureCount = len(sdkFixtureFiles)

// referenceHashTree is hashTree as it was before the memo and before its walk was shared: the function the cache entries of every released pig are
// named by, kept whole so a test can compare what the memo answers with it.
func referenceHashTree(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "error:" + err.Error()
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "error:" + err.Error()
	}
	h := sha256.New()
	h.Write([]byte(abs))
	h.Write([]byte{0})
	info, err := os.Stat(abs)
	if err != nil {
		return "error:" + err.Error()
	}
	if !info.IsDir() {
		data, err := os.ReadFile(abs)
		if err != nil {
			return "error:" + err.Error()
		}
		h.Write(data)
		return hex.EncodeToString(h.Sum(nil))
	}
	if err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != abs && skipPackedHashDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || skipPackedHashFile(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write([]byte(filepath.ToSlash(rel)))
		h.Write([]byte("\x00"))
		h.Write(data)
		h.Write([]byte("\x00"))
		return nil
	}); err != nil {
		return "error:" + err.Error()
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeTree writes files, a map from slash-separated relative path to content, under root.
func writeTree(t testing.TB, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// setTreeTimes gives every regular file under root, those of hidden directories too, the modification time mtime.
func setTreeTimes(t testing.TB, root string, mtime time.Time) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			return os.Chtimes(path, mtime, mtime)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// writeAt replaces the content of the file name under root and gives it the modification time mtime.
func writeAt(t testing.TB, root, name, content string, mtime time.Time) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// filesystemTimeStep returns the smallest modification-time step that the filesystem holding root preserves above base (1 ns on ext4/APFS, 100 ns on NTFS).
func filesystemTimeStep(t testing.TB, root string, base time.Time) time.Duration {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "mtime-step")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, step := range []time.Duration{time.Nanosecond, 100 * time.Nanosecond, time.Microsecond, time.Millisecond, time.Second} {
		target := base.Add(step)
		if err := os.Chtimes(probe, target, target); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(probe)
		if err != nil {
			t.Fatal(err)
		}
		if info.ModTime().UnixNano() != base.UnixNano() {
			return step
		}
	}
	t.Fatalf("filesystem at %s did not preserve any modification-time step up to 1s above %v", root, base)
	return 0
}

// longAgo is the time of the files of the fixtures: well before the window in which the memo does not trust a file. It is the same time at every
// call, to the second, so an edit that gives a file back this time restores exactly the time the memo stamped.
var longAgo = sync.OnceValue(func() time.Time { return time.Now().Add(-time.Hour).Truncate(time.Second) })

// stagedFixture writes the SDK fixture where the host stages an SDK, <config root>/state/pigsdk/sdk-<hash>, with files an hour old, as an installed
// pig's are at its next start.
func stagedFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state", "pigsdk", "sdk-0123456789abcdef")
	writeTree(t, root, sdkFixtureFiles)
	setTreeTimes(t, root, longAgo())
	return root
}

// checkoutFixture writes the SDK fixture where a repository keeps it, <checkout>/extensions/sdk, with files an hour old.
func checkoutFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "extensions", "sdk")
	writeTree(t, root, sdkFixtureFiles)
	setTreeTimes(t, root, longAgo())
	return root
}

// countTreeReads counts the files the tree hashes of the test read, from now on. The function it returns reports the count since its last call.
func countTreeReads(t testing.TB) func() int {
	t.Helper()
	var reads atomic.Int64
	previous := readTreeFile
	readTreeFile = func(name string) ([]byte, error) {
		reads.Add(1)
		return previous(name)
	}
	t.Cleanup(func() { readTreeFile = previous })
	return func() int { return int(reads.Swap(0)) }
}

// emptyTreeMemo starts the test with no tree memoized and leaves none for the next.
func emptyTreeMemo(t testing.TB) {
	t.Helper()
	empty := func() {
		memoizedTrees.mu.Lock()
		defer memoizedTrees.mu.Unlock()
		memoizedTrees.byRoot = nil
	}
	empty()
	t.Cleanup(empty)
}

// pinTreeClock makes the memo read now as the time at which it began, for the test.
func pinTreeClock(t testing.TB, now time.Time) {
	t.Helper()
	previous := treeMemoClock
	treeMemoClock = func() time.Time { return now }
	t.Cleanup(func() { treeMemoClock = previous })
}

func requireHash(t testing.TB, hash string) string {
	t.Helper()
	if strings.HasPrefix(hash, "error:") {
		t.Fatal(hash)
	}
	return hash
}

// The hash a lookup answers is the hash hashTree computes, from the files or from the memo, so the cache entries of the pigs that came before keep
// their names: for a staged SDK, for a source checkout, for a tree with everything the walk passes over, and for a copy of the repository's own SDK.
func TestSDKTreeHashIsTheHashOfTheFilesWhereverItIsAnsweredFrom(t *testing.T) {
	emptyTreeMemo(t)
	variety := func(t *testing.T, root string) {
		writeTree(t, root, map[string]string{
			".git/config":               "[core]\n",
			"node_modules/x/index.js":   "x\n",
			"target/debug/out":          "o\n",
			"vendor/modules.txt":        "v\n",
			"dist/a.js":                 "a\n",
			"build/b.js":                "b\n",
			"__pycache__/m.cpython.pyc": "p\n",
			".gz/sdk.go.gz":             "z\n",
			"notes.tmp":                 "t\n",
			"backup.go~":                "b\n",
			"m.pyc":                     "c\n",
			"m.pyo":                     "o\n",
			"empty":                     "",
			"binary.bin":                "\x00\x01\x02\xff\xfe",
			"ünï/cödé.go":               "package u\n",
			"a/b/c/d/deep.go":           "package d\n",
		})
		testenv.Symlink(t, "sdk.go", filepath.Join(root, "link.go"))
		setTreeTimes(t, root, longAgo())
	}
	repositorySDK, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		root func(t *testing.T) string
	}{
		{"a staged SDK", stagedFixture},
		{"a source checkout", checkoutFixture},
		{"a staged SDK with everything the walk passes over", func(t *testing.T) string { root := stagedFixture(t); variety(t, root); return root }},
		{"a copy of the repository's SDK, staged", func(t *testing.T) string {
			root := filepath.Join(t.TempDir(), "state", "pigsdk", "sdk-copy")
			if err := os.CopyFS(root, os.DirFS(repositorySDK)); err != nil {
				t.Fatal(err)
			}
			setTreeTimes(t, root, longAgo())
			return root
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := tc.root(t)
			want := requireHash(t, referenceHashTree(root))
			if got := hashTree(root); got != want {
				t.Fatalf("hashTree = %s, want the reference's %s", got, want)
			}
			for call := range 3 {
				if got := sdkTreeHash(root); got != want {
					t.Fatalf("sdkTreeHash, call %d = %s, want the reference's %s", call+1, got, want)
				}
			}
		})
	}
	t.Run("a root that is not a directory, and one that is not there", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "state", "pigsdk", "sdk-file")
		writeTree(t, filepath.Dir(file), map[string]string{"sdk-file": "a file\n"})
		for _, root := range []string{file, filepath.Join(t.TempDir(), "missing")} {
			want := referenceHashTree(root)
			for call := range 2 {
				if got := sdkTreeHash(root); got != want {
					t.Errorf("%s, call %d: %q, want %q", root, call+1, got, want)
				}
			}
		}
	})
}

// The second load of a process finds the staged SDK as the first left it, and the hash of its tree is not read again: each load looks at the
// tree, and not one of its files is read. Before the memo every load read every file (212 files, 2.1 MB for an installed pig's Go SDK).
func TestSecondLoadOfAnUnchangedStagedSDKReadsNoFile(t *testing.T) {
	emptyTreeMemo(t)
	root := stagedFixture(t)
	reads := countTreeReads(t)
	first := loadTreeHash(WithLoadTreeHashes(t.Context()), root)
	requireHash(t, first)
	if got := reads(); got != sdkFixtureCount {
		t.Fatalf("the first load read %d files, want its %d", got, sdkFixtureCount)
	}
	for load := 2; load <= 4; load++ {
		if got := loadTreeHash(WithLoadTreeHashes(t.Context()), root); got != first {
			t.Fatalf("load %d: %s, want the first load's %s", load, got, first)
		}
		if got := reads(); got != 0 {
			t.Fatalf("load %d of an unchanged staged SDK read %d files, want none", load, got)
		}
	}
	if got := loadTreeHash(t.Context(), root); got != first || reads() != 0 {
		t.Fatalf("outside a load: %s after %d reads, want %s after none", got, reads(), first)
	}
}

// A lookup that finds the staged tree as it was has every other kind of change to find, and finds each: whatever changes the size or the
// modification time of a file, whatever adds, removes, renames or replaces a file. The tree is read again, and its hash is the hash of the files.
func TestStagedSDKHashFollowsEveryChangeOfASizeATimeOrTheFiles(t *testing.T) {
	then := longAgo()
	for _, tc := range []struct {
		name string
		edit func(t *testing.T, root string)
	}{
		{"a file of another size, its time restored", func(t *testing.T, root string) {
			writeAt(t, root, "sdk.go", "package sdk\n\nconst Protocol = 22\n", then)
		}},
		{"a file of the same size, a later time", func(t *testing.T, root string) {
			writeAt(t, root, "sdk.go", "package sdk\n\nconst Protocol = 2\n", then.Add(30*time.Minute))
		}},
		{"a file of the same size, an earlier time", func(t *testing.T, root string) {
			writeAt(t, root, "sdk.go", "package sdk\n\nconst Protocol = 2\n", then.Add(-30*time.Minute))
		}},
		{"a file of the same size, a time one filesystem tick later", func(t *testing.T, root string) {
			// NTFS stores modification times in 100 ns units (FILETIME), so a 1 ns step rounds back to the same mtime;
			// probe the smallest step this filesystem preserves so the subtest proves sub-second mtime sensitivity on every OS.
			writeAt(t, root, "sdk.go", "package sdk\n\nconst Protocol = 2\n", then.Add(filesystemTimeStep(t, root, then)))
		}},
		{"a file in a package below the root", func(t *testing.T, root string) {
			writeAt(t, root, "internal/util/util.go", "package util\n\nvar X = 1\n", then)
		}},
		{"the marker file", func(t *testing.T, root string) {
			writeAt(t, root, ".pig-sdk-version", "fedcba9876543210\n", then.Add(time.Minute))
		}},
		{"a file added", func(t *testing.T, root string) {
			writeAt(t, root, "added.go", "package sdk\n", then)
		}},
		{"a file added in a new directory", func(t *testing.T, root string) {
			writeAt(t, root, "newpkg/added.go", "package newpkg\n", then)
		}},
		{"a file added that comes after every other file", func(t *testing.T, root string) {
			writeAt(t, root, "zzz/last.go", "package zzz\n", then)
		}},
		{"the last file removed", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "sdk.go")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a file removed", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "extension.go")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a directory and its files removed", func(t *testing.T, root string) {
			if err := os.RemoveAll(filepath.Join(root, "json")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a file renamed in its directory", func(t *testing.T, root string) {
			if err := os.Rename(filepath.Join(root, "sdk.go"), filepath.Join(root, "sdk_renamed.go")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a file moved to another directory", func(t *testing.T, root string) {
			if err := os.Rename(filepath.Join(root, "sdk.go"), filepath.Join(root, "json", "sdk.go")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a file replaced by a link", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "sdk.go")); err != nil {
				t.Fatal(err)
			}
			testenv.Symlink(t, "extension.go", filepath.Join(root, "sdk.go"))
		}},
		{"the whole tree restaged: every file written again with another content", func(t *testing.T, root string) {
			for name := range sdkFixtureFiles {
				writeAt(t, root, name, "restaged "+name+"\n", then.Add(time.Minute))
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emptyTreeMemo(t)
			root := stagedFixture(t)
			reads := countTreeReads(t)
			before := requireHash(t, sdkTreeHash(root))
			reads()
			if again := sdkTreeHash(root); again != before || reads() != 0 {
				t.Fatalf("the unchanged tree is not answered from the memo (%s after %d reads, want %s after none), so the rest proves nothing", again, reads(), before)
			}
			tc.edit(t, root)
			want := requireHash(t, referenceHashTree(root))
			if want == before {
				t.Fatal("the edit changes no hash")
			}
			if got := sdkTreeHash(root); got != want {
				t.Fatalf("after the edit: %s, want the hash of the files %s (the stale one is %s)", got, want, before)
			}
			if reads() == 0 {
				t.Error("the changed tree was answered without reading its files")
			}
			if again := sdkTreeHash(root); again != want || reads() != 0 {
				t.Errorf("the changed tree is not memoized in its turn: %s after %d reads, want %s after none", again, reads(), want)
			}
		})
	}
}

// The tree hash does not look at some changes, and the memo does not answer to them either: the hash stays, nothing is read, and the hash is the
// hash of the files. A directory the walk skips gains files, a file the walk passes over appears, a directory is made, a file's mode changes.
func TestStagedSDKHashDoesNotAnswerToWhatTheHashIgnores(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name string
		edit func(t *testing.T, root string)
	}{
		{".git gains a file", func(t *testing.T, root string) { writeAt(t, root, ".git/HEAD", "ref\n", now) }},
		{"node_modules gains a file", func(t *testing.T, root string) { writeAt(t, root, "node_modules/x/index.js", "x\n", now) }},
		{"target gains a file", func(t *testing.T, root string) { writeAt(t, root, "target/debug/out", "o\n", now) }},
		{"vendor gains a file", func(t *testing.T, root string) { writeAt(t, root, "vendor/modules.txt", "v\n", now) }},
		{"dist gains a file", func(t *testing.T, root string) { writeAt(t, root, "dist/a.js", "a\n", now) }},
		{"build gains a file", func(t *testing.T, root string) { writeAt(t, root, "build/b.js", "b\n", now) }},
		{"__pycache__ gains a file", func(t *testing.T, root string) { writeAt(t, root, "__pycache__/m.pyc", "p\n", now) }},
		{"a hidden directory below the root gains a file", func(t *testing.T, root string) { writeAt(t, root, "json/.cache/x", "x\n", now) }},
		{"a skipped directory below the root gains a file", func(t *testing.T, root string) { writeAt(t, root, "json/vendor/x.go", "x\n", now) }},
		{"a .tmp file appears", func(t *testing.T, root string) { writeAt(t, root, "sdk.go.tmp", "t\n", now) }},
		{"a ~ file appears", func(t *testing.T, root string) { writeAt(t, root, "sdk.go~", "t\n", now) }},
		{"a .pyc file appears", func(t *testing.T, root string) { writeAt(t, root, "json/m.pyc", "c\n", now) }},
		{"a .pyo file appears", func(t *testing.T, root string) { writeAt(t, root, "json/m.pyo", "o\n", now) }},
		{"an empty directory is made", func(t *testing.T, root string) {
			if err := os.Mkdir(filepath.Join(root, "json", "empty"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"a link appears", func(t *testing.T, root string) { testenv.Symlink(t, "sdk.go", filepath.Join(root, "alias.go")) }},
		{"a file's mode changes", func(t *testing.T, root string) {
			if runtime.GOOS == "windows" {
				t.Skip("modes are not kept on Windows")
			}
			if err := os.Chmod(filepath.Join(root, "sdk.go"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emptyTreeMemo(t)
			root := stagedFixture(t)
			reads := countTreeReads(t)
			before := requireHash(t, sdkTreeHash(root))
			reads()
			tc.edit(t, root)
			if want := requireHash(t, referenceHashTree(root)); want != before {
				t.Fatalf("the edit changes the hash of the files: %s, was %s", want, before)
			}
			if got := sdkTreeHash(root); got != before || reads() != 0 {
				t.Errorf("after the edit: %s after %d reads, want %s after none", got, reads(), before)
			}
		})
	}
}

// A file whose time the memo cannot tell from the time of an edit made right after it read the tree is not trusted. The clock is the memo's own
// (pinTreeClock): a file is trusted when it is older than racyTreeWindow, and a file at the edge, at the clock, or after it is not. A tree with such
// a file is read in full every time, and answers to an edit that keeps both the size and the time of the file.
func TestStagedSDKHashDoesNotTrustAFileTheClockCannotTellFromAnEdit(t *testing.T) {
	began := time.Now().Truncate(time.Second)
	for _, tc := range []struct {
		name   string
		age    time.Duration // the time of sdk.go, from the clock's: negative is before it
		trusts bool
	}{
		{"a file a second before the clock", -time.Second, false},
		{"a file at the edge of the window", -racyTreeWindow, false},
		{"a file just inside the window", -racyTreeWindow + time.Nanosecond, false},
		{"a file a second past the window", -racyTreeWindow - time.Second, true},
		{"a file just past the window", -racyTreeWindow - time.Millisecond, true},
		{"a file written at the clock", 0, false},
		{"a file written in the future of the clock", time.Hour, false},
		{"a file written a second in the future of the clock", time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emptyTreeMemo(t)
			root := stagedFixture(t)
			pinTreeClock(t, began)
			at := began.Add(tc.age)
			if err := os.Chtimes(filepath.Join(root, "sdk.go"), at, at); err != nil {
				t.Fatal(err)
			}
			reads := countTreeReads(t)
			// Every other file is an hour before the real clock, which the pinned one is not far from.
			before := requireHash(t, sdkTreeHash(root))
			reads()
			again := sdkTreeHash(root)
			got := reads()
			if tc.trusts && got != 0 {
				t.Errorf("a file past the window kept the tree from the memo: the second lookup read %d files", got)
			}
			if !tc.trusts && got != sdkFixtureCount {
				t.Errorf("a file the clock cannot tell from an edit was trusted: the second lookup read %d files, want all %d", got, sdkFixtureCount)
			}
			if again != before {
				t.Fatalf("the second lookup: %s, want %s", again, before)
			}
			// An edit of the same size that gives the file back its time, as an edit within the tick of a coarse clock does.
			writeAt(t, root, "sdk.go", "package sdk\n\nconst Protocol = 2\n", at)
			want := requireHash(t, referenceHashTree(root))
			if got := sdkTreeHash(root); !tc.trusts && got != want {
				t.Errorf("the edit that keeps the size and the time was missed: %s, want %s", got, want)
			}
		})
	}
}

// A tree freshly staged has files of the time of the staging. Its first lookups read it in full and none is memoized; once its files are older than
// the window the next lookup reads it and memoizes it, and the lookups after that read nothing.
func TestStagedSDKHashIsMemoizedOnceItsFilesAreOlderThanTheWindow(t *testing.T) {
	emptyTreeMemo(t)
	root := stagedFixture(t)
	now := time.Now().Truncate(time.Second)
	pinTreeClock(t, now)
	setTreeTimes(t, root, now.Add(-time.Second))
	reads := countTreeReads(t)
	want := requireHash(t, sdkTreeHash(root))
	for call := range 3 {
		if got := sdkTreeHash(root); got != want {
			t.Fatalf("fresh tree, lookup %d: %s, want %s", call+2, got, want)
		}
	}
	if got, all := reads(), 4*sdkFixtureCount; got != all {
		t.Fatalf("four lookups of a freshly staged tree read %d files, want all of them every time (%d)", got, all)
	}
	pinTreeClock(t, now.Add(racyTreeWindow+time.Second))
	if got := sdkTreeHash(root); got != want || reads() != sdkFixtureCount {
		t.Fatalf("the first lookup after the window: %s after %d reads, want %s after %d", got, reads(), want, sdkFixtureCount)
	}
	for call := range 3 {
		if got := sdkTreeHash(root); got != want || reads() != 0 {
			t.Fatalf("lookup %d after the window: %s after %d reads, want %s after none", call+2, got, reads(), want)
		}
	}
}

// The limit of the memo, which the documentation of the cache states: an edit that keeps both the size and the modification time of a file that was
// older than the window when the memo read it is not seen. The memo answers with the hash of the tree before the edit, and the next lookup that
// reads the tree, a changed size or time anywhere in it, answers with the hash after. The staged SDK is written by this host's staging, which
// writes the whole tree with new times, and never edited in place; a tool that restores times over a file of the same length is the only thing
// that can do this.
func TestStagedSDKHashDoesNotSeeAnEditThatKeepsTheSizeAndAnOldTime(t *testing.T) {
	emptyTreeMemo(t)
	root := stagedFixture(t)
	before := requireHash(t, sdkTreeHash(root))
	writeAt(t, root, "sdk.go", "package sdk\n\nconst Protocol = 2\n", longAgo())
	after := requireHash(t, referenceHashTree(root))
	if after == before {
		t.Fatal("the edit changes no hash")
	}
	if got := sdkTreeHash(root); got != before {
		t.Errorf("the lookup saw an edit that keeps the size and the time (%s, want the stale %s): the stated limit is gone, so the documentation of the cache is wrong", got, before)
	}
	writeAt(t, root, "extension.go", "package sdk\n\ntype Extension struct{ X int }\n", longAgo())
	if got, want := sdkTreeHash(root), requireHash(t, referenceHashTree(root)); got != want {
		t.Errorf("a change of a size makes the memo read the tree again, which finds both edits: %s, want %s", got, want)
	}
}

// Only a root the host stages itself is memoized: a directory directly inside <config root>/state/pigsdk, as the product judges a staged SDK
// (RetiredStagedSDK). The rule judges the physical path.
func TestStagedSDKTreeIsADirectoryInsideTheStagingArea(t *testing.T) {
	for path, staged := range map[string]bool{
		"/home/u/.pig/state/pigsdk/sdk-0123456789abcdef":    true,
		"/home/u/.pig/state/pigsdk/sdk-py-0123456789abcdef": true,
		"/home/u/.pig/state/pigsdk/sdk-rs-0123456789abcdef": true,
		"/home/u/.pig/state/pigsdk/sdk":                     true, // the unhashed directory of a pig that predates per-build directories
		"/x/state/pigsdk/sdk-1":                             true,
		"/home/u/.pig/state/pigsdk":                         false,
		"/home/u/.pig/state/pigsdk/sdk-1/json":              false,
		"/home/u/.pig/pigsdk/sdk-1":                         false,
		"/home/u/.pig/state/other/sdk-1":                    false,
		"/home/u/.pig/source/extensions/sdk":                false,
		"/home/u/work/PiG/extensions/sdk":                   false,
		"/state/pigsdk":                                     false,
	} {
		if got := stagedSDKTree(filepath.FromSlash(path)); got != staged {
			t.Errorf("stagedSDKTree(%s) = %v, want %v", path, got, staged)
		}
	}
}

// A developer edits the SDK tree of a checkout, so every load of it reads every file, as before the memo: the repository's extensions/sdk found
// from the working directory, an installed copy of the source, a PIG_SDK_GO_ROOT, a tree that only looks like the staging area, and a root that
// reaches a checkout through a link inside the staging area. A link from anywhere to a staged directory is a staged SDK.
func TestRootsTheHostDoesNotStageAreReadInFullByEveryLoad(t *testing.T) {
	emptyTreeMemo(t)
	inArea := func(t *testing.T) string {
		root := filepath.Join(t.TempDir(), "state", "pigsdk")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		return root
	}
	for _, tc := range []struct {
		name   string
		root   func(t *testing.T) string
		staged bool
	}{
		{"the repository's checkout", checkoutFixture, false},
		{"an installed copy of the source", func(t *testing.T) string {
			root := filepath.Join(t.TempDir(), "source", "extensions", "sdk")
			writeTree(t, root, sdkFixtureFiles)
			setTreeTimes(t, root, longAgo())
			return root
		}, false},
		{"a directory that is not in the staging area but has its name", func(t *testing.T) string {
			root := filepath.Join(t.TempDir(), "pigsdk", "sdk-0123456789abcdef")
			writeTree(t, root, sdkFixtureFiles)
			setTreeTimes(t, root, longAgo())
			return root
		}, false},
		{"the staging area itself", func(t *testing.T) string {
			root := inArea(t)
			writeTree(t, root, sdkFixtureFiles)
			setTreeTimes(t, root, longAgo())
			return root
		}, false},
		{"a directory inside a staged SDK", func(t *testing.T) string {
			root := stagedFixture(t)
			return filepath.Join(root, "json")
		}, false},
		{"a checkout reached by a link inside the staging area", func(t *testing.T) string {
			link := filepath.Join(inArea(t), "sdk-link")
			testenv.RequireDirectoryLink(t, checkoutFixture(t), link)
			return link
		}, false},
		{"a staged SDK reached by a link from outside", func(t *testing.T) string {
			link := filepath.Join(t.TempDir(), "sdk-link")
			testenv.RequireDirectoryLink(t, stagedFixture(t), link)
			return link
		}, true},
		{"a staged SDK", stagedFixture, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := tc.root(t)
			reads := countTreeReads(t)
			files := len(treeFilesOf(t, root))
			first := requireHash(t, loadTreeHash(WithLoadTreeHashes(t.Context()), root))
			if got := reads(); got != files {
				t.Fatalf("the first load read %d files, want %d", got, files)
			}
			for load := 2; load <= 3; load++ {
				if got := loadTreeHash(WithLoadTreeHashes(t.Context()), root); got != first {
					t.Fatalf("load %d: %s, want %s", load, got, first)
				}
				want := files
				if tc.staged {
					want = 0
				}
				if got := reads(); got != want {
					t.Errorf("load %d read %d files, want %d (staged: %v)", load, got, want, tc.staged)
				}
			}
		})
	}
	t.Run("a PIG_SDK_GO_ROOT is found as the root of the load and read in full", func(t *testing.T) {
		root := checkoutFixture(t)
		t.Setenv("PIG_SDK_GO_ROOT", root)
		normalized, err := normalizeGoExtensions([]GoExtension{{Name: "ask", Root: t.TempDir(), ModulePath: "example.com/ask", Factory: "Extension", Hash: "source"}}, true)
		if err != nil {
			t.Fatal(err)
		}
		found, err := findSDKRootWithPreferred(normalized, "")
		if err != nil || found != root {
			t.Fatalf("findSDKRootWithPreferred = %q, %v; want %q", found, err, root)
		}
		reads := countTreeReads(t)
		for load := 1; load <= 3; load++ {
			requireHash(t, loadTreeHash(WithLoadTreeHashes(t.Context()), found))
			if got := reads(); got != sdkFixtureCount {
				t.Errorf("load %d read %d files, want all %d", load, got, sdkFixtureCount)
			}
		}
	})
	t.Run("the staged directory a host prefers is memoized", func(t *testing.T) {
		staged := stagedFixture(t)
		normalized, err := normalizeGoExtensions([]GoExtension{{Name: "ask", Root: t.TempDir(), ModulePath: "example.com/ask", Factory: "Extension", Hash: "source"}}, true)
		if err != nil {
			t.Fatal(err)
		}
		found, err := findSDKRootWithPreferred(normalized, staged)
		if err != nil || found != staged {
			t.Fatalf("findSDKRootWithPreferred = %q, %v; want %q", found, err, staged)
		}
		reads := countTreeReads(t)
		requireHash(t, loadTreeHash(WithLoadTreeHashes(t.Context()), found))
		reads()
		requireHash(t, loadTreeHash(WithLoadTreeHashes(t.Context()), found))
		if got := reads(); got != 0 {
			t.Errorf("the second load read %d files, want none", got)
		}
	})
}

// treeFilesOf lists the files of the tree a hash of root covers.
func treeFilesOf(t testing.TB, root string) []string {
	t.Helper()
	abs, err := resolveTreeRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	if err := walkHashedFiles(abs, func(path string, _ fs.FileInfo) error {
		files = append(files, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

// The key of a build is read from the files: a build hashes the tree under its build lock, and again when the compiler returns, whatever the memo
// holds, so an image is published under the key of the content it was compiled from.
func TestAKeyForABuildIsReadFromTheFilesWhateverTheMemoHolds(t *testing.T) {
	emptyTreeMemo(t)
	root := stagedFixture(t)
	reads := countTreeReads(t)
	requireHash(t, sdkTreeHash(root))
	if again := sdkTreeHash(root); reads() != sdkFixtureCount || again == "" {
		t.Fatal("the memo does not hold the tree")
	}
	normalized, err := normalizeGoExtensions([]GoExtension{{Name: "ask", Root: t.TempDir(), ModulePath: "example.com/ask", Factory: "Extension", Hash: "source"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := t.TempDir()
	key := goPackedCellHash(cacheRoot, "ask", normalized, root)
	if strings.HasPrefix(key, "error:") {
		t.Fatal(key)
	}
	if got := reads(); got != sdkFixtureCount {
		t.Errorf("the key of a build read %d files, want all %d", got, sdkFixtureCount)
	}
	if shared := goPackedCellHashOfSDK(cacheRoot, "ask", normalized, loadTreeHash(WithLoadTreeHashes(t.Context()), root)); shared != key {
		t.Errorf("the key a load looks under from the memo is %s, and the key of a build is %s", shared, key)
	}
}

// Two staged SDKs at once, a language each: the lookup of one does not wait for the reading of the other. The lock of the memo guards its map and
// no file is read under it.
func TestMemoLockIsNotHeldWhileATreeIsRead(t *testing.T) {
	emptyTreeMemo(t)
	slow, quick := stagedFixture(t), stagedFixture(t)
	slowPhysical, err := resolveTreeRoot(slow)
	if err != nil {
		t.Fatal(err)
	}
	reading, release := make(chan struct{}), make(chan struct{})
	var releaseOnce, readingOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	previous := readTreeFile
	readTreeFile = func(name string) ([]byte, error) {
		if strings.HasPrefix(name, slowPhysical+string(filepath.Separator)) {
			readingOnce.Do(func() { close(reading) })
			<-release
		}
		return previous(name)
	}
	var lookups sync.WaitGroup
	t.Cleanup(func() {
		letGo()
		lookups.Wait()
		readTreeFile = previous
	})
	slowHash := make(chan string, 1)
	lookups.Go(func() { slowHash <- sdkTreeHash(slow) })
	<-reading
	quickHash := make(chan string, 1)
	lookups.Go(func() { quickHash <- sdkTreeHash(quick) })
	select {
	case got := <-quickHash:
		if want := referenceHashTree(quick); got != want {
			t.Errorf("the other root's hash: %s, want %s", got, want)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the lookup of a root waited for another root's reading: the memo's lock is held across it")
	}
	letGo()
	if got, want := <-slowHash, referenceHashTree(slow); got != want {
		t.Errorf("the slow root's hash: %s, want %s", got, want)
	}
}

// Loads at once, and a staged SDK replaced as staging replaces it, a new tree beside the old one swapped in by renames: every lookup answers with
// the hash of the files it found, and the memo follows the swap. Run under the race detector.
func TestMemoAnswersLoadsAtOnceAndFollowsAReplacedTree(t *testing.T) {
	emptyTreeMemo(t)
	root := stagedFixture(t)
	then := longAgo()
	for round := range 8 {
		want := requireHash(t, referenceHashTree(root))
		var loads sync.WaitGroup
		for range 16 {
			loads.Go(func() {
				for range 4 {
					if got := loadTreeHash(WithLoadTreeHashes(context.Background()), root); got != want {
						t.Errorf("round %d: %s, want %s", round, got, want)
					}
				}
			})
		}
		loads.Wait()
		replacement := root + ".new"
		files := map[string]string{}
		maps.Copy(files, sdkFixtureFiles)
		files["sdk.go"] = "package sdk\n\nconst Protocol = " + strings.Repeat("9", round+2) + "\n"
		writeTree(t, replacement, files)
		if round%2 == 0 {
			setTreeTimes(t, replacement, then) // memoizable
		} // otherwise the files have the time of this write: freshly staged
		aside := root + ".old"
		if err := os.Rename(root, aside); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, root); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(aside); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := sdkTreeHash(root), referenceHashTree(root); got != want {
		t.Errorf("after the last swap: %s, want %s", got, want)
	}
}

// A tree that could not be read to its end gives an error and no memo: the next lookup reads it again and finds the hash of the files. A file that
// is edited while the tree is being read, to another size with its time put back, leaves the stamp of the file as it was before the read, so the
// next lookup finds the difference: the hash is of the content read, and the stamp is what was seen before it.
func TestMemoDoesNotKeepAnErrorOrTrustAStampTakenAfterTheRead(t *testing.T) {
	t.Run("a tree that could not be read", func(t *testing.T) {
		emptyTreeMemo(t)
		root := stagedFixture(t)
		failing := true
		previous := readTreeFile
		readTreeFile = func(name string) ([]byte, error) {
			if failing && filepath.Base(name) == "sdk.go" {
				return nil, os.ErrPermission
			}
			return previous(name)
		}
		t.Cleanup(func() { readTreeFile = previous })
		for range 2 {
			if got := sdkTreeHash(root); !strings.HasPrefix(got, "error:") {
				t.Fatalf("a tree with an unreadable file: %s, want an error", got)
			}
		}
		failing = false
		reads := countTreeReads(t)
		if got, want := sdkTreeHash(root), referenceHashTree(root); got != want || reads() != sdkFixtureCount {
			t.Fatalf("after the file is readable: %s after %d reads, want %s after all %d", got, reads(), want, sdkFixtureCount)
		}
	})
	t.Run("a file edited while the tree is read", func(t *testing.T) {
		emptyTreeMemo(t)
		root := stagedFixture(t)
		then := longAgo()
		previous := readTreeFile
		edited := false
		readTreeFile = func(name string) ([]byte, error) {
			data, err := previous(name)
			if !edited && filepath.Base(name) == "sdk.go" {
				edited = true
				writeAt(t, root, "sdk.go", "package sdk\n\nconst Protocol = 22\n", then)
			}
			return data, err
		}
		t.Cleanup(func() { readTreeFile = previous })
		if got := requireHash(t, sdkTreeHash(root)); got == referenceHashTree(root) {
			t.Fatal("the edit during the read is not behind the hash that was read, so the test proves nothing")
		}
		readTreeFile = previous
		if got, want := sdkTreeHash(root), referenceHashTree(root); got != want {
			t.Fatalf("the lookup after the edit: %s, want the hash of the files %s: the memo kept the hash of the content before it", got, want)
		}
	})
}

// The memo is bounded: past maxMemoizedTrees staged roots it starts again, and the answers do not change.
func TestMemoIsBounded(t *testing.T) {
	emptyTreeMemo(t)
	var last string
	for range maxMemoizedTrees + 8 {
		root := stagedFixture(t)
		want := requireHash(t, referenceHashTree(root))
		for range 2 {
			if got := sdkTreeHash(root); got != want {
				t.Fatalf("%s: %s, want %s", root, got, want)
			}
		}
		last = root
	}
	memoizedTrees.mu.Lock()
	held := len(memoizedTrees.byRoot)
	memoizedTrees.mu.Unlock()
	if held == 0 || held > maxMemoizedTrees {
		t.Errorf("the memo holds %d trees, want 1 to %d", held, maxMemoizedTrees)
	}
	reads := countTreeReads(t)
	if got := sdkTreeHash(last); reads() != 0 || got != referenceHashTree(last) {
		t.Errorf("the newest root is not in the memo")
	}
}

// What a stale answer would cost, end to end: a packed cell is built against a staged SDK, found again without a build, and then not found, and
// rebuilt, after each kind of edit to the SDK that the memo checks. The reuse path decides which binary every Go extension runs.
func TestAPackedCellIsRebuiltAfterAnEditToTheStagedSDKTheMemoChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a packed Go cell three times")
	}
	emptyTreeMemo(t)
	extension := goExtensionFixture(t, "stalecell", `sdk.New("stalecell")`)
	repositorySDK := os.Getenv("PIG_SDK_GO_ROOT")
	staged := filepath.Join(t.TempDir(), "state", "pigsdk", "sdk-0123456789abcdef")
	if err := os.CopyFS(staged, os.DirFS(repositorySDK)); err != nil {
		t.Fatal(err)
	}
	then := longAgo()
	setTreeTimes(t, staged, then)
	t.Setenv("PIG_SDK_GO_ROOT", staged)
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	build := func(step string) *GoPackedCell {
		t.Helper()
		cell, err := buildGoPackedCell(WithLoadTreeHashes(t.Context()), cacheRoot, "stale-cell", []GoExtension{extension}, staged)
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		return cell
	}
	built := build("the first build")
	if built.Cached {
		t.Fatal("the first build found a cell")
	}
	reused := build("the second load")
	if !reused.Cached || reused.Hash != built.Hash {
		t.Fatalf("the second load: cached %v under %s, want the first build's cell %s", reused.Cached, reused.Hash, built.Hash)
	}
	reads := countTreeReads(t)
	if again := build("the third load"); !again.Cached || again.Hash != built.Hash || reads() != 0 {
		t.Fatalf("the third load: cached %v under %s after %d reads, want the cell found from the memo with no read", again.Cached, again.Hash, reads())
	}
	source, err := os.ReadFile(filepath.Join(staged, "extension.go"))
	if err != nil {
		t.Fatal(err)
	}
	// A change of size, the time put back; then a change that keeps the size and moves the time.
	for _, step := range []struct {
		name    string
		content string
		mtime   time.Time
	}{
		{"an edit of another size that keeps the time", string(source) + "\n// edited, another size\n", then},
		{"an edit of the same size that moves the time", strings.Replace(string(source)+"\n// edited, another size\n", "another size", "the same size", 1), then.Add(10 * time.Minute)},
	} {
		writeAt(t, staged, "extension.go", step.content, step.mtime)
		rebuilt := build(step.name)
		if rebuilt.Cached || rebuilt.Hash == built.Hash {
			t.Fatalf("after %s the cell was served from the build of the SDK before: cached %v under %s", step.name, rebuilt.Cached, rebuilt.Hash)
		}
		if again := build(step.name + ", the load after"); !again.Cached || again.Hash != rebuilt.Hash {
			t.Fatalf("after %s the next load: cached %v under %s, want the rebuilt cell %s", step.name, again.Cached, again.Hash, rebuilt.Hash)
		}
		built = rebuilt
	}
}

// BenchmarkSDKTreeHash_Load is what the load of a packed Go cell pays for the hash of the SDK tree, for a copy of the repository's Go SDK (the
// bundle an installed pig stages is about half its size). Each row asks the hash as one load does (loadTreeHash under a new load context), and
// reports the files it read for each lookup:
//
//   - staged/second-load: a staged tree the memo holds, as the second load of a process finds it, which looks at the files and reads none;
//   - staged/first-load: the same tree with the memo empty, as the first load of a process finds it, which reads every file and keeps the stamps;
//   - checkout/load: a tree that is not staged, which reads every file on every load;
//   - reference/hashTree: hashTree as it was before the memo (referenceHashTree), the cost of every load until then.
//
// Run with: go test ./coding/extension/host/runtimecell -run=^$ -bench=SDKTreeHash_Load -benchmem
func BenchmarkSDKTreeHash_Load(b *testing.B) {
	repositorySDK, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk"))
	if err != nil {
		b.Fatal(err)
	}
	copyOfSDK := func(b *testing.B, parts ...string) string {
		root := filepath.Join(append([]string{b.TempDir()}, parts...)...)
		if err := os.CopyFS(root, os.DirFS(repositorySDK)); err != nil {
			b.Fatal(err)
		}
		setTreeTimes(b, root, longAgo())
		return root
	}
	forget := func() {
		memoizedTrees.mu.Lock()
		defer memoizedTrees.mu.Unlock()
		memoizedTrees.byRoot = nil
	}
	for _, row := range []struct {
		name  string
		root  func(b *testing.B) string
		fresh bool // the memo is emptied before each lookup
		hash  func(ctx context.Context, root string) string
	}{
		{"staged/second-load", func(b *testing.B) string { return copyOfSDK(b, "state", "pigsdk", "sdk-copy") }, false, loadTreeHash},
		{"staged/first-load", func(b *testing.B) string { return copyOfSDK(b, "state", "pigsdk", "sdk-copy") }, true, loadTreeHash},
		{"checkout/load", func(b *testing.B) string { return copyOfSDK(b, "extensions", "sdk") }, false, loadTreeHash},
		{"reference/hashTree", func(b *testing.B) string { return copyOfSDK(b, "extensions", "sdk") }, false, func(_ context.Context, root string) string { return referenceHashTree(root) }},
	} {
		b.Run(row.name, func(b *testing.B) {
			forget()
			defer forget()
			root := row.root(b)
			if got := row.hash(WithLoadTreeHashes(context.Background()), root); strings.HasPrefix(got, "error:") {
				b.Fatal(got)
			}
			reads := countTreeReads(b)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if row.fresh {
					forget()
				}
				row.hash(WithLoadTreeHashes(context.Background()), root)
			}
			b.StopTimer()
			read := float64(reads()) / float64(b.N)
			if strings.HasPrefix(row.name, "reference/") {
				// referenceHashTree reads with os.ReadFile, which the counter does not see: it reads every file the walk covers.
				read = float64(len(treeFilesOf(b, root)))
			}
			b.ReportMetric(read, "files-read/op")
		})
	}
}
