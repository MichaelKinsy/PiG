package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/test/node/app/app.test.ts "export and import per address" (Pi 8a5da3ce6), the parts about the server's records
// and rows belonging to an address: copied, refused, replaced and deleted with it, and the order of an import and a delete around the
// server's commit. The server's data is two parts here (D462): the NodeHost's (a plugin's documents and rows, `server.archive`) and the
// Go host's (`kit-server.archive`); the NodeHost's part is Pi's TypeScript at the 1.5.2 pin, reached through the process's control requests (plugins.NodeHost.ExportHostData
// and its siblings).

// metaStore is the Go form of the meta plugin's server object (`MetaStore`; contracts are data).
var metaStore = mustLocal("test.meta-store", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("set", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("title", contracts.Str()), contracts.Prop("label", contracts.Str())))),
		contracts.Mem("get", contracts.NewMethod(contracts.Str(), contracts.Obj(contracts.Prop("title", contracts.Str()), contracts.Prop("labels", contracts.Arr(contracts.Str()))))),
	},
})

// metaPlugin is the meta plugin (`META_PLUGIN`): a server plugin keeping data about projects, a document and table rows keyed by the
// project's address.
var metaPlugin = map[string]string{
	"server.ts": strings.Join([]string{
		`import { arr, defineFacet, defineState, defineTable, eq, host, hostKind, local, method, obj, query, str } from "@test/app/v1";`,
		`const Project = hostKind("project", { id: str() });`,
		`const Meta = defineState("test.meta", { schema: obj({ title: str() }), version: 1, initial: () => ({ title: "" }), keyed: Project, persist: true, checkpointWhen: () => false });`,
		`const Labels = defineTable("test.labels", { version: 1, columns: { project: host(Project), label: str() }, key: ["project", "label"] });`,
		`const MetaStore = local("test.meta-store", { version: 1, params: {}, members: { set: method(obj({ id: str(), title: str(), label: str() })), get: method(str(), obj({ title: str(), labels: arr(str()) })) } });`,
		`export default defineFacet((h) => h.serve(MetaStore, (_params, self) => {`,
		`	const meta = self.state(Meta);`,
		`	const labels = self.table(Labels);`,
		`	return {`,
		`		set: async ({ id, title, label }, ctx) => { await self.tx(async (tx) => { (await tx.draft(meta, Project({ id }))).title = title; tx.put(labels, { project: Project({ id }), label }); }, ctx); return null; },`,
		`		get: async (id, ctx) => ({ title: (await meta.get(Project({ id }), ctx))?.title ?? "", labels: (await labels.page(query(Labels).where((t) => eq(t.project, Project({ id }))), 10, ctx)).items.map((row) => row.label) }),`,
		`	};`,
		`}));`,
	}, "\n"),
}

func projectAddress(id string) contracts.Address {
	return project.Address(delta.JsonObjectOf("id", id))
}

func metaSet(t *testing.T, meta *plugins.NodeReference, id, title, label string) {
	t.Helper()
	if _, err := meta.Call("set", delta.JsonObjectOf("id", id, "title", title, "label", label), background); err != nil {
		t.Fatal(err)
	}
}

// metaGet is the title and the labels the meta plugin holds of project id.
func metaGet(t *testing.T, meta *plugins.NodeReference, id string) (string, []string) {
	t.Helper()
	got, err := meta.Call("get", id, background)
	if err != nil {
		t.Fatal(err)
	}
	object := got.(*delta.JsonObject)
	var labels []string
	for _, label := range object.Value("labels").([]any) {
		labels = append(labels, label.(string))
	}
	return str(object, "title"), labels
}

