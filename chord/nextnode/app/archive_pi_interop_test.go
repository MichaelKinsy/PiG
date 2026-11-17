package app

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// D462: one archive format, Pi's, both ways. These tests run Pi's own compiled chord package (the pin's build, testdata/pi_archive_oracle.mjs:
// `packData`, `unpackStaging`, `filesFacet`, the store's export) over a host directory and over an archive PiG made; no archive here is
// built by hand. A Pi host has ONE store; PiG's worker has two (the NodeHost's `store.sqlite` for plugins and the Go kit's `kit.sqlite`
// for Files), so an archive Pi made holds the kit's `chord.app.files:*` tables and Files documents beside the plugins' records.

// piManifest is the manifest of the archives these tests make and take: the app of testOptions and a project address.
var piManifest = ArchiveManifest{Format: 2, App: "chord-test", API: "v1", Address: ArchiveAddress{Kind: "project", Key: "project|p9"}}

// piTable is one table of a store's export: how many rows it holds and how it is defined.
type piTable struct {
	Puts        int64               `json:"puts"`
	Version     int                 `json:"version"`
	Fingerprint string              `json:"fingerprint"`
	Key         []string            `json:"key"`
	Indexes     map[string][]string `json:"indexes"`
}

// piDescription is what Pi's own export of a store holds (or, for the Go stores, what ours holds): per table its rows, the documents'
// records and revisions, the archive's own count of its writes.
type piDescription struct {
	Tables    map[string]piTable `json:"tables"`
	Documents struct {
		Creates   int64 `json:"creates"`
		Revisions int64 `json:"revisions"`
	} `json:"documents"`
	Writes int64 `json:"writes"`
	Files  struct {
		Report      []string          `json:"report"`
		Texts       map[string]string `json:"texts"`
		UploadsDocs int               `json:"uploadsDocs"`
		Summary     struct {
			Files    float64 `json:"files"`
			Versions float64 `json:"versions"`
		} `json:"summary"`
	} `json:"files"`
	StagingEntries []string `json:"stagingEntries"`
	Error          string   `json:"error"`
}

// runPiOracle runs testdata/pi_archive_oracle.mjs under the qualified Node, scratch homes set, and decodes its answer.
func runPiOracle(t *testing.T, mode string, args ...string) piDescription {
	t.Helper()
	node := testenv.RequireQualifiedNode(t)
	project := pioracle.Project(t)
	if _, err := os.Stat(filepath.Join(project, "node_modules", "@earendil-works", "chord", "dist", "node", "app", "archive.js")); err != nil {
		pioracle.Missing(t, "Pi's built chord package (make durable-interop-deps)", err)
	}
	scratch := t.TempDir()
	command := exec.CommandContext(t.Context(), node, append([]string{"--disable-warning=ExperimentalWarning", "testdata/pi_archive_oracle.mjs", mode, project}, args...)...)
	command.Env = append(os.Environ(), "HOME="+scratch, "PIG_HOME="+scratch, "PI_HOME="+scratch, "PIG_CODING_AGENT_DIR="+scratch, "PI_CODING_AGENT_DIR="+scratch)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("Pi oracle %s: %v\n%s", mode, err, &stderr)
	}
	var described piDescription
	if err := json.Unmarshal(out, &described); err != nil {
		t.Fatalf("Pi oracle %s: %v\n%s", mode, err, out)
	}
	return described
}

// piPacks has Pi make an archive of a host directory with Files data (versions, a copy, folders) and plugin records (a document, a
// table): the archive's path and what Pi's export of that store held.
func piPacks(t *testing.T) (string, piDescription) {
	t.Helper()
	work := t.TempDir()
	described := runPiOracle(t, "pack", work, string(piManifest.text()))
	return filepath.Join(work, "pi.tar"), described
}

// describeStore is what the export of the store file at path holds (nothing for a file that is not there).
func describeStore(t *testing.T, path string) piDescription {
	t.Helper()
	described := piDescription{Tables: map[string]piTable{}}
	if !Exists(path) {
		return described
	}
	opened := openStoreAt(t, path)
	defer func() {
		if err := opened.Close(background); err != nil {
			t.Fatal(err)
		}
	}()
	exported, err := opened.Export(background)
	if err != nil {
		t.Fatal(err)
	}
	return describeParts(t, exported)
}

