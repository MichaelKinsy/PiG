package plugins

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Not upstream's (docs/plan/chord-plugins-node-evaluator.md 10.4): Go core facets in the plugins' closure. Oracle: Pi's own
// closure rule (staging.test.ts "the closure rule: a consumer edited together with a failing provider keeps its old code",
// "takes the plugins whose new code failed before staging (`failed`)") and plugins.test.ts's "the closure rule through
// builds", with the core facet on the Go side.

func bgContract(version float64, extra ...contracts.LocalEntry) *contracts.Contract {
	members := append([]contracts.LocalEntry{
		contracts.Mem("label", contracts.Str()), contracts.Mem("count", contracts.Int()),
		contracts.Mem("add", contracts.NewMethod(contracts.Int(), contracts.Int())),
	}, extra...)
	return mustContract(contracts.Local("test.gocore", contracts.LocalDeclaration{Version: version, Params: contracts.Params{contracts.Prop("id", contracts.Str())}, Members: members}))
}

var bgCore = bgContract(1)

// bgClosed counts the closes of the Go core facet's native: its objects that ended.
var bgClosed atomic.Int32

var bgCount = func() *documents.StateDefinition {
	definition, err := documents.DefineState("count", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("n", 0.0) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// bgFacet is the Go core facet: `test.gocore` with a label, a counter and `add`.
func bgFacet(contract *contracts.Contract) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(contract, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			counter := self.State(bgCount).(*documents.Singleton)
			self.Open(func(context.Context) (any, error) { return struct{}{}, nil },
				func(any, host.NativeCloseReason, context.Context) error {
					bgClosed.Add(1)
					return nil
				})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"label": self.Derive([]any{counter}, func([]any) (delta.JsonValue, error) { return "core", nil }),
					"count": self.Derive([]any{counter}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("n"), nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					"add": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						var n float64
						_, err := counter.Change(ctx, func(draft *delta.Object) (any, error) {
							n = draft.Get("n").(float64) + args.(float64)
							return nil, draft.Set("n", n)
						})
						return n, err
					},
				},
			}}, nil
		}, host.ServeOptions{})
	})
}

func bgGoHost(t *testing.T, facets ...host.HostFacet) host.Host {
	t.Helper()
	h, err := host.CreateHost(host.HostOptions{Principal: serverPrincipal, OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(background) })
	if _, err := h.Load(facets, background); err != nil {
		t.Fatal(err)
	}
	return h
}

// bgOut is `test.out`: what the plugin below serves, with the count of the Go core facet and a `bump` that calls it.
var bgOut = mustContract(contracts.Local("test.out", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	Members: []contracts.LocalEntry{
		contracts.Mem("label", contracts.Str()), contracts.Mem("count", contracts.Int()), contracts.Mem("tag", contracts.Str()),
		contracts.Mem("bump", contracts.NewMethod(contracts.Int(), contracts.Int())),
	},
}))

// bgPlugin is a script of a plugin that needs `test.gocore` of `version`, tagged tag.
func bgPlugin(t *testing.T, version int, tag string) PluginScript {
	t.Helper()
	extra := ""
	if version > 1 {
		extra = ", extra: str()"
	}
	source := `(function (require, module, exports) {
const { defineFacet, int, local, method, str } = require("@earendil-works/chord");
const Core = local("test.gocore", { version: ` + string(rune('0'+version)) + `, params: { id: str() }, members: { label: str(), count: int(), add: method(int(), int())` + extra + ` } });
const Out = local("test.out", { version: 1, params: { id: str() }, members: { label: str(), count: int(), tag: str(), bump: method(int(), int()) } });
module.exports = { default: defineFacet((host) => host.serve(Out, (_params, self) => {
	const core = self.get(Core, { id: "a" });
	return {
		label: self.derive([core.label], ([label]) => label),
		count: self.derive([core.count], ([count]) => count),
		tag: self.derive([core.label], () => "` + tag + `"),
		bump: async (by, ctx) => core.add(by, ctx),
	};
})) };
})`
	return handmade(t, "@test/plug", "worker", source)
}