func TestAppCarriesTheServersRecordsAndRowsOfTheAddressCopiedRefusedReplacedAndDeletedWithIt(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin, "meta": metaPlugin}, projectOptions(t))
	// A local type a plugin serves is the NodeHost's (Pi's `app.host.get`: one host; D462, as the echo plugin's test gets it).
	meta, err := s.app.Node.Get(background, metaStore, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Release()
	p1 := holdNotes(t, s, "p1")
	write(t, p1, "buy milk")
	p1.Release()
	metaSet(t, meta, "p1", "first", "red")
	archive := filepath.Join(t.TempDir(), "p1.tar")
	if err := s.app.ExportData(projectAddress("p1"), archive, background); err != nil {
		t.Fatal(err)
	}
	// A copy under p2: the worker's data and the server's, rewritten to p2.
	if err := s.app.ImportData(archive, projectAddress("p2"), background); err != nil {
		t.Fatal(err)
	}
	if title, labels := metaGet(t, meta, "p2"); title != "first" || !slices.Equal(labels, []string{"red"}) {
		t.Fatalf("p2 meta = %q %v", title, labels)
	}
	// p3 has server data only: refused by the server, no directory left.
	metaSet(t, meta, "p3", "other", "blue")
	projects := filepath.Join(s.dataDir, "workers", "project")
	if err := s.app.ImportData(archive, projectAddress("p3"), background); err == nil || !strings.Contains(err.Error(), "project|p3 has data in this store") {
		t.Fatalf("import over server data: %v", err)
	}
	if got := dirEntries(t, projects); !slices.Equal(got, []string{"project%7Cp1", "project%7Cp2"}) {
		t.Fatalf("workers = %v", got)
	}
	// Replaced explicitly: the worker's directory and the server's data.
	metaSet(t, meta, "p2", "changed", "green")
	wantError(t, s.app.ImportData(archive, projectAddress("p2"), background), "exists already")
	if err := s.app.ImportData(archive, projectAddress("p2"), background, DataImportOptions{Existing: store.ExistingReplace}); err != nil {
		t.Fatal(err)
	}
	if title, labels := metaGet(t, meta, "p2"); title != "first" || !slices.Equal(labels, []string{"red"}) {
		t.Fatalf("p2 meta after replace = %q %v", title, labels)
	}
	copied := holdNotes(t, s, "p2")
	if got := member(t, copied, "text"); got != "buy milk" {
		t.Fatalf("copy text = %v", got)
	}
	copied.Release()
	// Deleted: refused while its worker runs; then the directory and the server's data go.
	wantError(t, s.app.DeleteData(projectAddress("p2"), background), "stop its worker first")
	if err := s.app.Workers["project"].Stop(projectAddress("p2"), background); err != nil {
		t.Fatal(err)
	}
	if err := s.app.DeleteData(projectAddress("p2"), background); err != nil {
		t.Fatal(err)
	}
	if title, labels := metaGet(t, meta, "p2"); title != "" || len(labels) != 0 {
		t.Fatalf("p2 meta after delete = %q %v", title, labels)
	}
	if got := dirEntries(t, projects); !slices.Equal(got, []string{"project%7Cp1"}) {
		t.Fatalf("workers = %v", got)
	}
	if title, labels := metaGet(t, meta, "p1"); title != "first" || !slices.Equal(labels, []string{"red"}) {
		t.Fatalf("p1 meta = %q %v", title, labels)
	}
}

// refusingNode is the NodeHost's host data operations failing with err while it is set (Pi's test replaces `importHostData` and
// `deleteHostData` of the host; the NodeHost's are the ones behind the app's `nodeData`, a Go seam for the replacement).
type refusingNode struct {
	nodeHostData
	err error
	// deleteErr fails DeleteHostData only (a rollback that fails).
	deleteErr error
	asked     int
}

func (n *refusingNode) ImportHostData(address contracts.Address, path, existing string, ctx context.Context) error {
	if n.err != nil {
		n.asked++
		return n.err
	}
	return n.nodeHostData.ImportHostData(address, path, existing, ctx)
}

func (n *refusingNode) DeleteHostData(address contracts.Address, ctx context.Context) error {
	if n.deleteErr != nil {
		return n.deleteErr
	}
	if n.err != nil {
		n.asked++
		return n.err
	}
	return n.nodeHostData.DeleteHostData(address, ctx)
}

