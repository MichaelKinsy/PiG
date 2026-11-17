package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// The merge of the two stores' exports into the one archive Pi writes (archive_merge.go), and the split of a host's data archive by the
// owners a host serves (archive_split.go), over archives forged to hold what the two halves must keep apart or put together.

func docRecord(id int, owner, namespace string) string {
	return `{"kind":"document.create","record":{"id":` + strconv.Itoa(id) + `,"owner":"` + owner + `","namespace":"` + namespace + `","kind":"k","history":"latest","createdAt":1,"revision":1,"version":1,"fingerprint":"f"}}`
}

func docRevision(id int) string {
	return `{"kind":"document.revision","id":` + strconv.Itoa(id) + `,"seq":1,"content":{"kind":"base","version":1,"value":{}}}`
}

func tableDef(name string) string {
	return `{"kind":"table","table":"` + name + `","version":1,"state":"ready","columns":{"p":{"type":"str"}},"key":["p"],"fingerprint":"f"}`
}

func putRow(name string) string {
	return `{"kind":"put","table":"` + name + `","row":{"p":"x"},"version":1,"columns":{}}`
}

func archiveText(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

// partsOfText is the typed parts of the JSONL archive text, as a part iterator.
func partsOfText(t *testing.T, text string) store.PartIterator {
	t.Helper()
	path := filepath.Join(t.TempDir(), "forged.archive")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := openParts(path)
	if err != nil {
		t.Fatal(err)
	}
	return partsOf(source)
}

// shape is a line of an archive as the merge's tests compare it: what it is and where it belongs.
func shape(t *testing.T, line string) string {
	t.Helper()
	var part struct {
		Kind    string `json:"kind"`
		Table   string `json:"table"`
		ID      int    `json:"id"`
		Schema  int    `json:"schema"`
		LastSeq int    `json:"lastSeq"`
		NextID  int    `json:"nextId"`
		Writes  int    `json:"writes"`
		Record  struct {
			ID    int    `json:"id"`
			Owner string `json:"owner"`
		} `json:"record"`
	}
	if err := json.Unmarshal([]byte(line), &part); err != nil {
		t.Fatal(err)
	}
	switch part.Kind {
	case "manifest":
		return "manifest schema " + strconv.Itoa(part.Schema) + " lastSeq " + strconv.Itoa(part.LastSeq) + " nextId " + strconv.Itoa(part.NextID)
	case "table", "put":
		return part.Kind + " " + part.Table
	case "document.create":
		return "document " + strconv.Itoa(part.Record.ID) + " of " + part.Record.Owner
	case "document.revision":
		return "revision of " + strconv.Itoa(part.ID)
	case "end":
		return "end " + strconv.Itoa(part.Writes)
	}
	return part.Kind
}

// shapes is the lines of the JSONL file at path, as shape says.
func shapes(t *testing.T, path string) []string {
	t.Helper()
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for line := range strings.SplitSeq(strings.TrimSuffix(string(text), "\n"), "\n") {
		lines = append(lines, shape(t, line))
	}
	return lines
}

// mergedOf merges the plugins' archive with the kit's (forged texts) and writes it to a file: its path.
func mergedOf(t *testing.T, plugins, kit string, kitDocuments int64) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "merged.archive")
	return path, writeParts(background, mergeParts(partsOfText(t, plugins), partsOfText(t, kit), kitDocuments), path)
}

var (
	// pluginsForged and kitForged are two stores' archives with their own allocators (both hand out id 2 and on) and seqs.
	pluginsForged = archiveText(
		`{"kind":"manifest","format":1,"schema":3,"lastSeq":5,"nextId":10}`,
		tableDef("test.p:rows"), putRow("test.p:rows"), tableDef("test.z:rows"),
		docRecord(3, "test.p", "n1"), docRevision(3), docRecord(4, "test.p", "n2"), docRevision(4),
		`{"kind":"end","writes":7}`,
	)
	kitForged = archiveText(
		`{"kind":"manifest","format":1,"schema":2,"lastSeq":7,"nextId":8}`,
		tableDef("chord.app.files:t"), putRow("chord.app.files:t"), putRow("chord.app.files:t"),
		docRecord(2, filesName, "n3"), docRevision(2), docRecord(5, filesName, "n4"), docRevision(5),
		`{"kind":"end","writes":7}`,
	)
)

