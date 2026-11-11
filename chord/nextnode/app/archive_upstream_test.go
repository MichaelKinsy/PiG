package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/test/node/app/archive.test.ts (Pi 8a5da3ce6): pack and unpack round-trip a host directory (its store's
// export and its files, uploads under way left out) and the server's part, unpacked into a staging directory for the kit to put in
// place; unpacking refuses what a manifest `accept` refuses, an archive not starting with its manifest, an entry outside the format
// and an archive past its limits, each leaving nothing behind. The round trip of a live worker's documents, tables and files is in
// app_test.go.

var archiveManifest = ArchiveManifest{Format: 2, App: "acme", API: "v1", Address: ArchiveAddress{Kind: "doc", Key: "doc|1"}}

// serverParts are the server's part of the archives: a host data archive of `doc|1` without data, as JSONL.
var serverParts = []any{
	&store.ManifestPart{Format: 1, Schema: 3, LastSeq: 0, NextID: 2, Host: new("doc|1")},
	&store.EndPart{Writes: 0},
}

// serverPart writes the server's part (the NodeHost's, `server.archive`) beside the archives; its path.
func serverPart(t *testing.T, base string) string {
	return serverPartAt(t, filepath.Join(base, "server.jsonl"))
}

// kitServerPart writes the Go host's part (`kit-server.archive`, D462) beside the archives; its path.
func kitServerPart(t *testing.T, base string) string {
	return serverPartAt(t, filepath.Join(base, "kit-server.jsonl"))
}

func serverPartAt(t *testing.T, path string) string {
	t.Helper()
	writeFile(t, path, `{"kind":"manifest","format":1,"schema":3,"lastSeq":0,"nextId":2,"host":"doc|1"}`+"\n"+`{"kind":"end","writes":0}`+"\n")
	return path
}

// partsText is the parts as JSON.
func partsText(t *testing.T, parts []any) string {
	t.Helper()
	text, err := json.Marshal(parts)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// serverTaken is what a server that takes the parts it is handed got from the JSONL file at path.
func serverTaken(t *testing.T, path string) []any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	parts := store.DecodeJsonl(&fileBytes{file: file})
	var taken []any
	for {
		part, ok, err := parts.Next(background)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		taken = append(taken, part)
	}
	if err := parts.Close(); err != nil {
		t.Fatal(err)
	}
	return taken
}

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
	if err := PackData(background, dir, archiveManifest, serverPart(t, base), kitServerPart(t, base), filepath.Join(base, "out.tar")); err != nil {
		t.Fatal(err)
	}
	var seen []ArchiveManifest
	accept := func(manifest ArchiveManifest) error { seen = append(seen, manifest); return nil }
	staging, part, kitPart, err := UnpackStaging(background, filepath.Join(base, "out.tar"), filepath.Join(base, "in"), accept, ImportLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != archiveManifest {
		t.Fatalf("seen = %+v", seen)
	}
	for _, got := range [][]any{serverTaken(t, part), serverTaken(t, kitPart)} {
		if gotText, wantText := partsText(t, got), partsText(t, serverParts); gotText != wantText {
			t.Fatalf("the server got %s, want %s", gotText, wantText)
		}
	}
	if got := dirNames(t, staging); !slices.Equal(got, []string{"files", "kit-server.archive", "server.archive", "store.sqlite"}) {
		t.Fatalf("staging = %v", got)
	}
	if got := dirNames(t, filepath.Join(staging, "files")); !slices.Equal(got, []string{"a", "sub"}) {
		t.Fatalf("staging/files = %v", got)
	}
	if got, _ := os.ReadFile(filepath.Join(staging, "files", "sub", "b")); string(got) != "file b" {
		t.Fatalf("b = %q", got)
	}
	if exportedStore(t, staging) != exportedStore(t, dir) {
		t.Fatal("the store's export differs after the round trip")
	}
}

func TestRefusesToPackAnAddressWithoutData(t *testing.T) {
	base := t.TempDir()
	part := serverPart(t, base)
	wantError(t, PackData(background, filepath.Join(base, "none"), archiveManifest, part, kitServerPart(t, base), filepath.Join(base, "out.tar")), "has no data")
}

func TestRefusesAnArchivePastItsLimitsUnpackedBytesAndEntriesCountedBeforeWriting(t *testing.T) {
	base := t.TempDir()
	big := strings.Repeat("x", 2000)
	head := [][2]string{{"manifest.json", manifestText(t)}, {"store.archive", ""}, {"server.archive", ""}}
	in := filepath.Join(base, "in")
	forged(t, filepath.Join(base, "big.tar"), append(slices.Clone(head), [2]string{"files/a", big}, [2]string{"files/b", big})...)
	_, _, _, err := UnpackStaging(background, filepath.Join(base, "big.tar"), in, func(ArchiveManifest) error { return nil }, ImportLimits{Bytes: 3000})
	wantError(t, err, "unpacks to more than 3000 bytes")
	many := slices.Clone(head)
	for _, name := range []string{"0", "1", "2", "3", "4"} {
		many = append(many, [2]string{"files/" + name, "x"})
	}
	forged(t, filepath.Join(base, "many.tar"), many...)
	_, _, _, err = UnpackStaging(background, filepath.Join(base, "many.tar"), in, func(ArchiveManifest) error { return nil }, ImportLimits{Entries: 4})
	wantError(t, err, "more than 4 entries")
	if got := dirNames(t, in); len(got) != 0 {
		t.Fatalf("in = %v", got)
	}
}

func TestRefusesWhatAcceptRefusesAnArchiveNotStartingWithItsManifestAndAForeignEntryLeavingNothing(t *testing.T) {
	base := t.TempDir()
	if err := PackData(background, hostDirectory(t, base), archiveManifest, serverPart(t, base), kitServerPart(t, base), filepath.Join(base, "out.tar")); err != nil {
		t.Fatal(err)
	}
	in := filepath.Join(base, "in")
	refuse := func(ArchiveManifest) error { return errors.New("not this app") }
	accept := func(ArchiveManifest) error { return nil }
	_, _, _, err := UnpackStaging(background, filepath.Join(base, "out.tar"), in, refuse, ImportLimits{})
	wantError(t, err, "not this app")
	forged(t, filepath.Join(base, "late.tar"), [2]string{"files/x", "x"}, [2]string{"manifest.json", manifestText(t)})
	_, _, _, err = UnpackStaging(background, filepath.Join(base, "late.tar"), in, accept, ImportLimits{})
	wantError(t, err, "start with manifest.json")
	forged(t, filepath.Join(base, "extra.tar"), [2]string{"manifest.json", manifestText(t)}, [2]string{"plugins/evil.ts", "x"})
	_, _, _, err = UnpackStaging(background, filepath.Join(base, "extra.tar"), in, accept, ImportLimits{})
	wantError(t, err, "plugins/evil.ts")
	forged(t, filepath.Join(base, "nostore.tar"), [2]string{"manifest.json", manifestText(t)})
	_, _, _, err = UnpackStaging(background, filepath.Join(base, "nostore.tar"), in, accept, ImportLimits{})
	wantError(t, err, "no manifest.json, store.archive or server.archive")
	if got := dirNames(t, in); len(got) != 0 {
		t.Fatalf("in = %v", got)
	}
}

// Beyond Pi's tests: an archive without its server's part (the pre-change layout: manifest and store.archive only) is refused too.
func TestRefusesAnArchiveWithoutItsServerPart(t *testing.T) {
	base := t.TempDir()
	forged(t, filepath.Join(base, "old.tar"), [2]string{"manifest.json", manifestText(t)}, [2]string{"store.archive", ""})
	_, _, _, err := UnpackStaging(background, filepath.Join(base, "old.tar"), filepath.Join(base, "in"), func(ArchiveManifest) error { return nil }, ImportLimits{})
	wantError(t, err, "no manifest.json, store.archive or server.archive")
}

// Beyond Pi's tests: the entries are written in Pi's order: manifest, store.archive, [kit.archive], server.archive, [kit-server.archive], files/ (the bracketed are PiG's second store, D462).
func TestPackedEntriesAreInPisOrder(t *testing.T) {
	base := t.TempDir()
	dir := hostDirectory(t, base)
	if err := openStoreAt(t, filepath.Join(dir, "kit.sqlite")).Close(background); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(base, "out.tar")
	if err := PackData(background, dir, archiveManifest, serverPart(t, base), kitServerPart(t, base), out); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var names []string
	if err := ReadTar(file, func(entry TarRead) error {
		names = append(names, entry.Path)
		_, err := io.Copy(io.Discard, entry.Data)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"manifest.json", "store.archive", "kit.archive", "server.archive", "kit-server.archive", "files/a", "files/sub/b"}; !slices.Equal(names, want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
}

// Beyond Pi's tests: the manifest is written as JSON.stringify writes it (no HTML escaping, members in order).
func TestTheManifestIsWrittenAsJSONStringifyWritesIt(t *testing.T) {
	manifest := ArchiveManifest{Format: 2, App: "a&b<c>", API: "v1", Address: ArchiveAddress{Kind: "doc", Key: "doc|1"}}
	if got, want := string(manifest.text()), `{"format":2,"app":"a&b<c>","api":"v1","address":{"kind":"doc","key":"doc|1"}}`; got != want {
		t.Fatalf("manifest = %s, want %s", got, want)
	}
}

// A host directory with a kit store (kit.sqlite: the Go kit's Files, beside the plugins' store.sqlite) round-trips both stores; Pi has
// one store per host, so this is the Go kit's own addition.
func TestRoundTripsAHostDirectorysKitStoreBesideItsPluginStore(t *testing.T) {
	base := t.TempDir()
	dir := hostDirectory(t, base)
	if err := openStoreAt(t, filepath.Join(dir, "kit.sqlite")).Close(background); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(base, "out.tar")
	if err := PackData(background, dir, archiveManifest, serverPart(t, base), kitServerPart(t, base), out); err != nil {
		t.Fatal(err)
	}
	staging, _, _, err := UnpackStaging(background, out, filepath.Join(base, "in"), func(ArchiveManifest) error { return nil }, ImportLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if got := dirNames(t, staging); !slices.Equal(got, []string{"files", "kit-server.archive", "kit.sqlite", "server.archive", "store.sqlite"}) {
		t.Fatalf("staging = %v", got)
	}
	if exportedStore(t, staging) != exportedStore(t, dir) {
		t.Fatal("the copied plugin store differs")
	}
}
