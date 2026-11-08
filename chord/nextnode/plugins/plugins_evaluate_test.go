package plugins

// pi: packages/chord/src/next-node/plugins/evaluate.ts

import (
	"errors"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/node/plugins/evaluate.test.ts: plugins written as Pi's tests write them are built by the real
// BuildFacet and evaluated by Pi's own evaluateFacet, which runs in the Node subprocess. A case that reads a JavaScript
// object of the evaluated facet (`facet.made instanceof ChordError`, `facet.values`) or calls evaluateFacet directly reads it
// through the probe (plugins_probe_test.go): an op that calls `evaluate.evaluateFacet(built, { hostPackages, plugin })` with
// the built script and the plugin as JSON, and the host packages as Pi's `HOSTS` built inside the op. A case about an error
// of a plugin's load is also read in the LoadReport of the real load (`*NodeError`.Message and .Stack).

// evaluateHostPackages is the keys of evaluate.test.ts's `HOSTS`: what its builds name as host packages.
var evaluateHostPackages = []string{"@earendil-works/chord", "esm-host", "cjs-host"}

// evaluateOpsPrelude is the probe ops shared by the evaluate and the stack tests. `packages` is Pi's `HOSTS` (hosts: "all"),
// support.ts's `HOST_PACKAGES` ("chord") or `{}` ("none"); `run` is `evaluateFacet(script, { hostPackages, plugin })`;
// `collected` is `expectCollected` and `settle` is `settledHeap` (test/next/support.ts) with the process's `gc`.
const evaluateOpsPrelude = `
	packages: async (a) => {
		if (a.hosts === "none") return {};
		const chord = { "@earendil-works/chord": next };
		if (a.hosts === "chord") return chord;
		return { ...chord, "esm-host": await import(a.esm), "cjs-host": (await import(a.cjs)).default };
	},
	run: (a, hostPackages) => evaluate.evaluateFacet(a.script.built, { hostPackages, plugin: a.script.plugin }),
	facetOf: async (a) => ops.run(a, await ops.packages(a)),
	collected: async (ref) => {
		for (let round = 0; round < 10 && ref.deref() !== undefined; round++) {
			await new Promise((resolve) => setImmediate(resolve));
			globalThis.gc();
		}
		return ref.deref() === undefined;
	},
	settle: async () => {
		for (let round = 0; round < 5; round++) {
			await new Promise((resolve) => setImmediate(resolve));
			globalThis.gc();
		}
	},
	thrownBy: (facet) => {
		try {
			facet.fail();
		} catch (error) {
			return error;
		}
		throw new Error("fail did not throw");
	},
`

// evaluateOps is the ops of the evaluate tests.
const evaluateOps = evaluateOpsPrelude + `
	made: async (a) => {
		const facet = await ops.facetOf(a);
		return {
			instance: facet.made instanceof next.ChordError,
			code: next.isChordError(facet.made, "unavailable"),
			accepts: facet.is(new next.ChordError({ code: "unavailable", message: "x" })),
		};
	},
	values: async (a) => (await ops.facetOf(a)).values,
	files: async (a, { ctx }) => {
		const facet = await ops.facetOf(a);
		const Files = next.local("test.files", { version: 1, params: {}, members: { text: next.str() } });
		const host = next.createHost({ onError: () => {} });
		await host.load([{ name: "@test/files", facet }], ctx);
		const text = (await host.get(Files, {}, ctx)).text.value;
		await host.close(ctx);
		return text;
	},
	pluginFiles: async (a) => {
		const probe = await ops.facetOf(a);
		let escape;
		try {
			probe.escape();
		} catch (error) {
			escape = error.message;
		}
		let outside;
		try {
			evaluate.pluginFile("x");
		} catch (error) {
			outside = error.message;
		}
		return { escape, outside };
	},
	evaluated: async (a) => {
		const facet = evaluate.evaluateFacet(a.script.built, { hostPackages: a.packages, plugin: a.script.plugin });
		const out = {};
		for (const method of a.methods) {
			try {
				out[method] = { value: await facet[method]() };
			} catch (error) {
				out[method] = { threw: error.message };
			}
		}
		return out;
	},
	outcome: async (a) => {
		const hostPackages = await ops.packages(a);
		try {
			ops.run(a, hostPackages);
			return { threw: false };
		} catch (error) {
			return { threw: true, message: error.message };
		}
	},
	dynamic: async (a) => {
		const facet = await ops.facetOf(a);
		let rejected;
		try {
			await facet.later("missing-package");
		} catch (error) {
			rejected = error.message;
		}
		return { rejected, named: (await facet.later("cjs-host")).named };
	},
	failure: async (a) => {
		const facet = await ops.facetOf(a);
		return ops.thrownBy(facet).stack;
	},
	namespaces: async (a) => {
		const make = (namespace) => ops.run(a, { "fake-esm": namespace });
		// Wrapping reads each export once: twice evaluated, read once.
		let reads = 0;
		const namespace = {
			[Symbol.toStringTag]: "Module",
			get named() {
				reads++;
				return 1;
			},
		};
		const values = [make(namespace).ns.named, make(namespace).ns.named];
		const ref = (() => {
			const dropped = { [Symbol.toStringTag]: "Module", named: 2 };
			const value = make(dropped).ns.named;
			return { value, ref: new WeakRef(dropped) };
		})();
		return { values, reads, dropped: ref.value, collected: await ops.collected(ref.ref) };
	},
`

// evaluateProbe starts a Node host (routed from a new Go host) with the probe's ops, and holds the probe; execArgv are the
// subprocess's Node flags (`--expose-gc` for a GC case).
func evaluateProbe(t *testing.T, ops string, execArgv ...string) *probe {
	t.Helper()
	cache := t.TempDir()
	address := workerKind.Address(nil)
	node, _, err := NewPluginRuntime(NodeOptions{CacheDir: cache, ExecArgv: execArgv}).Load(RuntimeLoad{
		Kind: "worker", Core: []CoreFacet{probeCore(t, cache, ops)},
		Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	return probeOn(t, serverFor(t, workerRoute(node)))
}

// evaluateBuilt builds name's kind facet under root for Node (test's `built`: the plugins of root are the build's, the host
// packages are hostPackages) and returns the script with its plugin.
func evaluateBuilt(t *testing.T, root, name, kind string, hostPackages []string) PluginScript {
	t.Helper()
	discovery, err := DiscoverPlugins([]PluginRoot{{Path: root}}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	for _, plugin := range discovery.Plugins {
		if plugin.Name != name {
			continue
		}
		built, err := BuildFacet(plugin, kind, BuildOptions{HostPackages: hostPackages, Platform: PlatformNode, Plugins: discovery.Plugins}, background)
		if err != nil {
			t.Fatal(err)
		}
		if built.Kind == BuiltFacetFailed {
			t.Fatal(strings.Join(built.Errors, "\n"))
		}
		return PluginScript{Plugin: plugin, Built: built}
	}
	t.Fatalf("no plugin %q in %+v", name, discovery)
	return PluginScript{}
}

// evaluateFixtureURL is the file URL of a host package fixture of testdata/hosts.
func evaluateFixtureURL(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", "hosts", name))
	if err != nil {
		t.Fatal(err)
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

// evaluateArgs are the arguments of an op that evaluates script with the host packages hosts names: "all" (evaluate.test.ts's
// HOSTS: the host's `next`, the `esm-host` fixture's namespace, the `cjs-host` fixture's exports), "chord" (HOST_PACKAGES)
// or "none" (`{}`).
func evaluateArgs(t *testing.T, script PluginScript, hosts string) map[string]any {
	t.Helper()
	return map[string]any{
		"script": scriptsWire([]PluginScript{script})[0], "hosts": hosts,
		"esm": evaluateFixtureURL(t, "esm-host.ts"), "cjs": evaluateFixtureURL(t, "cjs-host.cjs"),
	}
}

// evaluateReport loads the scripts as kind into a Node host and returns its LoadReport: the failure of each plugin as the
// host reported it.
func evaluateReport(t *testing.T, kind string, scripts ...PluginScript) host.LoadReport {
	t.Helper()
	node, report, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
		Kind: kind, Scripts: scripts, Principal: workerPrincipal, Peer: serverPrincipal,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	return report
}

// evaluateFailure is the one failure of a report as the *NodeError the load reported it as.
func evaluateFailure(t *testing.T, report host.LoadReport, plugin string) *NodeError {
	t.Helper()
	if len(report.Failed) != 1 || report.Failed[0].Plugin != plugin {
		t.Fatalf("failed = %+v", report.Failed)
	}
	var nodeErr *NodeError
	if !errors.As(report.Failed[0].Error, &nodeErr) {
		t.Fatalf("error = %T %v, want a *NodeError", report.Failed[0].Error, report.Failed[0].Error)
	}
	return nodeErr
}

// asMap is an op's answer as an object.
func asMap(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("answer = %v (%T), want an object", value, value)
	}
	return object
}

// evaluate.test.ts › gives host packages as the host's instances: a ChordError crosses the plugin as one (read through the
// probe: the facet's `made` and `is`).
func TestEvaluateGivesHostPackagesAsTheHostsInstances(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "p", "@test/p", map[string]string{
		"worker.ts": strings.Join([]string{
			`import { ChordError, defineFacet } from "@earendil-works/chord";`,
			"const facet = defineFacet(() => {});",
			`export default { ...facet, made: new ChordError({ code: "unavailable", message: "m" }), is: (e: unknown) => e instanceof ChordError };`,
		}, "\n"),
	}, nil)
	script := evaluateBuilt(t, root, "@test/p", "worker", evaluateHostPackages)
	p := evaluateProbe(t, evaluateOps)
	got := asMap(t, p.must("made", evaluateArgs(t, script, "all")))
	want := map[string]any{"instance": true, "code": true, "accepts": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("made = %v, want %v", got, want)
	}
}

// evaluate.test.ts › answers default and named imports of an ESM and a CommonJS host package (read through the probe: the
// facet's `values`).
func TestEvaluateAnswersDefaultAndNamedImportsOfAnESMAndACommonJSHostPackage(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "p", "@test/p", map[string]string{
		"worker.ts": strings.Join([]string{
			`import esm, { named as esmNamed } from "esm-host";`,
			`import * as esmAll from "esm-host";`,
			`import cjs, { named as cjsNamed } from "cjs-host";`,
			"export default { setup: () => {}, values: [esm, esmNamed, esmAll.named, cjs, cjsNamed] };",
		}, "\n"),
	}, nil)
	script := evaluateBuilt(t, root, "@test/p", "worker", evaluateHostPackages)
	p := evaluateProbe(t, evaluateOps)
	got := p.must("values", evaluateArgs(t, script, "all"))
	want := []any{"esm default", "esm named", "esm named", map[string]any{"named": "cjs named"}, "cjs named"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
}

// evaluate.test.ts › loads chord.external packages and Node builtins from the plugin's directory (read through the probe: the
// facet's `values`).
func TestEvaluateLoadsChordExternalPackagesAndNodeBuiltinsFromThePluginsDirectory(t *testing.T) {
	root := tempRoot(t)
	dir := writePlugin(t, root, "p", "@test/p", map[string]string{
		"worker.ts": "import ext from \"ext-dep\";\nimport { basename } from \"node:path\";\nexport default { setup: () => {}, values: [ext, basename(\"/a/b\")] };",
	}, map[string]any{"chord": map[string]any{"external": []string{"ext-dep"}}})
	writeNpmPackage(t, dir, "ext-dep", "index.js", `module.exports = "from the plugin";`)
	script := evaluateBuilt(t, root, "@test/p", "worker", evaluateHostPackages)
	p := evaluateProbe(t, evaluateOps)
	got := p.must("values", evaluateArgs(t, script, "all"))
	if want := []any{"from the plugin", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
}

// evaluate.test.ts › gives each facet pluginFile for its own plugin's directory and refuses .. out of it (read through the
// probe: the facet is loaded into a host made in the subprocess, as the test does, and its `escape` called there).
func TestEvaluateGivesEachFacetPluginFileForItsOwnPluginsDirectoryAndRefusesDotDotOutOfIt(t *testing.T) {
	root := tempRoot(t, "contracts", "files")
	script := evaluateBuilt(t, root, "@test/files", "worker", evaluateHostPackages)
	p := evaluateProbe(t, evaluateOps)
	if got := p.must("files", evaluateArgs(t, script, "all")); got != "a file of the plugin" {
		t.Fatalf("text = %v", got)
	}
	writePlugin(t, root, "escape", "@test/escape", map[string]string{
		"worker.ts": "import { pluginFile } from \"@earendil-works/chord/plugins\";\nexport default { setup: () => {}, escape: () => pluginFile(\"../files/assets/data.txt\") };",
	}, nil)
	escaping := evaluateBuilt(t, root, "@test/escape", "worker", evaluateHostPackages)
	got := asMap(t, p.must("pluginFiles", evaluateArgs(t, escaping, "all")))
	if want := `pluginFile("../files/assets/data.txt"): outside the directory of plugin "@test/escape"`; !strings.Contains(got["escape"].(string), want) {
		t.Errorf("escape threw %v, want %q", got["escape"], want)
	}
	// Outside an evaluated facet it has none.
	if want := "only a plugin's facet script"; !strings.Contains(got["outside"].(string), want) {
		t.Errorf("pluginFile outside threw %v, want %q", got["outside"], want)
	}
}

// evaluate.test.ts › refuses pluginFile in a single-file plugin: it has no directory of its own (slice 12 §4) (read through the
// probe's `evaluateFacet`).
func TestEvaluateRefusesPluginFileInASingleFilePluginItHasNoDirectoryOfItsOwn(t *testing.T) {
	root := tempRoot(t)
	writeFiles(t, root, map[string]string{
		"lone.worker.ts": "import { pluginFile } from \"@earendil-works/chord/plugins\";\nexport default { setup: () => {}, read: () => pluginFile(\"x.txt\") };",
	})
	script := evaluateBuilt(t, root, "lone", "worker", evaluateHostPackages)
	p := evaluateProbe(t, evaluateOps)
	args := evaluateArgs(t, script, "all")
	args["packages"] = map[string]any{}
	args["methods"] = []string{"read"}
	got := asMap(t, asMap(t, p.must("evaluated", args))["read"])
	if want := `pluginFile("x.txt"): single-file plugin "lone" has no directory of its own`; !strings.Contains(got["threw"].(string), want) {
		t.Fatalf("read = %v, want a throw containing %q", got, want)
	}
}

// evaluate.test.ts › a computed require refuses what the build refuses (slice 12 fix round 1, Sol) › refuses another version
// of the app's API, Chord itself, and builtins in a single-file plugin (each read through the probe's `evaluateFacet`).
func TestEvaluateAComputedRequireRefusesWhatTheBuildRefusesAnotherVersionOfTheAppsAPIChordItselfAndBuiltinsInASingleFilePlugin(t *testing.T) {
	root := tempRoot(t)
	api := &AppApi{Name: "@acme/app", Versions: []string{"v3", "v2"}}
	packages := map[string]any{"@acme/app/v3": map[string]any{"value": 3}, "@acme/app/v2": map[string]any{"value": 2}, "@earendil-works/chord": map[string]any{}}
	hostPackages := []string{"@acme/app/v3", "@acme/app/v2", "@earendil-works/chord"}
	join := func(parts ...string) string {
		quoted := make([]string, len(parts))
		for i, part := range parts {
			quoted[i] = pcJSON(t, part)
		}
		return "[" + strings.Join(quoted, ", ") + `].join("/")`
	}
	writePlugin(t, root, "mixed", "@test/mixed", map[string]string{
		"worker.ts": strings.Join([]string{
			`import { value } from "@acme/app/v3";`,
			"export default { setup: () => {}, other: () => require(" + join("@acme/app", "v2") + "), chord: () => require(" + join("@earendil-works", "chord", "next") + "), same: () => require(" + join("@acme/app", "v3") + ").value + value };",
		}, "\n"),
	}, nil)
	writeFiles(t, root, map[string]string{
		"lone.worker.ts": `export default { setup: () => {}, builtin: () => require(["node", "path"].join(":")) };`,
	})
	p := evaluateProbe(t, evaluateOps)
	evaluated := func(name string, methods ...string) map[string]any {
		t.Helper()
		discovery, err := DiscoverPlugins([]PluginRoot{{Path: root}}, DiscoveryOptions{Kinds: kinds}, background)
		if err != nil {
			t.Fatal(err)
		}
		for _, plugin := range discovery.Plugins {
			if plugin.Name != name {
				continue
			}
			built, err := BuildFacet(plugin, "worker", BuildOptions{HostPackages: hostPackages, Platform: PlatformNode, Api: api, Kinds: kinds, Plugins: discovery.Plugins}, background)
			if err != nil {
				t.Fatal(err)
			}
			if built.Kind == BuiltFacetFailed {
				t.Fatal(strings.Join(built.Errors, "\n"))
			}
			args := evaluateArgs(t, PluginScript{Plugin: plugin, Built: built}, "none")
			args["packages"], args["methods"] = packages, methods
			return asMap(t, p.must("evaluated", args))
		}
		t.Fatalf("no plugin %q", name)
		return nil
	}
	threw := func(result map[string]any, method string) string {
		t.Helper()
		text, _ := asMap(t, result[method])["threw"].(string)
		return text
	}
	mixed := evaluated("@test/mixed", "same", "other", "chord")
	if got := asMap(t, mixed["same"])["value"]; got != float64(6) {
		t.Fatalf("same = %v, want 6", got)
	}
	if want := `"@acme/app/v2": this script uses @acme/app/v3: one facet uses one version of the API`; !strings.Contains(threw(mixed, "other"), want) {
		t.Fatalf("other = %v, want a throw containing %q", mixed["other"], want)
	}
	if want := "plugins import Chord through the app's API"; !strings.Contains(threw(mixed, "chord"), want) {
		t.Fatalf("chord = %v, want a throw containing %q", mixed["chord"], want)
	}
	// Without a static import the first version required pins the facet's (Opus N3).
	writePlugin(t, root, "computed", "@test/computed", map[string]string{
		"worker.ts": "export default { setup: () => {}, both: () => [require(" + join("@acme/app", "v3") + ").value, require(" + join("@acme/app", "v2") + ").value] };",
	}, nil)
	computed := evaluated("@test/computed", "both")
	if want := `"@acme/app/v2": this script uses @acme/app/v3: one facet uses one version of the API`; !strings.Contains(threw(computed, "both"), want) {
		t.Fatalf("both = %v, want a throw containing %q", computed["both"], want)
	}
	lone := evaluated("lone", "builtin")
	if want := `"node:path": a single-file plugin requires only host packages`; !strings.Contains(threw(lone, "builtin"), want) {
		t.Fatalf("builtin = %v, want a throw containing %q", lone["builtin"], want)
	}
}

// evaluate.test.ts › throws an import-time throw, a non-facet export and a refused require, naming the facet (each read
// through the probe's `evaluateFacet`; the first two again in the LoadReport of a real load of the built scripts).
func TestEvaluateThrowsAnImportTimeThrowANonFacetExportAndARefusedRequireNamingTheFacet(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "p", "@test/p", map[string]string{
		"worker.ts": "throw new Error(\"import time\");\nexport default { setup: () => {} };",
		"server.ts": "export default 42;",
	}, nil)
	thrown := evaluateBuilt(t, root, "@test/p", "worker", evaluateHostPackages)
	notFacet := evaluateBuilt(t, root, "@test/p", "server", evaluateHostPackages)
	refused := thrown
	refused.Built.Source = `(function (require, module, exports) { require("nope"); })`
	p := evaluateProbe(t, evaluateOps)
	for _, each := range []struct {
		name   string
		script PluginScript
		want   string
	}{
		{"an import-time throw", thrown, "import time"},
		{"a non-facet export", notFacet, `plugin "@test/p" (server): its default export is not a facet (defineFacet)`},
		{"a refused require", refused, `plugin "@test/p" (worker): "nope" is not a host package, a Node builtin or one of its chord.external packages`},
	} {
		got := asMap(t, p.must("outcome", evaluateArgs(t, each.script, "all")))
		if got["threw"] != true || !strings.Contains(got["message"].(string), each.want) {
			t.Errorf("%s: outcome = %v, want a throw of %q", each.name, got, each.want)
		}
	}
	if err := evaluateFailure(t, evaluateReport(t, "worker", thrown), "@test/p"); err.Message != "import time" || !strings.Contains(err.Stack, "p/worker.ts:1:") {
		t.Errorf("import-time throw reported as %+v", err)
	}
	if err := evaluateFailure(t, evaluateReport(t, "server", notFacet), "@test/p"); err.Message != `plugin "@test/p" (server): its default export is not a facet (defineFacet)` {
		t.Errorf("non-facet export reported as %+v", err)
	}
}

// evaluate.test.ts › runs a computed import() through the facet's require: an unknown one names the facet (Astra) (read through
// the probe: the facet's `later`).
func TestEvaluateRunsAComputedImportThroughTheFacetsRequire(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "dynamic", "@test/dynamic", map[string]string{
		"worker.ts": strings.Join([]string{
			`import { defineFacet } from "@earendil-works/chord";`,
			"const later = (specifier: string) => import(specifier);",
			"export default { ...defineFacet(() => {}), later };",
		}, "\n"),
	}, nil)
	script := evaluateBuilt(t, root, "@test/dynamic", "worker", evaluateHostPackages)
	p := evaluateProbe(t, evaluateOps)
	got := asMap(t, p.must("dynamic", evaluateArgs(t, script, "all")))
	want := `plugin "@test/dynamic" (worker): "missing-package" is not a host package`
	if rejected, _ := got["rejected"].(string); !strings.Contains(rejected, want) {
		t.Errorf("later(\"missing-package\") rejected with %v, want %q", got["rejected"], want)
	}
	if got["named"] != "cjs named" {
		t.Errorf("later(\"cjs-host\").named = %v", got["named"])
	}
}

// evaluate.test.ts › runs a script under a stable filename per plugin, kind and digest (read through the probe: the stack of
// the facet's `fail`).
func TestEvaluateRunsAScriptUnderAStableFilenamePerPluginKindAndDigest(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "p", "@test/p", map[string]string{
		"worker.ts": `export default { setup: () => {}, fail: () => { throw new Error("x"); } };`,
	}, nil)
	script := evaluateBuilt(t, root, "@test/p", "worker", evaluateHostPackages)
	p := evaluateProbe(t, evaluateOps)
	stack, _ := p.must("failure", evaluateArgs(t, script, "all")).(string)
	if want := "/chord-plugins/@test/p/worker.ts?" + script.Built.Digest + ":"; !strings.Contains(stack, want) {
		t.Fatalf("stack = %q, want it to contain %q", stack, want)
	}
}

// evaluate.test.ts › ESM host packages' wrappers › wraps a namespace once, and keeps no namespace nobody else holds (read
// through the probe, started with --expose-gc: the namespaces are built, evaluated and collected in the subprocess).
func TestEvaluateWrapsANamespaceOnceAndKeepsNoNamespaceNobodyElseHolds(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "p", "@test/p", map[string]string{
		"worker.ts": "import * as ns from \"fake-esm\";\nexport default { setup: () => {}, ns };",
	}, nil)
	script := evaluateBuilt(t, root, "@test/p", "worker", []string{"fake-esm"})
	p := evaluateProbe(t, evaluateOps, "--expose-gc")
	got := asMap(t, p.must("namespaces", evaluateArgs(t, script, "none")))
	if want := []any{1.0, 1.0}; !reflect.DeepEqual(got["values"], want) {
		t.Errorf("values = %v, want %v", got["values"], want)
	}
	if got["reads"] != 1.0 {
		t.Errorf("reads = %v, want 1", got["reads"])
	}
	if got["dropped"] != 2.0 {
		t.Errorf("dropped's named = %v, want 2", got["dropped"])
	}
	if got["collected"] != true {
		t.Errorf("the namespace nobody else holds was not collected")
	}
}
