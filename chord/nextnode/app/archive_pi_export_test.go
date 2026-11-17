package app

import (
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// D462: an archive PiG made is Pi's archive, and the other way round: Pi's own `unpackStaging` and `importHostData` take it, and what
// either of them makes of it is imported by PiG with every row. PiG's worker has two stores (the plugins' `store.sqlite` and the kit's
// `kit.sqlite`) and its server has two hosts; the archive has one `store.archive` and one `server.archive`, as Pi's has.

// piGProject is a started app with project p1 holding plugin data (a persisted document, table rows, a file with versions in the kit's
// Files index) and the server's records of p1 (the meta plugin's document and two table rows), and the archive an export of p1 made
// with p1's worker stopped. goRow also gives the Go host a row of p1 (an owner Pi's one host knows nothing of).
func piGProject(t *testing.T, goRow bool) (s started, meta *plugins.NodeReference, archive string) {
	t.Helper()
	s = startTestApp(t, map[string]map[string]string{"notes": notesPlugin, "meta": metaPlugin}, projectOptions(t))
	meta, err := s.app.Node.Get(background, metaStore, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(meta.Release)
	metaSet(t, meta, "p1", "first", "red")
	metaSet(t, meta, "p1", "first", "blue")
	address := projectAddress("p1")
	p1 := holdNotes(t, s, "p1")
	write(t, p1, "buy milk")
	for _, call := range [][2]string{{"visit", "home"}, {"visit", "home"}, {"visit", "about"}, {"attach", "report text"}, {"attach", "report text, again"}} {
		if _, err := p1.Call(call[0], call[1], background); err != nil {
			t.Fatal(err)
		}
	}
	p1.Release()
	if err := s.app.Workers["project"].Stop(address, background); err != nil {
		t.Fatal(err)
	}
	if goRow {
		goHostHolds(t, s, address)
	}
	archive = filepath.Join(t.TempDir(), "p1.tar")
	if err := s.app.ExportData(address, archive, background); err != nil {
		t.Fatal(err)
	}
	return s, meta, archive
}

// piManifestOf is the manifest of an archive of address, as Pi's oracle takes it.
func piManifestOf(address contracts.Address) string {
	manifest := piManifest
	manifest.Address = ArchiveAddress{Kind: address.Kind, Key: address.Key}
	return string(manifest.text())
}

// archiveEntry is the bytes of the entry of the tar at archive called name (nil: none).
func archiveEntry(t *testing.T, archive, name string) []byte {
	t.Helper()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var found []byte
	if err := ReadTar(file, func(entry TarRead) error {
		text, err := io.ReadAll(entry.Data)
		if entry.Path == name {
			found = text
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return found
}

// archiveEntries is the names of the entries of the tar at archive, in order, without the files'.
func archiveEntries(t *testing.T, archive string) []string {
	t.Helper()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var names []string
	if err := ReadTar(file, func(entry TarRead) error {
		_, err := io.Copy(io.Discard, entry.Data)
		if entry.Kind != TarDirectory && !strings.HasPrefix(entry.Path, "files/") {
			names = append(names, entry.Path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return names
}

// describeEntry is what the JSONL archive in the entry called name of the tar at archive holds.
func describeEntry(t *testing.T, archive, name string) piDescription {
	t.Helper()
	text := archiveEntry(t, archive, name)
	if text == nil {
		t.Fatalf("%s has no %s", archive, name)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, text, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := openParts(path)
	if err != nil {
		t.Fatal(err)
	}
	return describeParts(t, partsOf(source))
}

// dumpHost is everything the host directory dir's two stores hold, as text per table (its definition, indexes and rows) and per document
// (its address and its revisions' contents), the allocator's ids and the seqs left out: what an import into another store must keep.
func dumpHost(t *testing.T, dir string) map[string]string {
	t.Helper()
	dump := map[string]string{}
	rows := map[string][]string{}
	for _, name := range []string{archiveStore, kitStore} {
		path := filepath.Join(dir, name)
		if !Exists(path) {
			continue
		}
		opened := openStoreAt(t, path)
		exported, err := opened.Export(background)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[int64]string{}
		for {
			part, ok, err := exported.Next(background)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
			switch part := part.(type) {
			case *store.TableDefinitionWrite:
				rows["table "+part.Table] = append(rows["table "+part.Table], jsonOf(t, part))
			case *store.IndexWrite:
				rows["table "+part.Table] = append(rows["table "+part.Table], jsonOf(t, part))
			case *store.PutWrite:
				rows["table "+part.Table] = append(rows["table "+part.Table], jsonOf(t, part))
			case *store.DocumentCreate:
				key := ""
				if part.Record.Key != nil {
					key = *part.Record.Key
				}
				ids[part.Record.ID] = "document " + part.Record.Owner + "|" + part.Record.Namespace + "|" + part.Record.Kind + "|" + key + "|" + string(part.Record.History)
				rows[ids[part.Record.ID]] = append(rows[ids[part.Record.ID]], "")
			case *store.DocumentRevisionWrite:
				rows[ids[part.ID]] = append(rows[ids[part.ID]], jsonOf(t, part.Content))
			}
		}
		if err := errors.Join(exported.Close(), opened.Close(background)); err != nil {
			t.Fatal(err)
		}
	}
	for name, lines := range rows {
		if strings.HasPrefix(name, "table ") {
			slices.Sort(lines)
		}
		dump[name] = strings.Join(lines, "\n")
	}
	return dump
}

func sameDump(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	for name, text := range want {
		have, ok := got[name]
		if !ok {
			t.Errorf("%s: %s is missing", what, name)
		} else if have != text {
			t.Errorf("%s: %s differs:\n got %s\nwant %s", what, name, have, text)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s: %s is not in the original", what, name)
		}
	}
	if len(want) == 0 {
		t.Errorf("%s: the original holds nothing", what)
	}
}

// An archive PiG made is accepted by Pi's own `unpackStaging` and holds the same rows once Pi has imported it: one format, Pi's. (PiG's
// former `kit.archive` and `kit-server.archive` entries are refused by Pi's `unpackEntries`, `node/app/archive.ts:216`: "the archive
// holds X, which an address archive does not".)
func TestPisOwnUnpackStagingAcceptsAPiGArchive(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	address := projectAddress("p1")
	p1 := holdNotes(t, s, "p1")
	write(t, p1, "buy milk")
	for _, call := range [][2]string{{"visit", "home"}, {"visit", "home"}, {"visit", "about"}, {"attach", "report text"}} {
		if _, err := p1.Call(call[0], call[1], background); err != nil {
			t.Fatal(err)
		}
	}
	p1.Release()
	if err := s.app.Workers["project"].Stop(address, background); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "p1.tar")
	if err := s.app.ExportData(address, archive, background); err != nil {
		t.Fatal(err)
	}
	piManifest := piManifest
	piManifest.Address = ArchiveAddress{Kind: address.Kind, Key: address.Key}
	pi := runPiOracle(t, "unpack", archive, t.TempDir(), string(piManifest.text()))
	if pi.Error != "" {
		t.Fatalf("Pi's unpackStaging refused the archive PiG made: %s", pi.Error)
	}
	sameRows(t, "in Pi's staging store", pi, describeHost(t, WorkerDirectory(s.dataDir, address)))
	if !slices.Equal(pi.StagingEntries, []string{"files", "server.archive", "store.sqlite"}) {
		t.Errorf("Pi's staging directory holds %v, want files, server.archive and store.sqlite", pi.StagingEntries)
	}
}

// The server's part of an archive PiG made (`server.archive`: the records and rows belonging to the address) is what Pi's own
// `importHostData` takes: Pi's host imports it and Pi's export of the address holds the rows the archive held. The Go host's row of the
// address (an owner Pi's one host does not serve) travels in the same `server.archive`.
func TestPisOwnImportOfHostDataAcceptsAPiGArchivesServerPart(t *testing.T) {
	_, _, archive := piGProject(t, true)
	pi := runPiOracle(t, "hostdata", archive, t.TempDir(), piManifestOf(projectAddress("p1")), "p1")
	if pi.Error != "" {
		t.Fatalf("Pi's importHostData refused the server part PiG made: %s", pi.Error)
	}
	if got, want := archiveEntries(t, archive), []string{"manifest.json", "store.archive", "server.archive"}; !slices.Equal(got, want) {
		t.Fatalf("the archive PiG made holds %v, want %v (Pi's)", got, want)
	}
	want := describeEntry(t, archive, "server.archive")
	sameRows(t, "Pi's host after importing the server part", pi, want)
	if rows := pi.Tables["test.meta-store:test.labels"].Puts; rows != 2 {
		t.Errorf("Pi's host holds %d rows of test.labels of p1, want 2", rows)
	}
	if rows := pi.Tables["test.go:rows"].Puts; rows != 1 {
		t.Errorf("Pi's host holds %d rows of the Go host's table, want 1", rows)
	}
	if pi.Documents.Creates != 1 {
		t.Errorf("Pi's host holds %d documents of p1, want the meta plugin's 1", pi.Documents.Creates)
	}
}

// servingHost is the app's Go host serving more owners (Serves is what routes an archive's server part): the Go host row of goHostHolds
// is an owner of the Go host, as an embedder's host-keyed facet would be.
type servingHost struct {
	host.Host
	owners []string
}

func (h *servingHost) Serves(name string) bool {
	return slices.Contains(h.owners, name) || h.Host.Serves(name)
}

// PiG -> PiG: an archive PiG made, imported by PiG as another address, leaves the worker's two stores and the server's records and rows
// of the copy as the original's, table by table and document by document (everything but the allocator's ids and the seqs).
func TestAPiGArchiveRoundTripsThroughPiGWithEverythingTheStoresHold(t *testing.T) {
	s, meta, archive := piGProject(t, true)
	s.app.Host = &servingHost{Host: s.app.Host, owners: []string{"test.go"}}
	if err := s.app.ImportData(archive, projectAddress("p2"), background); err != nil {
		t.Fatal(err)
	}
	original := dumpHost(t, WorkerDirectory(s.dataDir, projectAddress("p1")))
	sameDump(t, "the copy's stores", dumpHost(t, WorkerDirectory(s.dataDir, projectAddress("p2"))), original)
	for _, table := range []string{"table chord.app.files:files", "table test.store:test.visits"} {
		if !strings.Contains(original[table], `"put"`) {
			t.Errorf("the original holds no rows of %s: the comparison proves nothing", table)
		}
	}
	if title, labels := metaGet(t, meta, "p2"); title != "first" || !slices.Equal(slices.Sorted(slices.Values(labels)), []string{"blue", "red"}) {
		t.Errorf("p2's meta = %q %v, want p1's", title, labels)
	}
	goRows := func(address contracts.Address) int64 {
		exported, err := s.app.Host.ExportHostData(address, background)
		if err != nil {
			t.Fatal(err)
		}
		return describeParts(t, exported).Tables["test.go:rows"].Puts
	}
	if got, want := goRows(projectAddress("p2")), goRows(projectAddress("p1")); got != want || want != 1 {
		t.Errorf("the Go host holds %d rows of p2, %d of p1, want 1 of each", got, want)
	}
}

// Pi -> PiG -> Pi: an archive Pi made, imported by PiG and exported by PiG again, is accepted by Pi's own `unpackStaging` and holds the
// rows Pi's own export of the host held.
func TestAPiMadeArchiveRoundTripsThroughPiGBackIntoPi(t *testing.T) {
	archive, pi := piPacks(t)
	s := startTestApp(t, nil, projectOptions(t))
	address := projectAddress("p9")
	if err := s.app.ImportData(archive, address, background); err != nil {
		t.Fatal(err)
	}
	again := filepath.Join(t.TempDir(), "again.tar")
	if err := s.app.ExportData(address, again, background); err != nil {
		t.Fatal(err)
	}
	back := runPiOracle(t, "unpack", again, t.TempDir(), piManifestOf(address))
	if back.Error != "" {
		t.Fatalf("Pi's unpackStaging refused the archive PiG exported of Pi's: %s", back.Error)
	}
	sameRows(t, "in Pi's staging store", back, pi)
	for name, table := range pi.Tables {
		if got := back.Tables[name]; got.Fingerprint != table.Fingerprint || got.Version != table.Version || !maps.EqualFunc(got.Indexes, table.Indexes, slices.Equal) {
			t.Errorf("table %s came back as %+v, Pi's was %+v", name, got, table)
		}
	}
}

// PiG -> Pi -> PiG: an archive PiG made, taken and packed again by Pi's own `unpackStaging` and `packData`, is imported by PiG with every
// row the original held.
func TestAPiGArchiveRoundTripsThroughPiBackIntoPiG(t *testing.T) {
	s, meta, archive := piGProject(t, false)
	repacked := filepath.Join(t.TempDir(), "repacked.tar")
	pi := runPiOracle(t, "repack", archive, t.TempDir(), piManifestOf(projectAddress("p1")), repacked)
	if pi.Error != "" {
		t.Fatalf("Pi's unpackStaging refused the archive PiG made: %s", pi.Error)
	}
	if err := s.app.ImportData(repacked, projectAddress("p2"), background); err != nil {
		t.Fatal(err)
	}
	original := dumpHost(t, WorkerDirectory(s.dataDir, projectAddress("p1")))
	sameDump(t, "the copy of Pi's repack", dumpHost(t, WorkerDirectory(s.dataDir, projectAddress("p2"))), original)
	if title, labels := metaGet(t, meta, "p2"); title != "first" || len(labels) != 2 {
		t.Errorf("p2's meta = %q %v, want p1's", title, labels)
	}
}
