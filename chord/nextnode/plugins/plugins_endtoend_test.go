package plugins

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next-node/plugins/plugins.test.ts › "end to end: a server and a worker": plugin directories
// (the fixtures `contracts` and `notes`, Pi's own) loaded on a server and a worker, an edit rolled out (server, then worker),
// and a broken edit kept old on both.
//
// Pi's shape, with one observing server: the worker's host connects to its parent (here a Node host: its link to the Go
// side is NodeHost.Link, the other end of the one the host `connect`s) and the server routes `Worker` to it
// (`server.route(Worker, { authorize, resolve: () => ({ link, principal: { kind: "worker" } }) })`: the Go host's Route).
// `server.get(Notes)` is the Go host's hold over that route. Adapted to the process boundary
// (docs/plan/chord-plugins-node-evaluator.md): the server's own plugins (kind "server") run in a Node host of their own, which
// the same Go host routes (its address is `test.server`); `Index`, a local type of that host, is read with a probe held
// through the route.

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

// pcFrames collects what a watch delivers as Pi's `valuesOf` records it (a value as itself, a failure as {failed: message}).
type pcFrames struct {
	mu     sync.Mutex
	values []delta.JsonValue
}

func pcStartFrames(watch chord.WatchHandle[delta.JsonValue]) *pcFrames {
	frames := &pcFrames{}
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		frames.mu.Lock()
		if call.Kind == chord.WatchCallValue {
			frames.values = append(frames.values, call.Value)
		} else {
			frames.values = append(frames.values, map[string]any{"failed": call.Error.Error()})
		}
		frames.mu.Unlock()
		return nil
	})
	return frames
}

func (f *pcFrames) all() []delta.JsonValue {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]delta.JsonValue{}, f.values...)
}

// plugins.test.ts › end to end: a server and a worker › loads plugin directories, rolls an edit out (server, then worker),
// and keeps a broken edit old on both. Pi reads what the host's watchers saw after `transported()` and `delivered()`
// (event-loop hops). Here Rollout waits itself (NodeHost's reload wait: the Go host's deliveries entered their listeners
// when it returns), so the frame list, and what the broken edit did not change, are read right after Rollout, with no wait
// and no call as a barrier.
func TestEndToEndAServerAndAWorkerLoadsPluginDirectoriesRollsAnEditOutServerThenWorkerAndKeepsABrokenEditOldOnBoth(t *testing.T) {
	root := tempRoot(t, "contracts", "notes")
	c := pcNew(t, root)

	// The worker's host, and the server's own plugins' host (it has an address of its own for the probe).
	workerAddress := workerKind.Address(nil)
	workerNode, _, err := c.Load(PluginsLoad{
		Kind: "worker", Address: &workerAddress, Principal: workerPrincipal, Peer: serverPrincipal,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerNode.Close(background) })
	serverAddress := pcServerKind.Address(nil)
	serverNode, _, err := c.Load(PluginsLoad{
		Kind: "server", Core: []CoreFacet{pcServerProbeCore(t, c)}, Address: &serverAddress,
		Principal: serverPrincipal, Peer: serverPrincipal,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverNode.Close(background) })

	// The server: it routes `Worker` (and its own plugins' kind) to the links of the Node hosts.
	server := serverFor(t, workerRoute(workerNode), nodeRoute{kind: pcServerKind, node: serverNode, principal: serverPrincipal})
	serverProbe := probeHeld(t, server, pcServerProbeContract)
	indexLabel := func() string {
		t.Helper()
		label, ok := serverProbe.must("label", map[string]any{"dir": "index"}).(string)
		if !ok {
			t.Fatal("the index label is no text")
		}
		return label
	}
	notes, err := server.Get(background, notesContract, delta.NewJsonObject(0), host.GetOptions{})
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
	if got := []any{member("label"), indexLabel()}; !reflect.DeepEqual(got, []any{"v2", "v2"}) {
		t.Fatalf("after the broken edit = %v, want [v2 v2]", got)
	}
	if state := notes.State().Value(); !reflect.DeepEqual(state, &host.RefState{Kind: host.RefStateLive}) {
		t.Fatalf("notes state = %+v, want {kind: live}", state)
	}
	if values := frames.all(); !reflect.DeepEqual(values, []delta.JsonValue{"v2"}) {
		t.Fatalf("frames after the broken edit = %v, want [v2]", values)
	}
	if marks := c.Marks(); !slices.Equal(marks.Dirty, []string{"@test/notes"}) {
		t.Fatalf("dirty = %v, want [@test/notes]", marks.Dirty)
	}
}