func bgNode(t *testing.T, bridged []BridgedFacet, scripts ...PluginScript) *NodeHost {
	t.Helper()
	node, report, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
		Kind: "worker", Scripts: scripts, Principal: workerPrincipal, Peer: serverPrincipal, Bridged: bridged,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Failed) != 0 {
		t.Fatalf("failed = %+v", report.Failed)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	acceptorOf(t, node, serverPrincipal)
	return node
}

func bgRead(t *testing.T, ref *NodeReference, member string) delta.JsonValue {
	t.Helper()
	return rfRead(t, ref, member)
}

// A plugin needing a type of a Go core facet binds to its proxy, reads it, calls it, and watches it in order; the Go host's
// references to the object are the proxy's, released exactly with the plugin's object.
func TestBridgedAPluginNeedingAGoCoreTypeBindsReadsCallsAndWatchesIt(t *testing.T) {
	bgClosed.Store(0)
	goHost := bgGoHost(t, host.HostFacet{Name: "@app/core", Facet: bgFacet(bgCore)})
	node := bgNode(t, []BridgedFacet{{Name: "@app/core", Host: goHost, Contracts: []*contracts.Contract{bgCore}}}, bgPlugin(t, 1, "p1"))
	out, err := node.Get(background, bgOut, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(out.Release)
	if got := []any{bgRead(t, out, "label"), bgRead(t, out, "count")}; !reflect.DeepEqual(got, []any{"core", 0.0}) {
		t.Fatalf("read = %v", got)
	}
	watch, err := out.Member("count").Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen []float64
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		mu.Lock()
		defer mu.Unlock()
		if call.Kind == chord.WatchCallValue {
			seen = append(seen, call.Value.(float64))
		}
		return nil
	})
	for i := 1; i <= 10; i++ {
		got, err := out.Call("bump", 1.0, background)
		if err != nil || got != float64(i) {
			t.Fatalf("bump = %v, %v, want %d", got, err, i)
		}
	}
	// The Go host's own object has the count the plugin changed through the proxy, and a change made on the Go side reaches the plugin.
	goRef, err := goHost.Get(background, bgCore, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(goRef.Release)
	if v, _ := goRef.Member("count").Value(); v != 10.0 {
		t.Fatalf("the Go object's count = %v, want 10", v)
	}
	if _, err := goRef.Call("add", 5.0, background); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, "the Go change to reach the plugin", func() bool { return bgRead(t, out, "count") == 15.0 })
	eventually(t, 15*time.Second, "the last frame", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) > 0 && seen[len(seen)-1] == 15
	})
	mu.Lock()
	if !slices.IsSorted(seen) {
		t.Fatalf("frames out of order: %v", seen)
	}
	mu.Unlock()
	// Release is exact: the proxy holds the Go object while the plugin's object lives, and releases it with it. The Go
	// object ends (its native closes) when its last reference goes: the test's own first, then the proxy's.
	goRef.Release()
	// A round trip through the Go host's loop and through the proxy runs after the release the line above queued, so a close
	// it caused would have happened before the check below.
	bgBarrier(t, goHost)
	bgRead(t, out, "count")
	if got := bgClosed.Load(); got != 0 {
		t.Fatalf("the Go object closed %d times while the plugin's object holds it", got)
	}
	out.Release()
	eventually(t, 15*time.Second, "the proxy to release its Go reference", func() bool { return bgClosed.Load() == 1 })
}

