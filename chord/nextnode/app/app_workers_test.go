package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/node/app/app.test.ts "workers" and "export and import per address", and worker.test.ts (Pi main c23b9ec1):
// a worker per address with the kit's start data and its own directory, two kinds side by side, a worker's directory outliving its
// process, and a worker's data exported (running or not) and imported at another address. `Files` of a worker are the plugins'
// through the bridge in Pi's test (`attach`, `file`); the Go kit's `Files` has its own tests (files_test.go), and the notes plugin here
// uses what the tests' API declares (WorkerInfo).

func holdNotes(t *testing.T, s started, id string) *host.Reference {
	t.Helper()
	notes, err := s.app.Host.Get(background, notes, delta.JsonObjectOf("id", id), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return notes
}

func member(t *testing.T, reference *host.Reference, name string) delta.JsonValue {
	t.Helper()
	value, err := reference.Member(name).Value()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func projectOptions(t *testing.T) func(*AppOptions) {
	return func(o *AppOptions) { o.Workers = []AppWorkerKind{testWorker(t, project, "")} }
}

func TestAppStartsAWorkerPerAddressWithTheKitsStartDataAndItsOwnDirectoryTwoKindsSideBySide(t *testing.T) {
	members := mustRemote("test.members", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
		HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
			return team.Address(delta.JsonObjectOf("id", params.Value("id"))), nil
		},
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("start", contracts.Unknown())}})},
	})
	teamPlugin := map[string]string{"teamworker.ts": `import { defineFacet, hostKind, remote, str, unknown, WorkerInfo } from "@test/app/v1";
const Team = hostKind("team", { id: str() });
const Members = remote("test.members", { version: 1, params: { id: str() }, hostAddress: ({ id }) => Team({ id }), variants: { main: { state: { start: unknown() } } } });
export default defineFacet((host) => host.serve(Members, (_params, _principal, self) => {
	const info = self.get(WorkerInfo, {});
	return { variant: "main", members: { start: self.derive([info.start], ([value]) => value) } };
}));`}
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin, "team": teamPlugin}, func(o *AppOptions) {
		o.Workers = []AppWorkerKind{testWorker(t, project, ""), testWorker(t, team, "teamworker")}
	})
	p1 := holdNotes(t, s, "p1")
	defer p1.Release()
	t1, err := s.app.Host.Get(background, members, delta.JsonObjectOf("id", "t1"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer t1.Release()
	if got := member(t, p1, "start").(*delta.JsonObject).Value("project"); got != "project|p1" {
		t.Fatalf("notes start = %v", got)
	}
	if got := member(t, t1, "start").(*delta.JsonObject).Value("team"); got != "team|t1" {
		t.Fatalf("members start = %v", got)
	}
	if got := dirEntries(t, filepath.Join(s.dataDir, "workers")); !slices.Equal(got, []string{"project", "team"}) {
		t.Fatalf("workers = %v", got)
	}
	if got := dirEntries(t, filepath.Join(s.dataDir, "workers", "project")); !slices.Equal(got, []string{"project%7Cp1"}) {
		t.Fatalf("project workers = %v", got)
	}
}

func TestAppAWorkersDirectoryKeepsItsStoreAndFilesAcrossItsProcessesRestarts(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	p1 := holdNotes(t, s, "p1")
	if _, err := p1.Call("write", "kept", background); err != nil {
		t.Fatal(err)
	}
	p1.Release()
	dir := filepath.Join(s.dataDir, "workers", "project", "project%7Cp1")
	if got := dirEntries(t, dir); !slices.Contains(got, "store.sqlite") {
		t.Fatalf("worker directory = %v", got)
	}
	mode := func(path string) os.FileMode {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm()
	}
	if got := []os.FileMode{mode(dir), mode(filepath.Join(dir, "..")), mode(filepath.Join(dir, "store.sqlite"))}; !slices.Equal(got, []os.FileMode{0o700, 0o700, 0o600}) {
		t.Fatalf("modes = %o", got)
	}
	workers := s.app.Workers["project"]
	address := project.Address(delta.JsonObjectOf("id", "p1"))
	if err := workers.Stop(address, background); err != nil {
		t.Fatal(err)
	}
	if err := workers.Retry(address, background); err != nil {
		t.Fatal(err)
	}
	again := holdNotes(t, s, "p1")
	defer again.Release()
	if got := member(t, again, "text"); got != "kept" {
		t.Fatalf("text after a restart = %v", got)
	}
}

// The module a worker writes for its HostData holds the start's secret token: it is there while the worker runs (the NodeHost loads
// it), mode 0600, and gone once the worker ended.
func TestAppAWorkersHostDataModuleHoldingItsTokenIsPrivateWhileItRunsAndGoneAfterItsEnd(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	p1 := holdNotes(t, s, "p1")
	defer p1.Release()
	module := filepath.Join(s.dataDir, "workers", "project", "project%7Cp1", ".host-data.mjs")
	info, err := os.Stat(module)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	if err := s.app.Workers["project"].Stop(project.Address(delta.JsonObjectOf("id", "p1")), background); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(module); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the module of an ended worker: %v", err)
	}
}
