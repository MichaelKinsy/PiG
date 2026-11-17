package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
)

// The kill tests of D462: an import or a delete of an address's data commits twice (the NodeHost's store, then the Go host's), where Pi's
// `importHostData` and `deleteHostData` are one commit (`host-data.ts:316`). A process killed between the commits must leave the address
// all old, all new or all gone at the next start, never partly one and partly the other. Each test runs a child process (this test
// binary, TestMain's "importer" mode) that stops at a seam and is killed by its pid there.

// The variables of the child process.
const (
	seamData    = "CHORD_SEAM_DATA"
	seamRoot    = "CHORD_SEAM_ROOT"
	seamArchive = "CHORD_SEAM_ARCHIVE"
	// seamDo is what the child does: "import", "replace" or "delete" the address seamID, stopping between the two commits;
	// "import-after" imports it and stops after the second commit; "start" only starts the app, stopping at the step seamStep of the
	// undo (or the completion) of an interrupted import or delete.
	seamDo     = "CHORD_SEAM_DO"
	seamID     = "CHORD_SEAM_ID"
	seamStep   = "CHORD_SEAM_STEP"
	seamMarker = "SEAM"
	// seamStarted is what a "start" child says when its start ended without stopping at the step.
	seamStarted = "STARTED"
)

// seamHost is the Go host of the child: the operation stops at the seam (the child says so on its standard output and waits there for the
// test to kill it by its pid).
type seamHost struct {
	host.Host
	at string
}

func blockAtTheSeam() {
	fmt.Println(seamMarker)
	select {}
}

func (h seamHost) ImportHostData(parts store.PartSource, ctx context.Context, options store.HostImportOptions) (store.HostDataCounts, error) {
	if h.at == "import" || h.at == "replace" {
		blockAtTheSeam()
	}
	counts, err := h.Host.ImportHostData(parts, ctx, options)
	if err == nil && h.at == "import-after" {
		blockAtTheSeam()
	}
	return counts, err
}

func (h seamHost) DeleteHostData(address contracts.Address, ctx context.Context, options host.HostDeleteOptions) (store.HostDataCounts, error) {
	if h.at == "delete" {
		blockAtTheSeam()
	}
	return h.Host.DeleteHostData(address, ctx, options)
}

// runSeamChild is the child process of the kill tests (TestMain, appFixtureEnv "importer").
func runSeamChild() {
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	do := os.Getenv(seamDo)
	if do == "start" {
		resolveSeam = func(step string) {
			if step == os.Getenv(seamStep) {
				blockAtTheSeam()
			}
		}
	}
	options := AppOptions{
		Name: "chord-test", DataDir: os.Getenv(seamData), Root: os.Getenv(seamRoot),
		Listen: &chordhttp.ListenAddress{Host: "127.0.0.1", Port: 0},
		API:    apiModules(), Kinds: testKinds(),
		Principal: AppPrincipals{
			Server: testServer, Owner: testOwner,
			Worker: func(address contracts.Address) data.Principal {
				return delta.JsonObjectOf("kind", "worker", "key", address.Key)
			},
		},
		OnError: func(err AppError) { fmt.Fprintln(os.Stderr, err.Kind, err.Error) },
	}
	worker, err := testWorkerKind(project, "")
	if err != nil {
		fail(err)
	}
	options.Workers = []AppWorkerKind{worker}
	app, err := StartServer(options, context.Background())
	if err != nil {
		fail(err)
	}
	<-app.BasesBuilt()
	if do == "start" {
		fmt.Println(seamStarted)
		_ = app.Close(context.Background())
		os.Exit(0)
	}
	address := projectAddress(os.Getenv(seamID))
	app.Host = seamHost{Host: app.Host, at: do}
	switch do {
	case "import", "import-after":
		err = app.ImportData(os.Getenv(seamArchive), address, context.Background())
	case "replace":
		err = app.ImportData(os.Getenv(seamArchive), address, context.Background(), DataImportOptions{Existing: store.ExistingReplace})
	case "delete":
		err = app.DeleteData(address, context.Background())
	}
	fmt.Fprintf(os.Stderr, "the %s returned without reaching the seam: %v\n", do, err)
	os.Exit(2)
}

// killedAtTheSeam runs the child in mode do on dataDir and kills it by its pid where it says SEAM; the test fails when it does not.
func killedAtTheSeam(t *testing.T, dataDir, root, archive, do, id, step string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable)
	child.Env = append(os.Environ(), appFixtureEnv+"=importer", seamData+"="+dataDir, seamRoot+"="+root, seamArchive+"="+archive, seamDo+"="+do, seamID+"="+id, seamStep+"="+step)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(stdout)
	reached := false
	for lines.Scan() {
		if strings.TrimSpace(lines.Text()) == seamMarker {
			reached = true
			break
		}
	}
	if err := child.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatal(err)
	}
	_ = child.Wait()
	if !reached {
		t.Fatalf("the child (%s %s %s) did not reach the seam: %s", do, id, step, &stderr)
	}
}