// The review's case against the first port's direction (B1): a Go host that has a parent of its own loads plugins. This is the
// shape of a worker process of Pi's workers: its host `connect`s to the server that routes its kind to it (workers.ts), and
// plugins load on that host. Here the worker's Go host (a relay: no address of its own, which its kind's route would not
// reach) routes the kind to the Node host that serves the plugins' types, so `server.get(Notes)` goes server, worker's Go
// host (over its parent link), Node host. The first port made the Node host the parent of the Go host: a Go host with a
// parent could not load, and a hold relayed up to the Node host failed with "hold: an origin on a child link" (a child
// relays nothing), which the Go side never saw (it timed out).
func TestEndToEndAGoHostThatHasAParentRoutesItsKindToANodeHost(t *testing.T) {
	root := tempRoot(t, "contracts", "notes")
	c := pcNew(t, root)
	address := workerKind.Address(nil)
	node, _, err := c.Load(PluginsLoad{Kind: "worker", Address: &address, Principal: workerPrincipal, Peer: workerPrincipal}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })

	up, down := link.MemoryLinkPair()
	worker := serverFor(t, nodeRoute{kind: workerKind, node: node, principal: workerPrincipal})
	if err := worker.Connect(up, host.LinkOptions{Principal: serverPrincipal, Trusted: true}, background); err != nil {
		t.Fatal(err)
	}
	server, err := host.CreateHost(host.HostOptions{Principal: serverPrincipal, OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(background) })
	if _, err := server.Route(workerKind, host.RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(contracts.Address, context.Context) (host.ResolvedLink, error) {
			return host.ResolvedLink{Link: down, Principal: workerPrincipal, Trusted: true}, nil
		},
	}, background); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Load(nil, background); err != nil {
		t.Fatal(err)
	}
	notes, err := server.Get(background, notesContract, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(notes.Release)
	if value, err := notes.Member("label").Value(); err != nil || value != "v1" {
		t.Fatalf("label = %v, %v", value, err)
	}
	if count, err := notes.Call("add", nil, background); err != nil || count != float64(1) {
		t.Fatalf("add = %v, %v", count, err)
	}
}

// pcIndexContract is the fixtures' `Index`: a local type, served by the server kind's plugin on its Node host.
var pcIndexContract = mustContract(contracts.Local("test.index", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Str())},
}))

// Not upstream's (plugnode-differences.md, 1): a local type is the host's own, so the app's Go host cannot `get` one that a
// plugin serves on its Node host (Pi's app does that in process, `server.get(Index)`): the hold is `unavailable`, `test.index is not served here`. The same
// type is read through the probe, in the Node host.
func TestEndToEndTheAppsGoHostCannotGetALocalTypeAPluginServes(t *testing.T) {
	root := tempRoot(t, "contracts", "notes")
	c := pcNew(t, root)
	serverAddress := pcServerKind.Address(nil)
	serverNode, _, err := c.Load(PluginsLoad{
		Kind: "server", Core: []CoreFacet{pcServerProbeCore(t, c)}, Address: &serverAddress,
		Principal: serverPrincipal, Peer: serverPrincipal,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverNode.Close(background) })
	server := serverFor(t, nodeRoute{kind: pcServerKind, node: serverNode, principal: serverPrincipal})
	if label := probeHeld(t, server, pcServerProbeContract).must("label", map[string]any{"dir": "index"}); label != "v1" {
		t.Fatalf("the probe reads %v, want v1", label)
	}
	short := 500 * time.Millisecond
	ref, err := server.Get(background, pcIndexContract, delta.NewJsonObject(0), host.GetOptions{ReadyTimeout: &short})
	if err == nil {
		ref.Release()
		t.Fatal("the app's Go host got a local type of its plugins")
	}
	if !strings.Contains(err.Error(), "test.index is not served here") {
		t.Fatalf("error = %v", err)
	}
}