// A Go core facet that failed on the Go host is shipped failed: the plugin that needs what its new code serves keeps its old
// code (Pi's closure rule through the proxy, plugins.test.ts "keeps a consumer edited together with a provider whose new
// build fails"); a Go core facet that loaded applies with the plugin.
func TestBridgedAFailedGoCoreFacetKeepsItsDependentPluginOldAtReload(t *testing.T) {
	core2 := bgContract(2, contracts.Mem("extra", contracts.Str()))
	for _, failed := range []bool{true, false} {
		goHost := bgGoHost(t, host.HostFacet{Name: "@app/core", Facet: bgFacet(bgCore)})
		node := bgNode(t, []BridgedFacet{{Name: "@app/core", Host: goHost, Contracts: []*contracts.Contract{bgCore}}}, bgPlugin(t, 1, "p1"))
		// The plugin's object is live while it reloads: its factory's recorded requests are what the closure rule reads.
		out, err := node.Get(background, bgOut, delta.JsonObjectOf("id", "a"), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		next := BridgedFacet{Name: "@app/core", Host: goHost, Contracts: []*contracts.Contract{core2}}
		if failed {
			next = BridgedFacet{Name: "@app/core", Failed: errors.New("the core facet failed")}
		}
		if err := node.SetBridged([]BridgedFacet{next}); err != nil {
			t.Fatal(err)
		}
		report, err := NewPluginRuntime(NodeOptions{}).Reload(node, []PluginScript{bgPlugin(t, 2, "p2")}, background)
		if err != nil {
			t.Fatal(err)
		}
		names := []string{}
		for _, f := range report.Failed {
			names = append(names, f.Plugin)
		}
		slices.Sort(names)
		if failed {
			if !reflect.DeepEqual(names, []string{"@app/core", "@test/plug"}) {
				t.Fatalf("failed = %v, want the core facet and its dependent plugin", names)
			}
			if tag := bgRead(t, out, "tag"); tag != "p1" {
				t.Fatalf("the plugin runs %v, want its old code p1", tag)
			}
			out.Release()
			// The Go side applies the closure for the plugins that failed on the Node host, never for the Go core facets themselves.
			if opts := node.ReloadOptions(report); len(opts.Failed) != 1 || opts.Failed[0].Plugin != "@test/plug" {
				t.Fatalf("ReloadOptions = %+v, want only the plugin", opts)
			}
			continue
		}
		out.Release()
		if len(names) != 0 || !slices.Equal(report.Plugins.Changed, []string{"@app/core", "@test/plug"}) && !slices.Equal(report.Plugins.Changed, []string{"@test/plug", "@app/core"}) {
			t.Fatalf("loaded core: failed = %v changed = %v", names, report.Plugins.Changed)
		}
	}
}

// bgIdx is a plugin's type as the Go side declares it (the plugin `@test/idx` serves `test.idx`), at a version.
func bgIdx(version float64, extra ...contracts.LocalEntry) *contracts.Contract {
	members := append([]contracts.LocalEntry{contracts.Mem("label", contracts.Str())}, extra...)
	return mustContract(contracts.Local("test.idx", contracts.LocalDeclaration{Version: version, Params: contracts.Params{}, Members: members}))
}

// bgStandIn is the Go side's form of the plugin `@test/idx`: a Go facet named like it that serves its type.
func bgStandIn(contract *contracts.Contract) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(contract, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			counter := self.State(bgCount).(*documents.Singleton)
			constant := func(value delta.JsonValue) chord.Watchable[delta.JsonValue] {
				return self.Derive([]any{counter}, func([]any) (delta.JsonValue, error) { return value, nil })
			}
			members := map[string]chord.Watchable[delta.JsonValue]{"label": constant("v")}
			if contract.Version > 1 {
				members["size"] = constant(1.0)
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{State: members}}, nil
		}, host.ServeOptions{})
	})
}

var bgNeeds = mustContract(contracts.Local("test.needs", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("tag", contracts.Str())},
}))

// bgNeedsFacet is a Go core facet whose `tag` is tag, needing the plugin's type at idx.
func bgNeedsFacet(idx *contracts.Contract, tag string) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(bgNeeds, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			dep := self.Get(idx, delta.NewJsonObject(0))
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"tag": self.Derive([]any{dep.Member("label")}, func([]any) (delta.JsonValue, error) { return tag, nil }),
			}}}, nil
		}, host.ServeOptions{})
	})
}