// seamPlugins are the plugins of the kill tests: notes (a worker's data) and meta (the NodeHost's records of an address).
var seamPlugins = map[string]map[string]string{"notes": notesPlugin, "meta": metaPlugin}

// populate gives address id data of the three kinds: its worker's directory (notes text), the plugins' records (a title and a label)
// and a row of the Go host.
func populate(t *testing.T, s started, id, text, title, label string) {
	t.Helper()
	meta, err := s.app.Node.Get(background, metaStore, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	metaSet(t, meta, id, title, label)
	meta.Release()
	goHostHolds(t, s, projectAddress(id))
	notes := holdNotes(t, s, id)
	write(t, notes, text)
	notes.Release()
	if err := s.app.Workers["project"].Stop(projectAddress(id), background); err != nil {
		t.Fatal(err)
	}
}

// seamServer is a data directory of the kill tests: project p1 holds data of the three kinds and is exported to archive, and setup puts
// more in; the app is closed when it returns.
func seamServer(t *testing.T, setup func(s started)) (dataDir, root, archive string) {
	t.Helper()
	s := startTestApp(t, seamPlugins, projectOptions(t))
	populate(t, s, "p1", "notes of p1", "first", "red")
	archive = exportedProject(t, s)
	if setup != nil {
		setup(s)
	}
	dataDir, root = s.dataDir, s.root
	closeAndWait(t, s.app)
	return dataDir, root, archive
}

// restarted starts an app on a data directory a killed process left; it may report what it found there.
func restarted(t *testing.T, dataDir, root string) started {
	t.Helper()
	s := startTestApp(t, seamPlugins, func(o *AppOptions) {
		projectOptions(t)(o)
		o.DataDir, o.Root = dataDir, root
	})
	s.errors.expect()
	return s
}

// goHostRows is how many rows of test.go:rows the Go host holds of address.
func goHostRows(t *testing.T, s started, address contracts.Address) int {
	t.Helper()
	exported, err := s.app.Host.ExportHostData(address, background)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = exported.Close() }()
	rows := 0
	for {
		part, ok, err := exported.Next(background)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return rows
		}
		if put, isPut := part.(*store.PutWrite); isPut && put.Table == "test.go:rows" {
			rows++
		}
	}
}

// addressData is what the three places hold of an address: the worker's directory and its notes' text, the plugins' records (the title
// and labels of meta) and the Go host's rows.
type addressData struct {
	Directory bool
	Notes     string
	Title     string
	Labels    []string
	GoRows    int
}