func TestAppOrdersAnImportAndADeleteAroundTheServersCommitUndoneBeforeItKeptAfterAnUnknownOutcome(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin, "meta": metaPlugin}, projectOptions(t))
	projects := filepath.Join(s.dataDir, "workers", "project")
	textOf := func(id string) string {
		notes := holdNotes(t, s, id)
		text := member(t, notes, "text").(string)
		notes.Release()
		if err := s.app.Workers["project"].Stop(projectAddress(id), background); err != nil {
			t.Fatal(err)
		}
		return text
	}
	for _, id := range []string{"p1", "p2"} {
		notes := holdNotes(t, s, id)
		write(t, notes, "notes of "+id)
		notes.Release()
		if err := s.app.Workers["project"].Stop(projectAddress(id), background); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(t.TempDir(), "p1.tar")
	if err := s.app.ExportData(projectAddress("p1"), archive, background); err != nil {
		t.Fatal(err)
	}
	real := s.app.nodeData
	refusing := &refusingNode{nodeHostData: real}
	s.app.nodeData = refusing
	// The server refuses: the renames are undone, the old directory is back, nothing is left beside it.
	refusing.err = data.Invalid(data.ReasonArgs, "refused by the server")
	wantError(t, s.app.ImportData(archive, projectAddress("p2"), background, DataImportOptions{Existing: store.ExistingReplace}), "refused by the server")
	if got := dirEntries(t, projects); !slices.Equal(got, []string{"project%7Cp1", "project%7Cp2"}) {
		t.Fatalf("workers after the refusal = %v", got)
	}
	if got := textOf("p2"); got != "notes of p2" {
		t.Fatalf("p2 after the refusal = %q", got)
	}
	// An unknown outcome: nothing is undone (the new directory in place, the old one aside), and it says so.
	refusing.err = data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Reason: data.ReasonStorageFailed, Message: "store: storage failed"}, nil)
	err := s.app.ImportData(archive, projectAddress("p2"), background, DataImportOptions{Existing: store.ExistingReplace})
	wantError(t, err, "unknown outcome; nothing was undone")
	var aside []string
	for _, name := range dirEntries(t, projects) {
		if strings.Contains(name, ".aside-") {
			aside = append(aside, name)
		}
	}
	if len(aside) != 1 {
		t.Fatalf("aside directories = %v", aside)
	}
	if !strings.Contains(err.Error(), "the old one at "+filepath.Join(projects, aside[0])) {
		t.Fatalf("the error does not name the aside directory: %v", err)
	}
	refusing.err = nil
	if got := textOf("p2"); got != "notes of p1" {
		t.Fatalf("p2 after the unknown outcome = %q", got)
	}
	// A delete commits on the server first: refused there, the directory stays.
	refusing.err = data.Invalid(data.ReasonArgs, "refused by the server")
	wantError(t, s.app.DeleteData(projectAddress("p1"), background), "refused by the server")
	if got := dirEntries(t, projects); !slices.Contains(got, "project%7Cp1") {
		t.Fatalf("workers after the refused delete = %v", got)
	}
	refusing.err = nil
	// A directory whose server part is gone already (a crash between the two): a repeated delete removes it.
	if err := real.DeleteHostData(projectAddress("p1"), background); err != nil {
		t.Fatal(err)
	}
	if _, err := s.app.Host.DeleteHostData(projectAddress("p1"), background); err != nil {
		t.Fatal(err)
	}
	if err := s.app.DeleteData(projectAddress("p1"), background); err != nil {
		t.Fatal(err)
	}
	if got := dirEntries(t, projects); slices.Contains(got, "project%7Cp1") {
		t.Fatalf("workers after the repeated delete = %v", got)
	}
	if refusing.asked != 3 {
		t.Fatalf("the NodeHost's part was asked to fail %d times, want 3", refusing.asked)
	}
}

func TestAppRefusesAnArchiveOfFormat1WithPisMessage(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	old := ArchiveManifest{Format: 1, App: "chord-test", API: "v1", Address: ArchiveAddress{Kind: "project", Key: "project|p1"}}
	archive := filepath.Join(t.TempDir(), "old.tar")
	forged(t, archive, [2]string{"manifest.json", string(old.text())}, [2]string{"store.archive", ""})
	wantError(t, s.app.ImportData(archive, projectAddress("p2"), background), "importData: not a format 2 archive")
	if got := dirEntries(t, filepath.Join(s.dataDir, "workers", "project")); len(got) != 0 {
		t.Fatalf("workers = %v", got)
	}
}

func TestAppRefusesAnArchiveOfAnotherKindOfAddress(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	other := ArchiveManifest{Format: 2, App: "chord-test", API: "v1", Address: ArchiveAddress{Kind: "team", Key: "team|t1"}}
	archive := filepath.Join(t.TempDir(), "team.tar")
	forged(t, archive, [2]string{"manifest.json", string(other.text())}, [2]string{"store.archive", ""}, [2]string{"server.archive", ""})
	wantError(t, s.app.ImportData(archive, projectAddress("p2"), background), "importData: an archive of a team, not a project")
}