// A Go core facet that needs a plugin's type keeps its old code when that plugin's new code fails to build: the Node host's
// report names the plugin (NodeHost.ReloadOptions), and the Go host's own closure rule applies (staging.test.ts "takes the
// plugins whose new code failed before staging (`failed`)"). Without the options the Go host cannot know and applies it.
func TestBridgedAGoCoreFacetNeedingAPluginsTypeKeepsItsOldCodeWhenThePluginFailsToBuild(t *testing.T) {
	idx2 := bgIdx(2, contracts.Mem("size", contracts.Int()))
	run := func(t *testing.T, useOptions bool) (host.ReloadReport, host.Host) {
		root := t.TempDir()
		writePlugin(t, root, "idx", "@test/idx", map[string]string{
			"worker.ts": `import { constant, defineFacet, local, str } from "@earendil-works/chord";
import { label } from "./label.ts";
const Idx = local("test.idx", { version: 1, params: {}, members: { label: str() } });
export default defineFacet((host) => host.serve(Idx, () => ({ label: constant(label) })));`,
			"label.ts": `export const label = "v1";`,
		}, nil)
		c := pcNew(t, root)
		node, _, err := c.Load(PluginsLoad{Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal}, background)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = node.Close(background) })
		acceptorOf(t, node, serverPrincipal)
		goHost := bgGoHost(t,
			host.HostFacet{Name: "@test/idx", Facet: bgStandIn(bgIdx(1))},
			host.HostFacet{Name: "@app/needs", Facet: bgNeedsFacet(bgIdx(1), "old")})
		// The plugin's new code does not build; the Go side's new code needs what the plugin's new code would serve.
		pcWrite(t, root, "idx", "label.ts", "export const label = ;\n")
		c.waitMarks(t, "dirty idx", pcDirty("@test/idx"))
		report, err := c.Reload(node, c.build(t, c.Marks().Dirty...), background)
		if err != nil {
			t.Fatal(err)
		}
		if got := pcFailedList(report.Failed); !reflect.DeepEqual(got, []string{"@test/idx"}) {
			t.Fatalf("the Node host's report failed = %v", got)
		}
		options := host.ReloadOptions{}
		if useOptions {
			options = node.ReloadOptions(report)
		}
		goReport, err := goHost.Reload([]host.HostFacet{
			{Name: "@test/idx", Facet: bgStandIn(idx2)},
			{Name: "@app/needs", Facet: bgNeedsFacet(idx2, "new")},
		}, background, options)
		if err != nil {
			t.Fatal(err)
		}
		return goReport, goHost
	}
	tagOf := func(t *testing.T, h host.Host) delta.JsonValue {
		ref, err := h.Get(background, bgNeeds, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer ref.Release()
		v, err := ref.Member("tag").Value()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	withOptions, goHost := run(t, true)
	if got := pcFailedList(withOptions.Failed); !reflect.DeepEqual(got, []string{"@test/idx", "@app/needs"}) {
		t.Fatalf("the Go host's failed = %v, want the plugin's stand-in and the core facet kept old", got)
	}
	if tag := tagOf(t, goHost); tag != "old" {
		t.Fatalf("the core facet runs %v, want its old code", tag)
	}
	without, goHost2 := run(t, false)
	if len(without.Failed) != 0 || tagOf(t, goHost2) != "new" {
		t.Fatalf("without the options the Go host applied: failed = %v", pcFailedList(without.Failed))
	}
	_ = data.CodeAborted
}

// The proxies' requests to the Go host and a plugin's calls of a GoPackage function share the control channel, each with ids of
// its own that start at 1: a plugin whose method awaits a Go function and a proxied method of a Go core facet at once has a
// callback call and a proxy request in flight with the same id. Each must reach its own side (the callbacks' `call`/`answer`
// and the bridge's `bridgeCall`/`bridgeAnswer`); with one name for both, the callbacks took the bridge's answers and the
// proxy's `get` never completed ("did not become live").
func TestBridgedAProxyRequestAndAGoFunctionCallInFlightTogetherEachGetItsOwnAnswer(t *testing.T) {
	bgClosed.Store(0)
	goHost := bgGoHost(t, host.HostFacet{Name: "@app/core", Facet: bgFacet(bgCore)})
	source := `(function (require, module, exports) {
const { defineFacet, int, local, method, str } = require("@earendil-works/chord");
const go = require("@acme/go");
const Core = local("test.gocore", { version: 1, params: { id: str() }, members: { label: str(), count: int(), add: method(int(), int()) } });
const Out = local("test.out", { version: 1, params: { id: str() }, members: { label: str(), count: int(), tag: str(), bump: method(int(), int()) } });
module.exports = { default: defineFacet((host) => host.serve(Out, (_params, self) => {
	const core = self.get(Core, { id: "a" });
	return {
		label: self.derive([core.label], ([label]) => label),
		count: self.derive([core.count], ([count]) => count),
		tag: self.derive([core.label], () => "p"),
		bump: async (by, ctx) => {
			const [twice, total] = await Promise.all([go.twice(by), core.add(by, ctx)]);
			return twice * 10 + total;
		},
	};
})) };
})`
	node, report, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
		Kind: "worker", Scripts: []PluginScript{handmade(t, "@test/plug", "worker", source)}, Principal: workerPrincipal, Peer: serverPrincipal,
		Bridged: []BridgedFacet{{Name: "@app/core", Host: goHost, Contracts: []*contracts.Contract{bgCore}}},
		GoPackages: []GoPackage{{Specifier: "@acme/go", Exports: map[string]GoExport{
			"twice": GoFunc(func(_ context.Context, args delta.JsonValue) (delta.JsonValue, error) { return args.(float64) * 2, nil }),
		}}},
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	if len(report.Failed) != 0 {
		t.Fatalf("failed = %+v", report.Failed)
	}
	acceptorOf(t, node, serverPrincipal)
	out, err := node.Get(background, bgOut, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(out.Release)
	if got, err := out.Call("bump", 3.0, background); err != nil || got != 63.0 {
		t.Fatalf("bump(3) = %v, %v; want 63 (twice 6, the core's count 3)", got, err)
	}
}

// bgBarrier is a Go host's get of the core facet's object a, live and released: every turn the host's loop had queued
// before it has run.
func bgBarrier(t *testing.T, goHost host.Host) {
	t.Helper()
	ref, err := goHost.Get(background, bgCore, delta.JsonObjectOf("id", "a"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ref.Member("count").Value(); err != nil {
		t.Fatal(err)
	}
	ref.Release()
}

// Pi ends the holds of a host's objects when the host closes. The proxies' holds are the Go host's own references and
// watches, so a Go host that outlives the NodeHost must not keep them: after the process ended (closed, or killed) every
// Go reference the proxies held is released, so its object's native closes, and every watch is stopped.
func TestBridgedTheProcessEndingReleasesTheGoReferencesAndWatchesItsProxiesHeld(t *testing.T) {
	for _, mode := range []string{"close", "kill"} {
		t.Run(mode, func(t *testing.T) {
			bgClosed.Store(0)
			goHost := bgGoHost(t, host.HostFacet{Name: "@app/core", Facet: bgFacet(bgCore)})
			node := bgNode(t, []BridgedFacet{{Name: "@app/core", Host: goHost, Contracts: []*contracts.Contract{bgCore}}}, bgPlugin(t, 1, "p1"))
			out, err := node.Get(background, bgOut, delta.JsonObjectOf("id", "a"), host.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if got := bgRead(t, out, "count"); got != 0.0 {
				t.Fatalf("count = %v", got)
			}
			served := node.serving()
			served.mu.Lock()
			held, watching := len(served.references), len(served.watches)
			served.mu.Unlock()
			if held == 0 || watching == 0 {
				t.Fatalf("before the end the proxies hold %d references and %d watches, want some of each", held, watching)
			}
			if got := bgClosed.Load(); got != 0 {
				t.Fatalf("the Go object closed %d times while the plugin's object holds it", got)
			}
			if mode == "close" {
				if err := node.Close(background); err != nil {
					t.Fatal(err)
				}
			} else {
				node.kill()
			}
			eventually(t, 15*time.Second, "the Go object's native to close with the process", func() bool { return bgClosed.Load() == 1 })
			served.mu.Lock()
			held, watching = len(served.references), len(served.watches)
			served.mu.Unlock()
			if held != 0 || watching != 0 {
				t.Fatalf("after the end the proxies still hold %d references and %d watches", held, watching)
			}
		})
	}
}
