package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/test/node/app/archive.test.ts (Pi main c23b9ec1): pack and unpack round-trip a host directory (its store's
// export and its files, uploads under way left out); unpacking refuses an existing directory, a manifest `accept` refuses, an
// archive not starting with its manifest and an entry outside the format, each leaving nothing behind. The round trip of a live
// worker's documents, tables and files is in app_test.go.

var archiveManifest = ArchiveManifest{Format: 1, App: "acme", API: "v1", Address: ArchiveAddress{Kind: "doc", Key: "doc|1"}}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	slices.Sort(out)
	return out
}

// hostDirectory is a host directory with a fresh store and files `files/a` and `files/sub/b`, plus an upload under way.
func hostDirectory(t *testing.T, base string) string {
	t.Helper()
	dir := filepath.Join(base, "host")
	if err := os.MkdirAll(filepath.Join(dir, "files", ".uploads", "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := openStoreAt(t, filepath.Join(dir, "store.sqlite")).Close(background); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "files", "a"), "file a")
	writeFile(t, filepath.Join(dir, "files", "sub", "b"), "file b")
	writeFile(t, filepath.Join(dir, "files", ".uploads", "run", "u"), "half")
	return dir
}

// exportedStore is the JSONL export of the store in dir.
func exportedStore(t *testing.T, dir string) string {
	t.Helper()
	opened := openStoreAt(t, filepath.Join(dir, "store.sqlite"))
	export, err := opened.Export(background)
	if err != nil {
		t.Fatal(err)
	}
	source := store.EncodeJsonl(export)
	var text bytes.Buffer
	for {
		chunk, ok, err := source.Next(background)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		text.Write(chunk)
	}
	if err := errors.Join(source.Close(), opened.Close(background)); err != nil {
		t.Fatal(err)
	}
	return text.String()
}

// forged writes an archive at path of entries (path and text), as an attacker could make one.
func forged(t *testing.T, path string, entries ...[2]string) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	all := func(yield func(TarEntry) bool) {
		for _, entry := range entries {
			if !yield(TarEntry{Path: entry[0], Size: int64(len(entry[1])), Data: strings.NewReader(entry[1])}) {
				return
			}
		}
	}
	if err := errors.Join(WriteTar(out, all), out.Close()); err != nil {
		t.Fatal(err)
	}
}

func manifestText(t *testing.T) string {
	t.Helper()
	text, err := json.Marshal(archiveManifest)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func wantError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want one holding %q", err, want)
	}
}