func TestAppRefusesAnExistingDirectoryUnlessReplacedBeforeUnpackingAnything(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	if err := os.MkdirAll(filepath.Join(s.dataDir, "workers", "project", "project%7Cp2"), 0o700); err != nil {
		t.Fatal(err)
	}
	wantError(t, s.app.ImportData(filepath.Join(t.TempDir(), "none.tar"), projectAddress("p2"), background), "exists already")
}

// unknownImportError keeps the commit's rejection as its cause, not in its message.
func TestUnknownImportErrorKeepsTheCommitsRejectionAsItsCause(t *testing.T) {
	cause := errors.New("storage")
	err := (&App{}).unknownImport(projectAddress("p2"), "", cause)
	if !errors.Is(err, cause) || strings.Contains(err.Error(), "storage") {
		t.Fatalf("err = %v", err)
	}
	want := `importData: project|p2: the server's commit has an unknown outcome; nothing was undone (the new directory is in place). Run the import again with existing: "replace" once the server's store is back.`
	if err.Error() != want {
		t.Fatalf("message = %q, want %q", err.Error(), want)
	}
}

// goHost is the app's Go host with its host data operations watched or failing.
type goHost struct {
	host.Host
	importErr error
	deleted   []string
}

func (h *goHost) ImportHostData(parts store.PartSource, ctx context.Context, options store.HostImportOptions) (store.HostDataCounts, error) {
	if h.importErr != nil {
		return store.HostDataCounts{}, h.importErr
	}
	return h.Host.ImportHostData(parts, ctx, options)
}

func (h *goHost) DeleteHostData(address contracts.Address, ctx context.Context) (store.HostDataCounts, error) {
	h.deleted = append(h.deleted, address.Key)
	return h.Host.DeleteHostData(address, ctx)
}

// exportedProject exports project p1 (with notes) of a started app: its archive's path.
func exportedProject(t *testing.T, s started) string {
	t.Helper()
	notes := holdNotes(t, s, "p1")
	write(t, notes, "notes of p1")
	notes.Release()
	if err := s.app.Workers["project"].Stop(projectAddress("p1"), background); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "p1.tar")
	if err := s.app.ExportData(projectAddress("p1"), archive, background); err != nil {
		t.Fatal(err)
	}
	return archive
}

// Beyond Pi's tests (D462: two commits where Pi has one): the NodeHost's commit stood and the Go host's definitely failed. The import
// ends as Pi's undo flow leaves a refused one: the old directory back, the new one gone, the NodeHost's data of the address as before
// (a refused import deletes what it put; a replace puts the old part back).
func TestAppUndoesTheNodeHostsCommitWhenTheGoHostsFailsDefinitely(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin, "meta": metaPlugin}, projectOptions(t))
	meta, err := s.app.Node.Get(background, metaStore, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Release()
	archive := exportedProject(t, s)
	metaSet(t, meta, "p1", "first", "red")
	if err := s.app.ExportData(projectAddress("p1"), archive, background); err != nil {
		t.Fatal(err)
	}
	projects := filepath.Join(s.dataDir, "workers", "project")
	failing := &goHost{Host: s.app.Host, importErr: data.Invalid(data.ReasonArgs, "refused by the Go host")}
	s.app.Host = failing
	// Refuse: a new address. The NodeHost's import is deleted again, the new directory is gone.
	err = s.app.ImportData(archive, projectAddress("p3"), background)
	wantError(t, err, "refused by the Go host")
	if got := dirEntries(t, projects); !slices.Equal(got, []string{"project%7Cp1"}) {
		t.Fatalf("workers after the refused import = %v", got)
	}
	if title, labels := metaGet(t, meta, "p3"); title != "" || len(labels) != 0 {
		t.Fatalf("p3 meta after the refused import = %q %v", title, labels)
	}
	// Replace: an address with a directory and NodeHost data. Both are as before.
	failing.importErr = nil
	if err := s.app.ImportData(archive, projectAddress("p2"), background); err != nil {
		t.Fatal(err)
	}
	metaSet(t, meta, "p2", "changed", "green")
	wasTitle, wasLabels := metaGet(t, meta, "p2")
	write2 := holdNotes(t, s, "p2")
	write(t, write2, "notes of p2")
	write2.Release()
	if err := s.app.Workers["project"].Stop(projectAddress("p2"), background); err != nil {
		t.Fatal(err)
	}
	failing.importErr = data.Invalid(data.ReasonArgs, "refused by the Go host")
	err = s.app.ImportData(archive, projectAddress("p2"), background, DataImportOptions{Existing: store.ExistingReplace})
	wantError(t, err, "refused by the Go host")
	if got := dirEntries(t, projects); !slices.Equal(got, []string{"project%7Cp1", "project%7Cp2"}) {
		t.Fatalf("workers after the refused replace = %v", got)
	}
	if title, labels := metaGet(t, meta, "p2"); title != wasTitle || !slices.Equal(labels, wasLabels) {
		t.Fatalf("p2 meta after the refused replace = %q %v, want %q %v", title, labels, wasTitle, wasLabels)
	}
	failing.importErr = nil
	if got := textOfProject(t, s, "p2"); got != "notes of p2" {
		t.Fatalf("p2 notes after the refused replace = %q", got)
	}
}