// describeParts is what the stream of archive parts holds; it closes the stream.
func describeParts(t *testing.T, parts store.PartIterator) piDescription {
	t.Helper()
	described := piDescription{Tables: map[string]piTable{}}
	defer func() { _ = parts.Close() }()
	for {
		part, ok, err := parts.Next(background)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return described
		}
		switch part := part.(type) {
		case *store.TableDefinitionWrite:
			described.Tables[part.Table] = piTable{Version: part.Version, Fingerprint: part.Fingerprint, Key: part.Key, Indexes: map[string][]string{}}
		case *store.IndexWrite:
			described.Tables[part.Table].Indexes[part.Name] = part.Columns
		case *store.PutWrite:
			table := described.Tables[part.Table]
			table.Puts++
			described.Tables[part.Table] = table
		case *store.DocumentCreate:
			described.Documents.Creates++
		case *store.DocumentRevisionWrite:
			described.Documents.Revisions++
		case *store.EndPart:
			described.Writes = part.Writes
		}
	}
}

// describeHost is the two stores of the host directory dir (the plugins' `store.sqlite` and the kit's `kit.sqlite`) as one: a table in
// both is an error, as one Pi store has each table once.
func describeHost(t *testing.T, dir string) piDescription {
	t.Helper()
	both := piDescription{Tables: map[string]piTable{}}
	for _, name := range []string{archiveStore, kitStore} {
		half := describeStore(t, filepath.Join(dir, name))
		for table, rows := range half.Tables {
			if _, twice := both.Tables[table]; twice {
				t.Errorf("table %s is in both stores of %s", table, dir)
			}
			both.Tables[table] = rows
		}
		both.Documents.Creates += half.Documents.Creates
		both.Documents.Revisions += half.Documents.Revisions
		both.Writes += half.Writes
	}
	return both
}

// sameRows reports what differs between two descriptions' tables, rows and documents (what an archive carries).
func sameRows(t *testing.T, what string, got, want piDescription) {
	t.Helper()
	for table, rows := range want.Tables {
		if have, ok := got.Tables[table]; !ok {
			t.Errorf("%s: table %s (%d rows in Pi's store) is in no store", what, table, rows.Puts)
		} else if have.Puts != rows.Puts {
			t.Errorf("%s: table %s holds %d rows, Pi's store %d", what, table, have.Puts, rows.Puts)
		}
	}
	for table := range got.Tables {
		if _, ok := want.Tables[table]; !ok {
			t.Errorf("%s: table %s is not in Pi's store", what, table)
		}
	}
	if got.Documents != want.Documents {
		t.Errorf("%s: documents %+v, Pi's store %+v", what, got.Documents, want.Documents)
	}
	if got.Writes != want.Writes {
		t.Errorf("%s: the stores' archives count %d writes, Pi's %d", what, got.Writes, want.Writes)
	}
}

// A Pi host directory with Files data and plugin records, packed by Pi's own `packData`, is imported by PiG with every row: the Files
// index reaches `kit.sqlite`, which the Go `Files` facet reads (Pi splits by the same owner in `files-copy.ts:46,69-86`:
// `table.startsWith("chord.app.files:")`, `record.owner === OWNER`), and the plugins' records reach `store.sqlite`; each table's rows are
// the number Pi's own export of the host counted.
func TestImportsAPiMadeArchiveWithItsFilesIndex(t *testing.T) {
	archive, pi := piPacks(t)
	s := startTestApp(t, nil, projectOptions(t))
	address := projectAddress("p9")
	if err := s.app.ImportData(archive, address, background); err != nil {
		t.Fatal(err)
	}
	dir := WorkerDirectory(s.dataDir, address)
	sameRows(t, "after the import", describeHost(t, dir), pi)
	files := filesOf(t, fixtureOptions{Dir: dir, Store: kitStore})
	defer files.close()
	var ids []string
	for _, item := range files.must("versions", "space", "artifacts", "path", "report").(*delta.JsonObject).Value("items").([]any) {
		ids = append(ids, str(item.(*delta.JsonObject), "id"))
	}
	if !slices.Equal(ids, pi.Files.Report) {
		t.Fatalf("the Go Files facet lists the versions of artifacts/report as %v, Pi's host had %v", ids, pi.Files.Report)
	}
	for id, text := range pi.Files.Texts {
		if got := files.text(id); got != text {
			t.Errorf("version %s reads %q, Pi's host had %q", id, got, text)
		}
	}
	if page := files.must("list", "space", "uploads", "folder", "docs").(*delta.JsonObject); len(page.Value("items").([]any)) != pi.Files.UploadsDocs {
		t.Errorf("uploads/docs lists %s, want %d entry", jsonOf(t, page), pi.Files.UploadsDocs)
	}
	if summary := files.summary("artifacts"); num(summary, "files") != pi.Files.Summary.Files || num(summary, "versions") != pi.Files.Summary.Versions {
		t.Errorf("the summary of artifacts = %s, Pi's host had %+v", jsonOf(t, summary), pi.Files.Summary)
	}
}
