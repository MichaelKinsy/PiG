package plugins

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next-node/plugins/plugins.test.ts › "end to end: a server and a worker": plugin directories
// loaded on a server and a worker joined by a memory link, an edit rolled out (server, then worker), and a broken edit kept
// old on both. The fixtures `contracts` and `notes` are the upstream plugins in the facet language of plugins_seam_test.go
// (`label.ts` is `label v1`, the worker `notes-worker`, the server `index-server`); their contracts are declared here.

// workerKindOf is the worker's host kind (`hostKind("<prefix>.worker")`): `Notes` lives there.
func workerKindOf(prefix string) *contracts.HostKind {
	return mustKind(contracts.NewHostKind(prefix + ".worker"))
}

// notesContractOf is the fixtures' `Notes`: a remote type on the worker, a label, a count and `add`.
func notesContractOf(prefix string) *contracts.Contract {
	worker := workerKindOf(prefix)
	return mustContract(contracts.Remote(prefix+".notes", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return worker.Address(nil), nil },
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
			State:   []contracts.Property{contracts.Prop("label", contracts.Str()), contracts.Prop("count", contracts.Int())},
			Methods: []contracts.MethodEntry{contracts.Meth("add", contracts.NewMethod(contracts.TSchema{}, contracts.Int()))},
		})},
	}))
}

var (
	notesContract = notesContractOf("test")
	// countDocument is the worker facet's `defineState("count", { schema: obj({ n: int() }), version: 1, initial: ... })`.
	countDocument = mustState(documents.DefineState("count", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("n", float64(0)) },
	}))
)

func mustKind(kind *contracts.HostKind, err error) *contracts.HostKind {
	if err != nil {
		panic(err)
	}
	return kind
}

func mustContract(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

func mustState(definition *documents.StateDefinition, err error) *documents.StateDefinition {
	if err != nil {
		panic(err)
	}
	return definition
}

// notesFactory is the worker facet's factory of Notes: `{ variant: "main", members: { label: constant(label), count:
// derive(doc, n), add } }`.
func notesFactory(label string) func(*delta.JsonObject, data.Principal, host.Self) (contracts.Implementation, error) {
	return func(_ *delta.JsonObject, _ data.Principal, self host.Self) (contracts.Implementation, error) {
		doc := self.State(countDocument).(*documents.Singleton)
		count := self.Derive([]any{doc}, func(values []any) (delta.JsonValue, error) {
			return values[0].(*delta.JsonObject).Value("n"), nil
		})
		return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
			State: map[string]chordWatchable{"label": host.Expose(state.NewConstant(label)), "count": count},
			Methods: map[string]contracts.MethodFunction{
				"add": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					if _, err := doc.Change(func(draft *delta.Object) (any, error) {
						n, _ := draft.Get("n").(float64)
						return nil, draft.Set("n", n+1)
					}, ctx); err != nil {
						return nil, err
					}
					value, err := doc.Value()
					if err != nil {
						return nil, err
					}
					return value.Value("n"), nil
				},
			},
		}}, nil
	}
}

// framesOf collects the values a watch delivers.
type framesOf struct {
	mu     sync.Mutex
	values []delta.JsonValue
}

func (f *framesOf) all() []delta.JsonValue {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]delta.JsonValue{}, f.values...)
}

func startFrames(watch chord.WatchHandle[delta.JsonValue]) *framesOf {
	frames := &framesOf{}
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			frames.mu.Lock()
			frames.values = append(frames.values, call.Value)
			frames.mu.Unlock()
		}
		return nil
	})
	return frames
}

// linkedHosts is a server and a worker (`<prefix>.worker`) joined by a memory link, as upstream's tests and examples make
// them: the worker connects to the server, the server routes the worker's kind over the other end. Both are closed when the
// test ends.
func linkedHosts(t *testing.T, prefix string) (server, worker host.Host) {
	t.Helper()
	kind := workerKindOf(prefix)
	up, down := link.MemoryLinkPair()
	address := kind.Address(nil)
	worker, err := host.CreateHost(host.HostOptions{Address: &address, Principal: delta.JsonObjectOf("kind", "worker"), OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Close(background) })
	server, err = host.CreateHost(host.HostOptions{Principal: delta.JsonObjectOf("kind", "server"), OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(background) })
	if err := worker.Connect(up, host.LinkOptions{Principal: delta.JsonObjectOf("kind", "server")}, background); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Route(kind, host.RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(contracts.Address, context.Context) (host.ResolvedLink, error) {
			return host.ResolvedLink{Link: down, Principal: delta.JsonObjectOf("kind", "worker")}, nil
		},
	}, background); err != nil {
		t.Fatal(err)
	}
	return server, worker
}