// The rollback of the NodeHost's commit fails too: both errors are reported and the directories stay as after an unknown outcome.
func TestAppReportsBothErrorsWhenTheNodeHostsRollbackFailsToo(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	archive := exportedProject(t, s)
	rollback := data.Invalid(data.ReasonArgs, "the rollback is refused")
	s.app.nodeData = &refusingNode{nodeHostData: s.app.nodeData, deleteErr: rollback}
	failing := &goHost{Host: s.app.Host, importErr: data.Invalid(data.ReasonArgs, "refused by the Go host")}
	s.app.Host = failing
	err := s.app.ImportData(archive, projectAddress("p2"), background)
	wantError(t, err, "restoring the plugins' store failed; nothing was undone")
	if !errors.Is(err, failing.importErr) || !errors.Is(err, rollback) {
		t.Fatalf("the cause of %v does not hold both errors", err)
	}
	if got := dirEntries(t, filepath.Join(s.dataDir, "workers", "project")); !slices.Equal(got, []string{"project%7Cp1", "project%7Cp2"}) {
		t.Fatalf("workers = %v", got)
	}
}

// An unknown outcome of the Go host's commit keeps the unknown-outcome message and undoes nothing.
func TestAppKeepsTheNewDirectoryWhenTheGoHostsCommitHasAnUnknownOutcome(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	archive := exportedProject(t, s)
	unknown := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Reason: data.ReasonStorageFailed, Message: "store: storage failed"}, nil)
	s.app.Host = &goHost{Host: s.app.Host, importErr: unknown}
	err := s.app.ImportData(archive, projectAddress("p2"), background)
	wantError(t, err, "unknown outcome; nothing was undone")
	if !errors.Is(err, unknown) {
		t.Fatalf("the cause of %v is not the Go host's rejection", err)
	}
	if got := dirEntries(t, filepath.Join(s.dataDir, "workers", "project")); !slices.Equal(got, []string{"project%7Cp1", "project%7Cp2"}) {
		t.Fatalf("workers = %v", got)
	}
}

// textOfProject is the notes text of project id; its worker is stopped after.
func textOfProject(t *testing.T, s started, id string) string {
	t.Helper()
	notes := holdNotes(t, s, id)
	text := member(t, notes, "text").(string)
	notes.Release()
	if err := s.app.Workers["project"].Stop(projectAddress(id), background); err != nil {
		t.Fatal(err)
	}
	return text
}