func TestRoundTripsAHostDirectorysStoreAndFilesWithoutUploadsUnderWay(t *testing.T) {
	base := t.TempDir()
	dir := hostDirectory(t, base)
	if err := PackData(background, dir, archiveManifest, filepath.Join(base, "out.tar")); err != nil {
		t.Fatal(err)
	}
	var seen []ArchiveManifest
	accept := func(manifest ArchiveManifest) error { seen = append(seen, manifest); return nil }
	if err := UnpackData(background, filepath.Join(base, "out.tar"), filepath.Join(base, "copy"), accept, ImportLimits{}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != archiveManifest {
		t.Fatalf("seen = %+v", seen)
	}
	if got := dirNames(t, filepath.Join(base, "copy")); !slices.Equal(got, []string{"files", "store.sqlite"}) {
		t.Fatalf("copy = %v", got)
	}
	if got := dirNames(t, filepath.Join(base, "copy", "files")); !slices.Equal(got, []string{"a", "sub"}) {
		t.Fatalf("copy/files = %v", got)
	}
	if got, _ := os.ReadFile(filepath.Join(base, "copy", "files", "sub", "b")); string(got) != "file b" {
		t.Fatalf("b = %q", got)
	}
	if exportedStore(t, filepath.Join(base, "copy")) != exportedStore(t, dir) {
		t.Fatal("the store's export differs after the round trip")
	}
	// No staging directory left beside either.
	if got := dirNames(t, base); !slices.Equal(got, []string{"copy", "host", "out.tar"}) {
		t.Fatalf("base = %v", got)
	}
}

func TestRefusesToPackAnAddressWithoutData(t *testing.T) {
	base := t.TempDir()
	wantError(t, PackData(background, filepath.Join(base, "none"), archiveManifest, filepath.Join(base, "out.tar")), "has no data")
}

func TestRefusesAnArchivePastItsLimitsUnpackedBytesAndEntriesCountedBeforeWriting(t *testing.T) {
	base := t.TempDir()
	big := strings.Repeat("x", 2000)
	head := [][2]string{{"manifest.json", manifestText(t)}, {"store.archive", ""}}
	forged(t, filepath.Join(base, "big.tar"), append(slices.Clone(head), [2]string{"files/a", big}, [2]string{"files/b", big})...)
	wantError(t, UnpackData(background, filepath.Join(base, "big.tar"), filepath.Join(base, "a"), func(ArchiveManifest) error { return nil }, ImportLimits{Bytes: 3000}), "unpacks to more than 3000 bytes")
	many := slices.Clone(head)
	for _, name := range []string{"0", "1", "2", "3", "4"} {
		many = append(many, [2]string{"files/" + name, "x"})
	}
	forged(t, filepath.Join(base, "many.tar"), many...)
	wantError(t, UnpackData(background, filepath.Join(base, "many.tar"), filepath.Join(base, "b"), func(ArchiveManifest) error { return nil }, ImportLimits{Entries: 4}), "more than 4 entries")
	if got := dirNames(t, base); !slices.Equal(got, []string{"big.tar", "many.tar"}) {
		t.Fatalf("base = %v", got)
	}
}

func TestRefusesAnExistingDirectory(t *testing.T) {
	base := t.TempDir()
	dir := hostDirectory(t, base)
	if err := PackData(background, dir, archiveManifest, filepath.Join(base, "out.tar")); err != nil {
		t.Fatal(err)
	}
	wantError(t, UnpackData(background, filepath.Join(base, "out.tar"), dir, func(ArchiveManifest) error { return nil }, ImportLimits{}), "exists already")
}

func TestRefusesWhatAcceptRefusesAnArchiveNotStartingWithItsManifestAndAForeignEntryLeavingNothing(t *testing.T) {
	base := t.TempDir()
	if err := PackData(background, hostDirectory(t, base), archiveManifest, filepath.Join(base, "out.tar")); err != nil {
		t.Fatal(err)
	}
	refuse := func(ArchiveManifest) error { return errors.New("not this app") }
	accept := func(ArchiveManifest) error { return nil }
	wantError(t, UnpackData(background, filepath.Join(base, "out.tar"), filepath.Join(base, "a"), refuse, ImportLimits{}), "not this app")
	forged(t, filepath.Join(base, "late.tar"), [2]string{"files/x", "x"}, [2]string{"manifest.json", manifestText(t)})
	wantError(t, UnpackData(background, filepath.Join(base, "late.tar"), filepath.Join(base, "b"), accept, ImportLimits{}), "start with manifest.json")
	forged(t, filepath.Join(base, "extra.tar"), [2]string{"manifest.json", manifestText(t)}, [2]string{"plugins/evil.ts", "x"})
	wantError(t, UnpackData(background, filepath.Join(base, "extra.tar"), filepath.Join(base, "c"), accept, ImportLimits{}), "plugins/evil.ts")
	forged(t, filepath.Join(base, "nostore.tar"), [2]string{"manifest.json", manifestText(t)})
	wantError(t, UnpackData(background, filepath.Join(base, "nostore.tar"), filepath.Join(base, "d"), accept, ImportLimits{}), "no manifest.json or store.archive")
	if got := dirNames(t, base); !slices.Equal(got, []string{"extra.tar", "host", "late.tar", "nostore.tar", "out.tar"}) {
		t.Fatalf("base = %v", got)
	}
}

// Beyond Pi's tests: the manifest is written as JSON.stringify writes it (no HTML escaping, members in order).
func TestTheManifestIsWrittenAsJSONStringifyWritesIt(t *testing.T) {
	manifest := ArchiveManifest{Format: 1, App: "a&b<c>", API: "v1", Address: ArchiveAddress{Kind: "doc", Key: "doc|1"}}
	if got, want := string(manifest.text()), `{"format":1,"app":"a&b<c>","api":"v1","address":{"kind":"doc","key":"doc|1"}}`; got != want {
		t.Fatalf("manifest = %s, want %s", got, want)
	}
}