// The merged archive is Pi's: its manifest has the larger schema and `lastSeq` and a next id that counts the kit's documents on, its tables
// are in `CompareText` order whichever store they came from, the plugins' documents come by id and the kit's after them, numbered on from the
// plugins' next id with their revisions, and its `end` counts what is between.
func TestMergedArchiveHasOnePisShape(t *testing.T) {
	path, err := mergedOf(t, pluginsForged, kitForged, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"manifest schema 3 lastSeq 7 nextId 12",
		"table chord.app.files:t", "put chord.app.files:t", "put chord.app.files:t",
		"table test.p:rows", "put test.p:rows", "table test.z:rows",
		"document 3 of test.p", "revision of 3", "document 4 of test.p", "revision of 4",
		"document 10 of " + filesName, "revision of 10", "document 11 of " + filesName, "revision of 11",
		"end 14",
	}
	if got := shapes(t, path); !slices.Equal(got, want) {
		t.Fatalf("the merged archive is\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Splitting the merged archive by the kit's owners gives the two archives back, up to the kit's document ids.
func TestSplittingAMergedArchiveGivesBothStoresBack(t *testing.T) {
	merged, err := mergedOf(t, pluginsForged, kitForged, 2)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := splitHostData(background, merged, filepath.Join(dir, "kit"), filepath.Join(dir, "plugins"), kitOwned); err != nil {
		t.Fatal(err)
	}
	wantKit := []string{
		"manifest schema 3 lastSeq 7 nextId 12",
		"table chord.app.files:t", "put chord.app.files:t", "put chord.app.files:t",
		"document 10 of " + filesName, "revision of 10", "document 11 of " + filesName, "revision of 11",
		"end 7",
	}
	if got := shapes(t, filepath.Join(dir, "kit")); !slices.Equal(got, wantKit) {
		t.Errorf("the kit's half is\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantKit, "\n"))
	}
	wantPlugins := []string{
		"manifest schema 3 lastSeq 7 nextId 12",
		"table test.p:rows", "put test.p:rows", "table test.z:rows",
		"document 3 of test.p", "revision of 3", "document 4 of test.p", "revision of 4",
		"end 7",
	}
	if got := shapes(t, filepath.Join(dir, "plugins")); !slices.Equal(got, wantPlugins) {
		t.Errorf("the plugins' half is\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantPlugins, "\n"))
	}
}

// An archive of a host's data split by the owners the server's Go host serves: the Go host's half has its owners' tables, rows and
// documents; the rest, the NodeHost's, has the others; the host in the manifest is kept.
func TestAHostDataArchiveIsSplitByTheOwnersTheGoHostServes(t *testing.T) {
	text := archiveText(
		`{"kind":"manifest","format":1,"schema":3,"lastSeq":0,"nextId":6,"host":"doc|1"}`,
		tableDef("test.go:rows"), putRow("test.go:rows"), tableDef("test.node:rows"), putRow("test.node:rows"), putRow("test.node:rows"),
		docRecord(2, "test.go", "n1"), docRevision(2), docRecord(3, "test.node", "n2"), docRevision(3),
		`{"kind":"end","writes":9}`,
	)
	archive := filepath.Join(t.TempDir(), "server.archive")
	if err := os.WriteFile(archive, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := splitHostData(background, archive, filepath.Join(dir, "go"), filepath.Join(dir, "node"), func(owner string) bool { return owner == "test.go" }); err != nil {
		t.Fatal(err)
	}
	wantGo := []string{"manifest schema 3 lastSeq 0 nextId 6", "table test.go:rows", "put test.go:rows", "document 2 of test.go", "revision of 2", "end 4"}
	wantNode := []string{"manifest schema 3 lastSeq 0 nextId 6", "table test.node:rows", "put test.node:rows", "put test.node:rows", "document 3 of test.node", "revision of 3", "end 5"}
	if got := shapes(t, filepath.Join(dir, "go")); !slices.Equal(got, wantGo) {
		t.Errorf("the Go host's half is %v, want %v", got, wantGo)
	}
	if got := shapes(t, filepath.Join(dir, "node")); !slices.Equal(got, wantNode) {
		t.Errorf("the NodeHost's half is %v, want %v", got, wantNode)
	}
	if first, _, _ := strings.Cut(readText(t, filepath.Join(dir, "go")), "\n"); !strings.Contains(first, `"host":"doc|1"`) {
		t.Errorf("the Go host's manifest lost its host: %s", first)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// What a merge cannot make a valid archive of is refused, naming it: a table in both stores, a kit store that holds an owner the import
// would send to the plugins' store, ids that would leave the numbers JavaScript holds.
func TestAMergeRefusesWhatWouldNotBeOneArchive(t *testing.T) {
	t.Run("a table in both stores", func(t *testing.T) {
		both := archiveText(`{"kind":"manifest","format":1,"schema":3,"lastSeq":1,"nextId":2}`, tableDef("test.p:rows"), `{"kind":"end","writes":1}`)
		_, err := mergedOf(t, pluginsForged, both, 0)
		wantError(t, err, "the table test.p:rows is in both stores")
	})
	t.Run("a kit store holding another owner", func(t *testing.T) {
		foreign := archiveText(`{"kind":"manifest","format":1,"schema":3,"lastSeq":1,"nextId":3}`, docRecord(2, "test.p", "n"), `{"kind":"end","writes":1}`)
		_, err := stageParts(background, partsOfText(t, foreign), filepath.Join(t.TempDir(), "kit.part"), kitOwned)
		wantError(t, err, `the kit's store holds records of "test.p"`)
		foreignTable := archiveText(`{"kind":"manifest","format":1,"schema":3,"lastSeq":1,"nextId":3}`, tableDef("test.p:rows"), `{"kind":"end","writes":1}`)
		_, err = stageParts(background, partsOfText(t, foreignTable), filepath.Join(t.TempDir(), "kit.part"), kitOwned)
		wantError(t, err, `the kit's store holds records of "test.p"`)
	})
	t.Run("ids past the safe integers", func(t *testing.T) {
		near := archiveText(`{"kind":"manifest","format":1,"schema":3,"lastSeq":1,"nextId":9007199254740991}`, `{"kind":"end","writes":0}`)
		_, err := mergedOf(t, near, kitForged, 2)
		wantError(t, err, "the stores' ids would pass 9007199254740991")
	})
	t.Run("documents are counted", func(t *testing.T) {
		staged, err := stageParts(background, partsOfText(t, kitForged), filepath.Join(t.TempDir(), "kit.part"), kitOwned)
		if err != nil {
			t.Fatal(err)
		}
		if staged.documents != 2 {
			t.Errorf("the staged kit export has %d documents, want 2", staged.documents)
		}
	})
}