// An archive without the Go host's part replaces the Go host's data of the address by nothing under replace: Pi's replace removes the
// address's data.
func TestAppReplacesTheGoHostsDataByNothingWhenTheArchiveHasNoPartOfIt(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	archive := exportedProject(t, s)
	without := filepath.Join(t.TempDir(), "without.tar")
	copyWithout(t, archive, without, "kit-server.archive")
	watching := &goHost{Host: s.app.Host}
	s.app.Host = watching
	if err := s.app.ImportData(without, projectAddress("p2"), background); err != nil {
		t.Fatal(err)
	}
	if len(watching.deleted) != 0 {
		t.Fatalf("a refusing import deleted the Go host's data of %v", watching.deleted)
	}
	if err := s.app.ImportData(without, projectAddress("p2"), background, DataImportOptions{Existing: store.ExistingReplace}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(watching.deleted, []string{"project|p2"}) {
		t.Fatalf("the Go host deleted the data of %v, want project|p2", watching.deleted)
	}
}

// goHostHolds puts a row of address into the Go host's store (a table of its own host-keyed by the address's kind): the address has data
// in the Go host.
func goHostHolds(t *testing.T, s started, address contracts.Address) {
	t.Helper()
	key := address.Key
	text := strings.Join([]string{
		`{"kind":"manifest","format":1,"schema":3,"lastSeq":0,"nextId":1,"host":"` + key + `"}`,
		`{"kind":"table","table":"test.go:rows","version":1,"state":"ready","columns":{"p":{"type":"str","host":"project"}},"key":["p"],"fingerprint":"f"}`,
		`{"kind":"put","table":"test.go:rows","row":{"p":"` + key + `"},"version":1,"columns":{}}`,
		`{"kind":"end","writes":2}`,
	}, "\n") + "\n"
	path := filepath.Join(t.TempDir(), "go-host.jsonl")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := s.app.Host.ImportHostData(store.DecodeJsonl(&fileBytes{file: file}), background, store.HostImportOptions{Address: &address}); err != nil {
		t.Fatal(err)
	}
}

// Pi's `importHostData` refuses an address that has data in the store with existing refuse whatever the archive holds. An archive Pi
// made has no `kit-server.archive`: the Go host's refusal is still asked, and when it refuses after the NodeHost's part was imported
// the NodeHost is rolled back (its data of the address as before, the old directory back).
func TestAppRefusesAPiMadeArchiveWhenTheGoHostHoldsDataOfTheAddressAndRollsTheNodeHostBack(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin, "meta": metaPlugin}, projectOptions(t))
	meta, err := s.app.Node.Get(background, metaStore, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Release()
	metaSet(t, meta, "p1", "first", "red")
	archive := exportedProject(t, s)
	piMade := filepath.Join(t.TempDir(), "pi-made.tar")
	copyWithout(t, archive, piMade, "kit-server.archive")
	goHostHolds(t, s, projectAddress("p3"))
	err = s.app.ImportData(piMade, projectAddress("p3"), background)
	wantError(t, err, "project|p3 has data in this store")
	if got := dirEntries(t, filepath.Join(s.dataDir, "workers", "project")); !slices.Equal(got, []string{"project%7Cp1"}) {
		t.Fatalf("workers after the refused import = %v", got)
	}
	if title, labels := metaGet(t, meta, "p3"); title != "" || len(labels) != 0 {
		t.Fatalf("p3 meta after the refused import = %q %v, want none (the NodeHost's part rolled back)", title, labels)
	}
}

// copyWithout writes the archive at from to to, without the entry named skip.
func copyWithout(t *testing.T, from, to, skip string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	type kept struct {
		path string
		text []byte
	}
	var entries []kept
	if err := ReadTar(in, func(entry TarRead) error {
		text, err := io.ReadAll(entry.Data)
		if entry.Kind != TarDirectory && entry.Path != skip {
			entries = append(entries, kept{entry.Path, text})
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	all := func(yield func(TarEntry) bool) {
		for _, entry := range entries {
			if !yield(TarEntry{Path: entry.path, Size: int64(len(entry.text)), Data: bytes.NewReader(entry.text)}) {
				return
			}
		}
	}
	if err := errors.Join(WriteTar(out, all), out.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownOutcomeIsTheNodeHostsStorageFailedToo(t *testing.T) {
	chord := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Reason: data.ReasonStorageFailed, Message: "store: storage failed"}, nil)
	if !unknownOutcome(chord) || !unknownOutcome(&plugins.NodeError{Name: "ChordError", Message: chord.Message, Chord: chord}) {
		t.Fatal("storage_failed is not an unknown outcome")
	}
	if unknownOutcome(data.Invalid(data.ReasonArgs, "refused")) || unknownOutcome(&plugins.NodeError{Name: "Error", Message: "x"}) || unknownOutcome(errors.New("storage_failed")) {
		t.Fatal("another rejection is an unknown outcome")
	}
}
