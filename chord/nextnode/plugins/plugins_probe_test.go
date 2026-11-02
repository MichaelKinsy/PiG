package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// The probe: how a test reads what only the Node host can see (a facet's own members, a local type, a stack). Pi's tests run
// in the process of the host and read the evaluated facet's JavaScript objects; here the host is in a subprocess, so a test
// loads a core facet (a fixture module it writes) that serves `test.probe`, a remote type of the host's address with one
// method, `call(request)`: the request is `{op, args}` as JSON text, and the answer the JSON text `ops[op](args, ctx)`
// returns. The ops are JavaScript the test gives, run with Pi's own modules in scope (`next`: next/index.ts, `evaluate`:
// next-node/plugins/evaluate.ts, `stack`: next-node/plugins/stack.ts), so a case that Pi writes against `evaluateFacet` or
// `mapStack` calls Pi's functions in the subprocess and reports the same values. A test that reads through the probe says so.

// probeContract is `test.probe`: a remote type on the test worker.
var probeContract = mustContract(contracts.Remote("test.probe", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return workerKind.Address(nil), nil },
	Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
		Methods: []contracts.MethodEntry{contracts.Meth("call", contracts.NewMethod(contracts.Str(), contracts.Str()))},
	})},
}))

// probeCore writes the probe's module under dir and returns the core facet that loads it. cacheDir is the NodeOptions.CacheDir
// the host is started with: the module imports Pi's modules from the tree extracted there. ops is the body of a JavaScript
// object literal: `name: async (args, { ctx, self }) => value, ...`.
func probeCore(t *testing.T, cacheDir, ops string) CoreFacet {
	t.Helper()
	return probeCoreSetup(t, cacheDir, "", "", ops)
}

// probeCoreSetup is probeCore for a case whose Pi test holds something of the host's own while the probe's object is being
// made (a registry read, `self.registry(R)`, is a declaration of that object): imports is module-level JavaScript (the
// imports the setup and the ops use), setup is statements of the object's factory (`self` in scope) that put what the ops
// read into `held`, which the ops get next to `ctx` and `self`.
func probeCoreSetup(t *testing.T, cacheDir, imports, setup, ops string) CoreFacet {
	t.Helper()
	tree, err := extractNode(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	src := "file://" + filepath.ToSlash(filepath.Join(tree, "pi", "chord", "src")) + "/"
	module := `import * as next from "` + src + `next/index.ts";
import * as evaluate from "` + src + `next-node/plugins/evaluate.ts";
import * as stack from "` + src + `next-node/plugins/stack.ts";
` + imports + `
const { defineFacet, remote, method, str, hostKind } = next;
const Worker = hostKind("test.worker");
const Probe = remote("test.probe", {
	version: 1,
	params: {},
	hostAddress: () => Worker(),
	variants: { main: { state: {}, methods: { call: method(str(), str()) } } },
});
const ops = { ` + ops + ` };
export default defineFacet((host) =>
	host.serve(Probe, (_params, _principal, self) => {
		const held = {};
		` + setup + `
		return {
			variant: "main",
			members: {
				call: async (request, ctx) => {
					const { op, args } = JSON.parse(request);
					return JSON.stringify((await ops[op](args, { ctx, self, held })) ?? null);
				},
			},
		};
	}),
);
`
	file := filepath.Join(t.TempDir(), "probe.mjs")
	if err := os.WriteFile(file, []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	return CoreFacet{Name: "@test/probe", Module: file}
}

// probe is a held reference to `test.probe`, held on a Go host that routes the worker kind to the Node host.
type probe struct {
	t   *testing.T
	ref *host.Reference
}

// probeOn holds the probe of the Node host h routes the worker kind to (serverFor).
func probeOn(t *testing.T, h host.Host) *probe { return probeHeld(t, h, probeContract) }

// probeHeld holds a probe contract (`test.probe`, or the same one on another host kind) on h: a hold over h's route to the
// Node host that serves it, as the app's server holds what its worker serves.
func probeHeld(t *testing.T, h host.Host, contract *contracts.Contract) *probe {
	t.Helper()
	ref, err := h.Get(background, contract, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	return &probe{t: t, ref: ref}
}

// call runs ops[op](args) in the subprocess and decodes its JSON answer into a value; a throw is the error.
func (p *probe) call(op string, args any) (any, error) {
	p.t.Helper()
	request, err := json.Marshal(map[string]any{"op": op, "args": args})
	if err != nil {
		p.t.Fatal(err)
	}
	answer, err := p.ref.Call("call", string(request), context.Background())
	if err != nil {
		return nil, err
	}
	text, ok := answer.(string)
	if !ok {
		p.t.Fatalf("probe answer = %v, want text", answer)
	}
	var value any
	if err := json.NewDecoder(strings.NewReader(text)).Decode(&value); err != nil {
		p.t.Fatal(err)
	}
	return value, nil
}

// must is call that fails the test on an error.
func (p *probe) must(op string, args any) any {
	p.t.Helper()
	value, err := p.call(op, args)
	if err != nil {
		p.t.Fatalf("probe %s: %v", op, err)
	}
	return value
}

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

// workerKind is the fixtures' `Worker` host kind (`hostKind("test.worker")`).
var workerKind = mustKind(contracts.NewHostKind("test.worker"))

// notesContract is the fixtures' `Notes` as contracts/contract.ts declares it: a remote type on the worker, a label, a count
// and `add`.
var notesContract = mustContract(contracts.Remote("test.notes", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return workerKind.Address(nil), nil },
	Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
		State:   []contracts.Property{contracts.Prop("label", contracts.Str()), contracts.Prop("count", contracts.Int())},
		Methods: []contracts.MethodEntry{contracts.Meth("add", contracts.NewMethod(contracts.Schema{}, contracts.Int()))},
	})},
}))

func TestProbeSmoke(t *testing.T) {
	cache := t.TempDir()
	address := workerKind.Address(nil)
	node, _, err := NewPluginRuntime(NodeOptions{CacheDir: cache}).Load(RuntimeLoad{
		Kind: "worker", Core: []CoreFacet{probeCore(t, cache, `echo: async (args) => args, nope: async () => { throw new Error("nope"); }`)},
		Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	p := probeOn(t, serverFor(t, workerRoute(node)))
	if got := p.must("echo", map[string]any{"a": 1.0}); !reflect.DeepEqual(got, map[string]any{"a": 1.0}) {
		t.Fatalf("echo = %v", got)
	}
	if _, err := p.call("nope", nil); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error = %v", err)
	}
}