// dataOf reads what s holds of project id (the notes only when its directory exists: reading them starts its worker).
func dataOf(t *testing.T, s started, id string) addressData {
	t.Helper()
	address := projectAddress(id)
	meta, err := s.app.Node.Get(background, metaStore, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	title, labels := metaGet(t, meta, id)
	meta.Release()
	got := addressData{Directory: Exists(WorkerDirectory(s.dataDir, address)), Title: title, Labels: slices.Clone(labels), GoRows: goHostRows(t, s, address)}
	if got.Labels == nil {
		got.Labels = []string{}
	}
	if got.Directory {
		got.Notes = textOfProject(t, s, id)
	}
	return got
}

// none is the data of an address that holds nothing.
var none = addressData{Labels: []string{}}

func wantData(t *testing.T, s started, id string, want addressData, when string) {
	t.Helper()
	if got := dataOf(t, s, id); !reflect.DeepEqual(got, want) {
		t.Errorf("%s project %s holds %+v, want %+v", when, id, got, want)
	}
}

// intentNames are the files of the data directory's server/imports directory, sorted ([] when it has none).
func intentNames(t *testing.T, dataDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "server", "imports"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// intentFileOf is the intent file a kill left (the test fails when there is not exactly one).
func intentFileOf(t *testing.T, dataDir string) string {
	t.Helper()
	var found []string
	for _, name := range intentNames(t, dataDir) {
		if strings.HasSuffix(name, ".json") {
			found = append(found, filepath.Join(dataDir, "server", "imports", name))
		}
	}
	if len(found) != 1 {
		t.Fatalf("the killed process left %d intent files in server/imports (%v), want 1", len(found), intentNames(t, dataDir))
	}
	return found[0]
}

// A process killed between the two commits of an import (the NodeHost's store, then the Go host's) leaves the address all new or all
// old at the next start: Pi's server part is one commit and never partly new (`host-data.ts:316`). PiG has two stores and commits them
// one after the other (D462); the child imports an archive whose server part has records in both and is killed by its pid at the seam
// between the two.
func TestAKillBetweenTheTwoCommitsOfAnImportLeavesTheAddressAllOrNothing(t *testing.T) {
	dataDir, root, archive := seamServer(t, nil)
	killedAtTheSeam(t, dataDir, root, archive, "import", "p2", "")
	s := restarted(t, dataDir, root)
	meta, err := s.app.Node.Get(background, metaStore, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Release()
	address := projectAddress("p2")
	title, labels := metaGet(t, meta, "p2")
	have := map[string]bool{
		"the worker's directory": Exists(WorkerDirectory(s.dataDir, address)),
		"the plugins' records":   title != "" || len(labels) != 0,
		"the Go host's rows":     goHostRows(t, s, address) != 0,
	}
	if have["the worker's directory"] != have["the plugins' records"] || have["the plugins' records"] != have["the Go host's rows"] {
		t.Errorf("after a kill between the two commits the address is partly imported at the next start: %v (all or nothing)", have)
	}
}

// The same kill, over an address that holds data and is imported with existing: "replace": the design reaches the OLD data back (the
// directory, the plugins' records and the Go host's rows as before the import), because the undo is the live rollback's: both stores are
// restored from the parts saved before the first commit and the old directory is put back from where it was set aside.
func TestAKillBetweenTheTwoCommitsOfAReplaceImportPutsTheOldDataBack(t *testing.T) {
	old := addressData{Directory: true, Notes: "notes of p2", Title: "old", Labels: []string{"blue"}, GoRows: 1}
	dataDir, root, archive := seamServer(t, func(s started) { populate(t, s, "p2", "notes of p2", "old", "blue") })
	killedAtTheSeam(t, dataDir, root, archive, "replace", "p2", "")
	s := restarted(t, dataDir, root)
	wantData(t, s, "p2", old, "after a kill between the two commits of a replace import,")
	if got := dirEntries(t, filepath.Join(dataDir, "workers", "project")); !slices.Equal(got, []string{"project%7Cp1", "project%7Cp2"}) {
		t.Errorf("workers after the undo = %v, want no aside or staging directory left", got)
	}
}

// A kill between the two commits of deleteData (the NodeHost's rows, then the Go host's): the next start finishes the delete, as Pi's
// repeated `deleteData` does (`app.ts:889-891`): the address holds nothing, in no store and no directory.
func TestAKillBetweenTheTwoCommitsOfADeleteFinishesTheDeleteAtTheNextStart(t *testing.T) {
	dataDir, root, archive := seamServer(t, func(s started) { populate(t, s, "p2", "notes of p2", "old", "blue") })
	killedAtTheSeam(t, dataDir, root, archive, "delete", "p2", "")
	s := restarted(t, dataDir, root)
	wantData(t, s, "p2", none, "after a kill between the two commits of a delete,")
	wantData(t, s, "p1", addressData{Directory: true, Notes: "notes of p1", Title: "first", Labels: []string{"red"}, GoRows: 1}, "after the delete of another address,")
}

// A kill after the second commit and before the intent's removal: the import was finished in both stores but its caller saw no success,
// so the next start undoes it, as a whole (the address is all old). The window is the unlink of one file right after the second commit.
func TestAKillAfterTheSecondCommitBeforeTheIntentIsRemovedUndoesTheFinishedImport(t *testing.T) {
	dataDir, root, archive := seamServer(t, nil)
	killedAtTheSeam(t, dataDir, root, archive, "import-after", "p2", "")
	s := restarted(t, dataDir, root)
	wantData(t, s, "p2", none, "after a kill after the second commit,")
}

// A kill during the undo at the start (at each of its steps) leaves it half done; the next start finishes it: the address is all old.
func TestAKillDuringTheUndoAtStartIsFinishedByTheNextStart(t *testing.T) {
	for _, step := range []string{"restore-node", "restore-go", "place", "intent"} {
		t.Run(step, func(t *testing.T) {
			dataDir, root, archive := seamServer(t, nil)
			killedAtTheSeam(t, dataDir, root, archive, "import", "p2", "")
			killedAtTheSeam(t, dataDir, root, "", "start", "", step)
			if names := intentNames(t, dataDir); !slices.ContainsFunc(names, func(name string) bool { return strings.HasSuffix(name, ".json") }) {
				t.Fatalf("the undo killed at %s left no intent: %v", step, names)
			}
			s := restarted(t, dataDir, root)
			wantData(t, s, "p2", none, "after the second start,")
			if names := intentNames(t, dataDir); len(names) != 0 {
				t.Errorf("the second start left %v in server/imports", names)
			}
		})
	}
}

// The start tells the operator of what it undid: one report of kind interrupted_data_change naming the address.
func TestAStartReportsTheInterruptedImportItUndid(t *testing.T) {
	dataDir, root, archive := seamServer(t, nil)
	killedAtTheSeam(t, dataDir, root, archive, "import", "p2", "")
	s := restarted(t, dataDir, root)
	reported := s.errors.all()
	if len(reported) != 1 || reported[0].Kind != "interrupted_data_change" || !strings.Contains(fmt.Sprint(reported[0].Error), "project|p2") {
		t.Fatalf("the start reported %v, want one interrupted_data_change naming project|p2", reported)
	}
}

// An intent the start cannot use blocks its own address and no other: nothing is guessed (the address's data stays as the kill left it),
// the address refuses every request with an error that names the file, and the other addresses serve. A broken address fails its own
// worker today (`worker failed to start`), not the server.
func TestAnIntentThatCannotBeUsedBlocksItsAddressOnly(t *testing.T) {
	damage := map[string]func(t *testing.T, intent string){
		"torn": func(t *testing.T, intent string) {
			text, err := os.ReadFile(intent)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(intent, text[:len(text)/2], 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"another shape": func(t *testing.T, intent string) {
			text, err := os.ReadFile(intent)
			if err != nil {
				t.Fatal(err)
			}
			var record map[string]any
			if err := json.Unmarshal(text, &record); err != nil {
				t.Fatal(err)
			}
			record["unknown"] = true
			changed, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(intent, changed, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"old part missing": func(t *testing.T, intent string) {
			if err := os.Remove(strings.TrimSuffix(intent, ".json") + ".node"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, damaged := range damage {
		t.Run(name, func(t *testing.T) {
			dataDir, root, archive := seamServer(t, nil)
			killedAtTheSeam(t, dataDir, root, archive, "import", "p2", "")
			intent := intentFileOf(t, dataDir)
			damaged(t, intent)
			s := restarted(t, dataDir, root)
			address := projectAddress("p2")
			// Every request of the address is refused with the intent's error.
			file := filepath.Base(intent)
			mentions := func(err error) bool { return err != nil && strings.Contains(err.Error(), file) }
			held, err := s.app.Host.Get(background, notes, delta.JsonObjectOf("id", "p2"), host.GetOptions{})
			if err == nil {
				_, err = held.Member("text").Value()
				held.Release()
			}
			if err == nil {
				t.Error("a hold of the blocked address succeeded")
			}
			if err := s.app.ImportData(archive, address, background); !mentions(err) {
				t.Errorf("importData of the blocked address: %v, want an error naming %s", err, file)
			}
			if err := s.app.DeleteData(address, background); !mentions(err) {
				t.Errorf("deleteData of the blocked address: %v, want an error naming %s", err, file)
			}
			if err := s.app.ExportData(address, filepath.Join(t.TempDir(), "p2.tar"), background); !mentions(err) {
				t.Errorf("exportData of the blocked address: %v, want an error naming %s", err, file)
			}
			// The other addresses serve.
			other := holdNotes(t, s, "p1")
			if got := member(t, other, "text"); got != "notes of p1" {
				t.Errorf("p1 notes = %v", got)
			}
			other.Release()
			// The operator is told, and the files are as they were.
			told := false
			for _, reported := range s.errors.all() {
				told = told || (reported.Kind == "interrupted_data_change" && strings.Contains(fmt.Sprint(reported.Error), file))
			}
			if !told {
				t.Errorf("the start did not report the unusable intent %s: %v", file, s.errors.all())
			}
			if !Exists(intent) {
				t.Error("the unusable intent was removed: nothing may be guessed")
			}
		})
	}
}

// An import whose intent cannot be written is refused before anything changed: the directory, the plugins' records and the Go host's
// rows of the address are as they were and nothing is left in the workers' directory.
func TestAnImportIsRefusedWhenItsIntentCannotBeWritten(t *testing.T) {
	s := startTestApp(t, seamPlugins, projectOptions(t))
	populate(t, s, "p1", "notes of p1", "first", "red")
	archive := exportedProject(t, s)
	if err := os.WriteFile(filepath.Join(s.dataDir, "server", "imports"), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.app.ImportData(archive, projectAddress("p2"), background); err == nil {
		t.Error("an import whose intent cannot be written succeeded")
	}
	wantData(t, s, "p2", none, "after the refused import,")
	if got := dirEntries(t, filepath.Join(s.dataDir, "workers", "project")); !slices.Equal(got, []string{"project%7Cp1"}) {
		t.Errorf("workers after the refused import = %v", got)
	}
}

// probeNode and probeHost tell, where the NodeHost and the Go host are asked to commit, what server/imports holds at that moment.
type probeNode struct {
	nodeHostData
	observe func(string)
}

func (n probeNode) ImportHostData(address contracts.Address, path, existing string, ctx context.Context) error {
	n.observe("commit-node")
	return n.nodeHostData.ImportHostData(address, path, existing, ctx)
}

func (n probeNode) DeleteHostData(address contracts.Address, ctx context.Context) error {
	n.observe("delete-node")
	return n.nodeHostData.DeleteHostData(address, ctx)
}

type probeHost struct {
	host.Host
	observe func(string)
}

func (h probeHost) ImportHostData(parts store.PartSource, ctx context.Context, options store.HostImportOptions) (store.HostDataCounts, error) {
	h.observe("commit-go")
	return h.Host.ImportHostData(parts, ctx, options)
}

func (h probeHost) DeleteHostData(address contracts.Address, ctx context.Context, options host.HostDeleteOptions) (store.HostDataCounts, error) {
	h.observe("delete-go")
	return h.Host.DeleteHostData(address, ctx, options)
}

// The intent is on disk, with the old parts of both hosts, before the first commit and until after the second; it is gone, with its
// parts, when the operation ends, whatever the outcome (a refusal, a definite failure, an unknown outcome): the intent covers a kill and
// nothing else, the live paths keep their own undo.
func TestTheIntentStandsFromBeforeTheFirstCommitUntilTheOperationEnds(t *testing.T) {
	stem := "project.project%7Cp2"
	both := []string{stem + ".go", stem + ".json", stem + ".node"}
	s := startTestApp(t, seamPlugins, projectOptions(t))
	populate(t, s, "p1", "notes of p1", "first", "red")
	archive := exportedProject(t, s)
	seen := map[string][]string{}
	observe := func(at string) { seen[at] = intentNames(t, s.dataDir) }
	realNode, realHost := s.app.nodeData, s.app.Host
	s.app.nodeData = probeNode{nodeHostData: realNode, observe: observe}
	s.app.Host = probeHost{Host: realHost, observe: observe}
	if err := s.app.ImportData(archive, projectAddress("p2"), background); err != nil {
		t.Fatal(err)
	}
	for _, at := range []string{"commit-node", "commit-go"} {
		if !slices.Equal(seen[at], both) {
			t.Errorf("server/imports at %s holds %v, want %v", at, seen[at], both)
		}
	}
	if got := intentNames(t, s.dataDir); len(got) != 0 {
		t.Errorf("server/imports after the import holds %v", got)
	}
	// A delete: the intent has no parts.
	if err := s.app.DeleteData(projectAddress("p2"), background); err != nil {
		t.Fatal(err)
	}
	for _, at := range []string{"delete-node", "delete-go"} {
		if !slices.Equal(seen[at], []string{stem + ".json"}) {
			t.Errorf("server/imports at %s holds %v, want [%s.json]", at, seen[at], stem)
		}
	}
	if got := intentNames(t, s.dataDir); len(got) != 0 {
		t.Errorf("server/imports after the delete holds %v", got)
	}
	// The outcomes that are not a success.
	refused := data.Invalid(data.ReasonArgs, "refused")
	unknown := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Reason: data.ReasonStorageFailed, Message: "store: storage failed"}, nil)
	importP3 := func() error { return s.app.ImportData(archive, projectAddress("p3"), background) }
	for _, outcome := range []struct {
		name string
		node nodeHostData
		host host.Host
		run  func() error
	}{
		{"the NodeHost refuses", &refusingNode{nodeHostData: realNode, err: refused}, realHost, importP3},
		{"the Go host refuses", realNode, &goHost{Host: realHost, importErr: refused}, importP3},
		{"the Go host's outcome is unknown", realNode, &goHost{Host: realHost, importErr: unknown}, importP3},
		{"the delete is refused", &refusingNode{nodeHostData: realNode, err: refused}, realHost, func() error { return s.app.DeleteData(projectAddress("p1"), background) }},
	} {
		s.app.nodeData, s.app.Host = outcome.node, outcome.host
		if err := outcome.run(); err == nil {
			t.Fatalf("%s: the operation succeeded", outcome.name)
		}
		if got := intentNames(t, s.dataDir); len(got) != 0 {
			t.Errorf("server/imports after %s holds %v", outcome.name, got)
		}
	}
}
