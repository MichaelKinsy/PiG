package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/next-node/plugins/plugins.test.ts › "end to end: a server and a worker": plugin directories
// (the fixtures `contracts` and `notes`, Pi's own) loaded on a server and a worker, an edit rolled out (server, then worker),
// and a broken edit kept old on both.
//
// Adapted to the process boundary (docs/plan/chord-plugins-node-evaluator.md): a Node host is one process with one parent
// link, so the server and the worker are two Node hosts of the component (kind "server", kind "worker"), each the parent of
// a Go host that observes it. Pi's `server.get(Notes)` goes over a route of the server host to the worker host; here the
// worker's Go host holds `test.notes` over its parent link from the worker's Node host (a route from the server's Go host to
// the worker's was tried: the hold never went live, so the two observers stay separate). `Index` is a local type of the
// server's Node host: it is read with a probe of the server (the worker's is plugins_probe_test.go's, on the worker kind).

// pcServerKind is the host kind of the server's Node host, which serves the server probe.
var pcServerKind = mustKind(contracts.NewHostKind("test.server"))

// pcServerProbeContract is `test.sprobe`: probeContract on the server kind.
var pcServerProbeContract = mustContract(contracts.Remote("test.sprobe", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return pcServerKind.Address(nil), nil },
	Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
		Methods: []contracts.MethodEntry{contracts.Meth("call", contracts.NewMethod(contracts.Str(), contracts.Str()))},
	})},
}))

// pcServerProbeCore is probeCore's module for the server kind: it serves `test.sprobe`, whose one method runs the labelled
// op (pcLabelOps) in the server's Node host.
func pcServerProbeCore(t *testing.T, c *pcComponent) CoreFacet {
	t.Helper()
	module := `import * as next from "` + c.src(t) + `next/index.ts";
const { defineFacet, remote, method, str, hostKind } = next;
const Server = hostKind("test.server");
const Probe = remote("test.sprobe", {
	version: 1,
	params: {},
	hostAddress: () => Server(),
	variants: { main: { state: {}, methods: { call: method(str(), str()) } } },
});
const ops = { ` + pcLabelOps + ` };
export default defineFacet((host) =>
	host.serve(Probe, (_params, _principal, self) => ({
		variant: "main",
		members: {
			call: async (request, ctx) => {
				const { op, args } = JSON.parse(request);
				return JSON.stringify((await ops[op](args, { ctx, self })) ?? null);
			},
		},
	})),
);
`
	file := filepath.Join(t.TempDir(), "sprobe.mjs")
	if err := os.WriteFile(file, []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	return CoreFacet{Name: "@test/sprobe", Module: file}
}

// pcFrames collects the values a watch delivers, and announces each.
type pcFrames struct {
	mu     sync.Mutex
	values []delta.JsonValue
	got    chan struct{}
}

func pcStartFrames(watch chord.WatchHandle[delta.JsonValue]) *pcFrames {
	frames := &pcFrames{got: make(chan struct{}, 64)}
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			frames.mu.Lock()
			frames.values = append(frames.values, call.Value)
			frames.mu.Unlock()
			frames.got <- struct{}{}
		}
		return nil
	})
	return frames
}

func (f *pcFrames) all() []delta.JsonValue {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]delta.JsonValue{}, f.values...)
}

// next waits for the next frame to arrive (an event, not a poll).
func (f *pcFrames) next(t *testing.T) {
	t.Helper()
	select {
	case <-f.got:
	case <-time.After(30 * time.Second):
		t.Fatal("no frame arrived")
	}
}