func TestEndToEndAServerAndAWorkerLoadsPluginDirectoriesRollsAnEditOutServerThenWorkerAndKeepsABrokenEditOldOnBoth(t *testing.T) {
	root := tempRoot(t)
	writeFiles(t, filepath.Join(root, "contracts"), map[string]string{
		"package.json": `{"name":"@test/contracts","private":true,"type":"module"}`,
		"contract.ts":  "contracts",
	})
	writePlugin(t, root, "notes", "@test/notes", map[string]string{
		"label.ts":  "label v1",
		"server.ts": "import ./label.ts\nimport @test/contracts/contract.ts\nindex-server",
		"worker.ts": "import ./label.ts\nimport @test/contracts/contract.ts\nnotes-worker",
	}, nil)
	plugins := component(t, root)
	server, worker := linkedHosts(t, "test")
	mustLoad(t)(plugins.Load(worker, PluginsLoad{Kind: "worker", Modules: HostPackages{}}, background))
	mustLoad(t)(plugins.Load(server, PluginsLoad{Kind: "server", Modules: HostPackages{}}, background))
	notes, err := server.Get(notesContract, delta.NewJsonObject(0), background, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	index, err := server.Get(itemContract("test.index", 1), delta.NewJsonObject(0), background, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	member := func(ref *host.Reference, name string) delta.JsonValue {
		t.Helper()
		value, err := ref.Member(name).Value()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := []delta.JsonValue{member(notes, "label"), member(index, "label")}; !reflect.DeepEqual(got, []delta.JsonValue{"v1", "v1"}) {
		t.Fatalf("labels = %v, want [v1 v1]", got)
	}
	if _, err := notes.Call("add", nil, background); err != nil {
		t.Fatal(err)
	}
	watch, err := notes.Member("label").Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	frames := startFrames(watch)
	serverStep, err := plugins.Step("server", server)
	if err != nil {
		t.Fatal(err)
	}
	workerStep, err := plugins.Step("worker", worker)
	if err != nil {
		t.Fatal(err)
	}
	steps := []RolloutStep{serverStep, workerStep}

	writeText(t, filepath.Join(root, "notes", "label.ts"), "label v2")
	waitMarks(t, plugins, `dirty [@test/notes]`, dirtyIs("@test/notes"))
	rolled := rollout(t, plugins, steps, build(t, plugins, plugins.Marks().Dirty...))
	type rolledStep struct {
		name    string
		changed []string
		objects []host.ReloadedObject
	}
	var got []rolledStep
	for _, each := range rolled.Steps {
		report := each.Hosts[0].Report
		got = append(got, rolledStep{each.Name, report.Plugins.Changed, report.Objects})
	}
	want := []rolledStep{
		{"server", []string{"@test/notes"}, []host.ReloadedObject{{Object: "test.index", Kind: host.ReloadSwapped}}},
		{"worker", []string{"@test/notes"}, []host.ReloadedObject{{Object: "test.notes", Kind: host.ReloadSwapped}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rolled = %+v, want %+v", got, want)
	}
	eventually(t, 5*time.Second, `one frame "v2"`, func() bool { return reflect.DeepEqual(frames.all(), []delta.JsonValue{"v2"}) })
	if got := []delta.JsonValue{member(notes, "label"), member(notes, "count"), member(index, "label")}; !reflect.DeepEqual(got, []delta.JsonValue{"v2", float64(1), "v2"}) {
		t.Fatalf("after the edit = %v, want [v2 1 v2]", got)
	}
	if marks := plugins.Marks(); len(marks.Dirty) != 0 {
		t.Fatalf("marks = %+v, want nothing dirty", marks)
	}

	writeText(t, filepath.Join(root, "notes", "label.ts"), "syntax error: export const label = ;")
	waitMarks(t, plugins, `dirty [@test/notes]`, dirtyIs("@test/notes"))
	broken := rollout(t, plugins, steps, build(t, plugins, plugins.Marks().Dirty...))
	for _, each := range broken.Steps {
		if names := failedNames(each.Hosts[0].Report.Failed); !slices.Equal(names, []string{"@test/notes"}) {
			t.Fatalf("step %s failed = %v, want [@test/notes]", each.Name, names)
		}
	}
	if len(broken.Steps) != 2 || broken.Steps[0].Name != "server" || broken.Steps[1].Name != "worker" {
		t.Fatalf("steps = %+v", broken.Steps)
	}
	if got := []delta.JsonValue{member(notes, "label"), member(index, "label")}; !reflect.DeepEqual(got, []delta.JsonValue{"v2", "v2"}) {
		t.Fatalf("after the broken edit = %v, want [v2 v2]", got)
	}
	if state := notes.State().Value(); state.Kind != host.RefStateLive {
		t.Fatalf("notes state = %+v, want live", state)
	}
	if marks := plugins.Marks(); !slices.Equal(marks.Dirty, []string{"@test/notes"}) {
		t.Fatalf("marks = %+v, want @test/notes dirty", marks)
	}
}
