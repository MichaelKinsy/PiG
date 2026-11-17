package app

import (
	"path/filepath"
	"strings"
	"testing"
)

// The import of a store archive routes its parts by owner (archive_split.go, D462): a Pi archive's one `store.archive` holds the kit's
// tables and documents beside the plugins'. The archives here are forged to hold what the halves must keep apart; the interop test
// (TestImportsAPiMadeArchiveWithItsFilesIndex) is the same import over an archive Pi made.

// splitArchive is a store archive of a kit table with a row and a kit document, and a plugin's table with a row and document.
// kitID and pluginID are the two documents' ids; writes is the `end`'s count (the parts are 8).
func splitArchive(kitID, pluginID, writes string) string {
	record := func(id, owner, namespace string) string {
		return `{"kind":"document.create","record":{"id":` + id + `,"owner":"` + owner + `","namespace":"` + namespace + `","kind":"k","history":"latest","createdAt":1,"revision":1,"version":1,"fingerprint":"f"}}`
	}
	revision := func(id string) string {
		return `{"kind":"document.revision","id":` + id + `,"seq":1,"content":{"kind":"base","version":1,"value":{}}}`
	}
	table := func(name string) string {
		return `{"kind":"table","table":"` + name + `","version":1,"state":"ready","columns":{"p":{"type":"str"}},"key":["p"],"fingerprint":"f"}`
	}
	put := func(name string) string {
		return `{"kind":"put","table":"` + name + `","row":{"p":"x"},"version":1,"columns":{}}`
	}
	return strings.Join([]string{
		`{"kind":"manifest","format":1,"schema":3,"lastSeq":1,"nextId":10}`,
		table("chord.app.files:t"), put("chord.app.files:t"),
		table("test.p:rows"), put("test.p:rows"),
		record(kitID, filesName, "n1"), revision(kitID), record(pluginID, "test.p", "n2"), revision(pluginID),
		`{"kind":"end","writes":` + writes + `}`,
	}, "\n") + "\n"
}

func unpackSplit(t *testing.T, base string, entries ...[2]string) (string, error) {
	t.Helper()
	forged(t, filepath.Join(base, "in.tar"), append([][2]string{{"manifest.json", manifestText(t)}}, entries...)...)
	staging, _, err := UnpackStaging(background, filepath.Join(base, "in.tar"), filepath.Join(base, "in"), func(ArchiveManifest) error { return nil }, ImportLimits{})
	return staging, err
}

const emptyServerPart = `{"kind":"manifest","format":1,"schema":3,"lastSeq":0,"nextId":2,"host":"doc|1"}` + "\n" + `{"kind":"end","writes":0}` + "\n"

func TestUnpackStagingRoutesAStoreArchiveByOwnerKitPartsToKitSqlite(t *testing.T) {
	base := t.TempDir()
	staging, err := unpackSplit(t, base, [2]string{"store.archive", splitArchive("2", "3", "8")}, [2]string{"server.archive", emptyServerPart})
	if err != nil {
		t.Fatal(err)
	}
	kit, plugins := describeStore(t, filepath.Join(staging, kitStore)), describeStore(t, filepath.Join(staging, archiveStore))
	if rows, ok := kit.Tables["chord.app.files:t"]; !ok || rows.Puts != 1 || len(kit.Tables) != 1 || kit.Documents.Creates != 1 || kit.Documents.Revisions != 1 || kit.Writes != 4 {
		t.Errorf("kit.sqlite holds %+v", kit)
	}
	if rows, ok := plugins.Tables["test.p:rows"]; !ok || rows.Puts != 1 || len(plugins.Tables) != 1 || plugins.Documents.Creates != 1 || plugins.Writes != 4 {
		t.Errorf("store.sqlite holds %+v", plugins)
	}
}

func TestUnpackStagingRefusesASplitArchiveWhoseHalvesWouldShareADocumentIdOrMiscount(t *testing.T) {
	for name, c := range map[string]struct{ archive, want string }{
		"a document id twice across the halves": {splitArchive("2", "2", "8"), "id 2 occurs twice"},
		"an end that counts the halves wrongly": {splitArchive("2", "3", "9"), "end counts 9 writes, the archive has 8"},
	} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			_, err := unpackSplit(t, base, [2]string{"store.archive", c.archive}, [2]string{"server.archive", emptyServerPart})
			wantError(t, err, c.want)
			if left := dirNames(t, filepath.Join(base, "in")); len(left) != 0 {
				t.Fatalf("a refused import left %v", left)
			}
		})
	}
}

// The kit's store travels in `store.archive` only, as in Pi: PiG's former `kit.archive` and `kit-server.archive` are entries an address
// archive does not have, refused with Pi's own words (`archive.ts:216`) and leaving nothing.
func TestUnpackStagingRefusesTheFormerKitEntries(t *testing.T) {
	for _, name := range []string{"kit.archive", "kit-server.archive"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			_, err := unpackSplit(t, base, [2]string{"store.archive", splitArchive("2", "3", "8")}, [2]string{name, emptyServerPart}, [2]string{"server.archive", emptyServerPart})
			wantError(t, err, "importData: the archive holds "+name+", which an address archive does not")
			if left := dirNames(t, filepath.Join(base, "in")); len(left) != 0 {
				t.Fatalf("a refused import left %v", left)
			}
		})
	}
}