// plugins.test.ts › end to end: a server and a worker › loads plugin directories, rolls an edit out (server, then worker),
// and keeps a broken edit old on both. `Index` (local to the server's Node host) is read through the server's probe; `notes`
// through the worker's Go host. Pi's `transported()` and `delivered()` (event-loop hops) are the arrival of the frame
// (v2), and, for the broken edit, where nothing may change, a call of `add` as a barrier: every frame the worker's host
// sent before its reply has arrived by then (count 2 is that call's, not Pi's).
func TestEndToEndAServerAndAWorkerLoadsPluginDirectoriesRollsAnEditOutServerThenWorkerAndKeepsABrokenEditOldOnBoth(t *testing.T) {
	root := tempRoot(t, "contracts", "notes")
	c := pcNew(t, root)

	// The worker: its Node host, and the Go host that is its child.
	workerGo, err := host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerGo.Close(background) })
	if _, err := workerGo.Load(nil, background); err != nil {
		t.Fatal(err)
	}
	workerAddress := workerKind.Address(nil)
	workerNode, _, err := c.Load(PluginsLoad{
		Kind: "worker", Parent: workerGo, Address: &workerAddress, Principal: workerPrincipal, Peer: serverPrincipal,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerNode.Close(background) })

	serverGo, err := host.CreateHost(host.HostOptions{Principal: serverPrincipal, OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverGo.Close(background) })
	if _, err := serverGo.Load(nil, background); err != nil {
		t.Fatal(err)
	}
	serverAddress := pcServerKind.Address(nil)
	serverNode, _, err := c.Load(PluginsLoad{
		Kind: "server", Core: []CoreFacet{pcServerProbeCore(t, c)}, Parent: serverGo, Address: &serverAddress,
		Principal: serverPrincipal, Peer: delta.JsonObjectOf("kind", "observer"),
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverNode.Close(background) })

	probeRef, err := serverGo.Get(pcServerProbeContract, delta.NewJsonObject(0), background, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(probeRef.Release)
	indexLabel := func() string {
		t.Helper()
		request, _ := json.Marshal(map[string]any{"op": "label", "args": map[string]any{"dir": "index"}})
		answer, err := probeRef.Call("call", string(request), background)
		if err != nil {
			t.Fatal(err)
		}
		var label string
		if err := json.Unmarshal([]byte(answer.(string)), &label); err != nil {
			t.Fatal(err)
		}
		return label
	}
	notes, err := workerGo.Get(notesContract, delta.NewJsonObject(0), background, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(notes.Release)
	member := func(name string) delta.JsonValue {
		t.Helper()
		value, err := notes.Member(name).Value()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := []any{member("label"), indexLabel()}; !reflect.DeepEqual(got, []any{"v1", "v1"}) {
		t.Fatalf("labels = %v, want [v1 v1]", got)
	}
	if _, err := notes.Call("add", nil, background); err != nil {
		t.Fatal(err)
	}
	watch, err := notes.Member("label").Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	frames := pcStartFrames(watch)
	serverStep, err := c.Step("server", serverNode)
	if err != nil {
		t.Fatal(err)
	}
	workerStep, err := c.Step("worker", workerNode)
	if err != nil {
		t.Fatal(err)
	}
	steps := []RolloutStep{serverStep, workerStep}

	pcWrite(t, root, "notes", "label.ts", "export const label = \"v2\";\n")
	c.waitMarks(t, "dirty notes", pcDirty("@test/notes"))
	rolled := c.rollout(t, steps, c.build(t, c.Marks().Dirty...))
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
	frames.next(t)
	if values := frames.all(); !reflect.DeepEqual(values, []delta.JsonValue{"v2"}) {
		t.Fatalf("frames = %v, want [v2]", values)
	}
	if got := []any{member("label"), member("count"), indexLabel()}; !reflect.DeepEqual(got, []any{"v2", float64(1), "v2"}) {
		t.Fatalf("after the edit = %v, want [v2 1 v2]", got)
	}
	if marks := c.Marks(); len(marks.Dirty) != 0 {
		t.Fatalf("dirty = %v, want none", marks.Dirty)
	}

	pcWrite(t, root, "notes", "label.ts", "export const label = ;\n")
	c.waitMarks(t, "dirty notes", pcDirty("@test/notes"))
	broken := c.rollout(t, steps, c.build(t, c.Marks().Dirty...))
	var brokenSteps [][2]any
	for _, each := range broken.Steps {
		brokenSteps = append(brokenSteps, [2]any{each.Name, pcFailedList(each.Hosts[0].Report.Failed)})
	}
	if want := [][2]any{{"server", []string{"@test/notes"}}, {"worker", []string{"@test/notes"}}}; !reflect.DeepEqual(brokenSteps, want) {
		t.Fatalf("broken = %v, want %v", brokenSteps, want)
	}
	// The barrier: what the worker's host sent before this reply has arrived.
	if _, err := notes.Call("add", nil, background); err != nil {
		t.Fatal(err)
	}
	if got := []any{member("label"), indexLabel()}; !reflect.DeepEqual(got, []any{"v2", "v2"}) {
		t.Fatalf("after the broken edit = %v, want [v2 v2]", got)
	}
	if state := notes.State().Value(); state.Kind != host.RefStateLive {
		t.Fatalf("notes state = %+v, want live", state)
	}
	if values := frames.all(); !slices.Equal(values, []delta.JsonValue{"v2"}) {
		t.Fatalf("frames after the broken edit = %v, want [v2]", values)
	}
	if marks := c.Marks(); !slices.Equal(marks.Dirty, []string{"@test/notes"}) {
		t.Fatalf("dirty = %v, want [@test/notes]", marks.Dirty)
	}
}
