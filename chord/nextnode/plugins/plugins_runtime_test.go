package plugins

// pi: packages/chord/src/next-node/plugins/runtime.ts

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/build"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports packages/chord/test/next-node/plugins/runtime.test.ts. Tests of the plugin runtime (slice 10 §3, D11): loading and
// reloading hosts with complete sets of shipped scripts (unchanged digests kept as the same facet, removed = loaded minus
// shipped, failed builds and evaluations kept old), a required root's failed plugin rejecting `load`, and that importing it
// loads no esbuild. The runtime runs, as Pi's own
// code, in a Node subprocess: the scripts are built by BuildFacet from real TypeScript plugins, and a plugin's label, a
// local type of that process, is read through the probe (plugins_probe_test.go), which calls `ctx.get` in the subprocess.

// pcLabelOps is the probe op every test reads a plugin's `test.<dir>` with: `ctx.get` of `Item` in the subprocess, as
// runtime.test.ts's `label(host, dir)` does with `host.get`.
const pcLabelOps = `label: async ({ dir }, { ctx }) => {
	const Item = next.local("test." + dir, { version: 1, params: {}, members: { label: str() } });
	return (await ctx.get(Item, {})).label.value;
},
`

// pcHostPackages are the host packages every fixture build keeps external.
var pcHostPackages = []string{"@earendil-works/chord"}

// pcJSON is value as JavaScript's JSON.stringify writes it.
func pcJSON(t *testing.T, value any) string {
	t.Helper()
	text, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// pcItemPlugin writes plugin `@test/<dir>` serving `test.<dir>` labelled label from its worker facet (runtime.test.ts's
// `plugin`, plugins.test.ts's `itemPlugin`).
func pcItemPlugin(t *testing.T, root, dir, label string) {
	t.Helper()
	pcPluginSource(t, root, dir, strings.Join([]string{
		`import { constant, defineFacet, local, str } from "@earendil-works/chord";`,
		fmt.Sprintf(`const Item = local("test.%s", { version: 1, params: {}, members: { label: str() } });`, dir),
		fmt.Sprintf(`export default defineFacet((host) => host.serve(Item, () => ({ label: constant(%s) })));`, pcJSON(t, label)),
	}, "\n"))
}

// pcPluginSource writes plugin `@test/<dir>` whose worker facet is source as it is.
func pcPluginSource(t *testing.T, root, dir, source string) {
	t.Helper()
	writePlugin(t, root, dir, "@test/"+dir, map[string]string{"worker.ts": source}, nil)
}

// pcShipped is the worker scripts of every plugin under root, built now: what the server would ship (runtime.test.ts's
// `shipped`).
func pcShipped(t *testing.T, root string) []PluginScript {
	t.Helper()
	return pcShippedRoots(t, []model.PluginRoot{{Path: root}})
}

// pcShippedRoots is pcShipped over roots (some of them required).
func pcShippedRoots(t *testing.T, roots []model.PluginRoot) []PluginScript {
	t.Helper()
	discovery, err := model.DiscoverPlugins(roots, model.DiscoveryOptions{Kinds: []string{"worker"}}, background)
	if err != nil {
		t.Fatal(err)
	}
	options := model.BuildOptions{HostPackages: pcHostPackages, Platform: model.PlatformNode, Plugins: discovery.Plugins}
	scripts := make([]PluginScript, len(discovery.Plugins))
	for i, plugin := range discovery.Plugins {
		built, err := build.BuildFacet(plugin, "worker", options, background)
		if err != nil {
			t.Fatal(err)
		}
		scripts[i] = PluginScript{Plugin: plugin, Built: built}
	}
	return scripts
}

// pcRuntimeHost is a Node host loaded by the runtime itself, with the probe, and the Go host it is the parent of.
type pcRuntimeHost struct {
	runtime *PluginRuntime
	node    *NodeHost
	report  host.LoadReport
	probe   *probe
}

// pcRuntimeLoad is `runtime.load(host, { kind: "worker", core: [], scripts, modules: HOST_PACKAGES })` on a Node host with
// the probe as its one core facet.
func pcRuntimeLoad(t *testing.T, scripts []PluginScript, ops string) pcRuntimeHost {
	t.Helper()
	h, err := pcRuntimeLoadErr(t, scripts, ops)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// pcRuntimeLoadErr is pcRuntimeLoad that returns the load's error with the host that is loaded all the same: a
// *PluginError of a required root comes once the host's load settled, and the host is the caller's.
func pcRuntimeLoadErr(t *testing.T, scripts []PluginScript, ops string) (pcRuntimeHost, error) {
	t.Helper()
	cache := t.TempDir()
	runtime := NewPluginRuntime(NodeOptions{CacheDir: cache})
	address := workerKind.Address(nil)
	node, report, err := runtime.Load(RuntimeLoad{
		Kind: "worker", Core: []CoreFacet{probeCore(t, cache, pcLabelOps+ops)}, Scripts: scripts,
		Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
	}, background)
	if node == nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	parent := serverFor(t, workerRoute(node))
	return pcRuntimeHost{runtime: runtime, node: node, report: report, probe: probeOn(t, parent)}, err
}

// label is the label `test.<dir>` shows on the Node host.
func (h pcRuntimeHost) label(dir string) string {
	h.probe.t.Helper()
	text, ok := h.probe.must("label", map[string]any{"dir": dir}).(string)
	if !ok {
		h.probe.t.Fatalf("label %s is no text", dir)
	}
	return text
}

// pcFailedNames is the plugin names of failures, sorted.
func pcFailedNames(failed []host.PluginFailure) []string {
	names := make([]string, len(failed))
	for i, failure := range failed {
		names[i] = failure.Plugin
	}
	slices.Sort(names)
	return names
}

// runtime.test.ts › loading and reloading with complete sets › loads a set and leaves out a plugin whose build or
// evaluation failed, reported with the host's report.
func TestRuntimeLoadsASetAndLeavesOutAPluginWhoseBuildOrEvaluationFailedReportedWithTheHostsReport(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	pcPluginSource(t, root, "broken", "export const = ;")
	pcPluginSource(t, root, "throws", `throw new Error("at import");`)
	h := pcRuntimeLoad(t, pcShipped(t, root), "")
	if got := pcFailedNames(h.report.Failed); !slices.Equal(got, []string{"@test/broken", "@test/throws"}) {
		t.Fatalf("failed = %v", got)
	}
	if got := h.label("a"); got != "a1" {
		t.Fatalf("label a = %q, want a1", got)
	}
}

// runtime.test.ts › reloads a complete set: same digests unchanged, a changed one applied, a loaded one not shipped
// removed.
func TestRuntimeReloadsACompleteSetSameDigestsUnchangedAChangedOneAppliedALoadedOneNotShippedRemoved(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	pcItemPlugin(t, root, "b", "b1")
	pcItemPlugin(t, root, "c", "c1")
	h := pcRuntimeLoad(t, pcShipped(t, root), "")
	pcItemPlugin(t, root, "b", "b2")
	var next []PluginScript
	for _, script := range pcShipped(t, root) {
		if script.Plugin.Name != "@test/c" {
			next = append(next, script)
		}
	}
	report, err := h.runtime.Reload(h.node, next, background)
	if err != nil {
		t.Fatal(err)
	}
	want := host.PluginChanges{Added: []string{}, Changed: []string{"@test/b"}, Removed: []string{"@test/c"}}
	if !reflect.DeepEqual(report.Plugins, want) {
		t.Fatalf("plugins = %+v, want %+v", report.Plugins, want)
	}
	if got := []string{h.label("a"), h.label("b")}; !slices.Equal(got, []string{"a1", "b2"}) {
		t.Fatalf("labels = %v, want [a1 b2]", got)
	}
}

// runtime.test.ts › keeps the loaded facet of a plugin whose new build or evaluation failed; an added failing one is left
// out.
func TestRuntimeKeepsTheLoadedFacetOfAPluginWhoseNewBuildOrEvaluationFailedAnAddedFailingOneIsLeftOut(t *testing.T) {
	root := tempRoot(t)
	pcItemPlugin(t, root, "a", "a1")
	h := pcRuntimeLoad(t, pcShipped(t, root), "")
	pcPluginSource(t, root, "a", "export const = ;")
	pcPluginSource(t, root, "new", `throw new Error("at import");`)
	report, err := h.runtime.Reload(h.node, pcShipped(t, root), background)
	if err != nil {
		t.Fatal(err)
	}
	if got := pcFailedNames(report.Failed); !slices.Equal(got, []string{"@test/a", "@test/new"}) {
		t.Fatalf("failed = %v", got)
	}
	want := host.PluginChanges{Added: []string{}, Changed: []string{}, Removed: []string{}}
	if !reflect.DeepEqual(report.Plugins, want) {
		t.Fatalf("plugins = %+v, want %+v", report.Plugins, want)
	}
	if got := h.label("a"); got != "a1" {
		t.Fatalf("label a = %q, want a1", got)
	}
}

// runtime.test.ts › required roots at load (slice 12 §1) › rejects with a PluginError naming a required plugin that failed,
// after the host's load settled.
func TestRuntimeRequiredRootsAtLoadRejectsWithAPluginErrorNamingARequiredPluginThatFailedAfterTheHostsLoadSettled(t *testing.T) {
	app := tempRoot(t)
	user := tempRoot(t)
	pcItemPlugin(t, app, "core", "core")
	pcPluginSource(t, app, "broken", "export const = ;")
	pcPluginSource(t, user, "flaky", `throw new Error("at import");`)
	scripts := pcShippedRoots(t, []model.PluginRoot{{Path: app, Required: true}, {Path: user}})
	h, err := pcRuntimeLoadErr(t, scripts, "")
	failure, ok := errors.AsType[*PluginError](err)
	if !ok {
		t.Fatalf("load error = %T %v, want a *PluginError", err, err)
	}
	if !regexpMatch(`^required plugin failed at load: "@test/broken": plugin "@test/broken": its worker facet failed to build`, failure.Error()) {
		t.Fatalf("message = %q", failure.Error())
	}
	if got := pcFailedNames(failure.Required); !slices.Equal(got, []string{"@test/broken"}) {
		t.Fatalf("required = %v", got)
	}
	if got := pcFailedNames(failure.Failed); !slices.Equal(got, []string{"@test/broken", "@test/flaky"}) {
		t.Fatalf("failed = %v", got)
	}
	if len(failure.Problems) != 0 {
		t.Fatalf("problems = %+v", failure.Problems)
	}
	if got := pcFailedNames(h.report.Failed); !slices.Equal(got, []string{"@test/broken", "@test/flaky"}) {
		t.Fatalf("report failed = %v", got)
	}
	// The load settled: the required root's other plugin is loaded.
	if got := h.label("core"); got != "core" {
		t.Fatalf("label core = %q, want core", got)
	}
}

// runtime.test.ts › required roots at load (slice 12 §1) › only reports a failed plugin of a root that is not required.
func TestRuntimeRequiredRootsAtLoadOnlyReportsAFailedPluginOfARootThatIsNotRequired(t *testing.T) {
	user := tempRoot(t)
	pcPluginSource(t, user, "flaky", `throw new Error("at import");`)
	h := pcRuntimeLoad(t, pcShipped(t, user), "")
	if got := pcFailedNames(h.report.Failed); !slices.Equal(got, []string{"@test/flaky"}) {
		t.Fatalf("failed = %v", got)
	}
}

// fileURL is the file: URL of the absolute path (Node's `pathToFileURL(path).href`), the form `import()` takes on every platform.
func fileURL(path string) string {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

// runtime.test.ts › the runtime's imports › loads no esbuild: a worker that only evaluates never imports it. Pi's script,
// run on Node as Pi's test runs it, against the runtime.ts of the extracted tree (the file the subprocess runs).
func TestRuntimeLoadsNoEsbuildAWorkerThatOnlyEvaluatesNeverImportsIt(t *testing.T) {
	tree, err := extractNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Join(tree, "pi", "chord", "src", "node", "plugins", "runtime.ts")
	script := strings.Join([]string{
		`import { registerHooks } from "node:module";`,
		"registerHooks({ resolve(specifier, context, next) {",
		`  if (specifier === "esbuild") throw new Error("esbuild imported");`,
		// Pi's chord imports its pi-schema package by name; the extracted copy is TypeScript outside node_modules (host.mjs).
		fmt.Sprintf("  if (specifier === \"@earendil-works/pi-schema\") return { url: %s, shortCircuit: true };", pcJSON(t, "file://"+filepath.ToSlash(filepath.Join(tree, "pi", "schema", "src", "index.ts")))),
		"  return next(specifier, context);",
		"} });",
		fmt.Sprintf("const { createPluginRuntime } = await import(%s);", pcJSON(t, fileURL(runtime))),
		"createPluginRuntime();",
		`console.log("ok");`,
	}, "\n")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(node, "--experimental-strip-types", "--no-warnings", "--input-type=module", "--eval", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "ok" {
		t.Fatalf("output = %q, want ok", got)
	}
}

// hostErrors collects what a Node host's host reports in the background.
type hostErrors struct {
	got     chan NodeHostError
	backlog []NodeHostError
}

func newHostErrors() *hostErrors { return &hostErrors{got: make(chan NodeHostError, 64)} }

// next waits for the report of kind that arrived first and was not taken yet: the host reports in the background, so the wait
// is for the event.
func (h *hostErrors) next(t *testing.T, kind string) NodeHostError {
	t.Helper()
	for {
		for i, err := range h.backlog {
			if err.Kind == kind {
				h.backlog = append(h.backlog[:i], h.backlog[i+1:]...)
				return err
			}
		}
		select {
		case err := <-h.got:
			h.backlog = append(h.backlog, err)
		case <-time.After(30 * time.Second):
			t.Fatalf("the host reported no %s (reported: %+v)", kind, h.backlog)
		}
	}
}

// hostErrorPlugin serves the types whose failures the host reports (each with a param, so its object lives while it is
// held): `test.leaky` (two natives whose closes fail and hang), `test.boom` (a routine that throws, and a derive that throws
// once `bump` changed its document) and `test.parks` (an object that waits for a type nobody serves).
const hostErrorPlugin = `import { constant, defineFacet, defineState, int, local, method, obj, str } from "@earendil-works/chord";
const Leaky = local("test.leaky", { version: 1, params: { id: str() }, members: { n: int() } });
const Boom = local("test.boom", { version: 1, params: { id: str() }, members: { n: int(), bump: method() } });
const Parks = local("test.parks", { version: 1, params: { id: str() }, members: { n: int() } });
const Nobody = local("test.nobody", { version: 1, params: {}, members: { n: int() } });
const Count = defineState("count", { schema: obj({ n: int() }), version: 1, initial: () => ({ n: 0 }) });
export default defineFacet((host) => {
	host.serve(Leaky, (_params, self) => {
		self.open(async () => "socket", () => { throw new Error("socket close failed"); });
		self.open(async () => "worker", () => new Promise(() => {}));
		return { n: constant(1) };
	});
	host.serve(Boom, (_params, self) => {
		const doc = self.state(Count);
		self.onLive(async () => { throw new Error("routine boom"); });
		return {
			n: self.derive([doc], ([value]) => { if (value.n > 0) throw new Error("derive boom"); return value.n; }),
			bump: async (ctx) => { await doc.change((draft) => { draft.n += 1; }, ctx); return null; },
		};
	});
	host.serve(Parks, (_params, self) => {
		self.get(Nobody, {});
		return { n: constant(1) };
	});
});
`

// hostErrorOps are the probe ops that bring those objects to life and let them go (a `get` is released when the call ends).
const hostErrorOps = `leaky: async (_args, { ctx }) => {
	const Type = next.local("test.leaky", { version: 1, params: { id: next.str() }, members: { n: next.int() } });
	return (await ctx.get(Type, { id: "a" })).n.value;
},
boom: async (_args, { ctx }) => {
	const Type = next.local("test.boom", { version: 1, params: { id: next.str() }, members: { n: next.int(), bump: next.method() } });
	const ref = await ctx.get(Type, { id: "a" });
	await ref.bump(ctx);
	try { return ref.n.value; } catch (error) { return "threw " + error.message; }
},
parks: async (_args, { ctx }) => {
	const Type = next.local("test.parks", { version: 1, params: { id: next.str() }, members: { n: next.int() } });
	try { await ctx.get(Type, { id: "a" }, { readyTimeout: 1500 }); return "live"; } catch (error) { return "not live"; }
},
`

// The host's background reports arrive with every field of their kind, typed (Pi's `HostError`): a position, a member, the
// object. The host is started with a close timeout of its own (RuntimeLoad.CloseTimeout), so a hanging close is reported.
func TestRuntimeTheHostsBackgroundErrorsCarryEveryFieldOfTheirKind(t *testing.T) {
	root := tempRoot(t)
	pcPluginSource(t, root, "errors", hostErrorPlugin)
	errs := newHostErrors()
	c := pcNew(t, root, pcOptions{hostError: func(err NodeHostError) { errs.got <- err }})
	closing := 200 * time.Millisecond
	h := c.loadWorkerWith(t, hostErrorOps, nil, func(load *PluginsLoad) { load.CloseTimeout = &closing })
	h.probe.must("leaky", nil)
	failed := errs.next(t, "close_failed")
	if failed.Object != "test.leaky(a)" || failed.Native != (host.Position{Kind: host.PositionNative, Position: 1}) || failed.Err == nil || failed.Err.Error() != "socket close failed" {
		t.Fatalf("close_failed = %+v", failed)
	}
	timedOut := errs.next(t, "close_timeout")
	if timedOut.Object != "test.leaky(a)" || timedOut.Native != (host.Position{Kind: host.PositionNative, Position: 2}) {
		t.Fatalf("close_timeout = %+v", timedOut)
	}
	if got := h.probe.must("boom", nil); got != "threw derive boom" {
		t.Fatalf("boom = %v", got)
	}
	routine := errs.next(t, "routine_failed")
	if routine.Object != "test.boom(a)" || routine.Routine != (host.Position{Kind: host.PositionOnLive, Position: 1}) || routine.Err == nil || routine.Err.Error() != "routine boom" {
		t.Fatalf("routine_failed = %+v", routine)
	}
	derived := errs.next(t, "derive_failed")
	if derived.Object != "test.boom(a)" || derived.Member != "n" || derived.Err == nil || derived.Err.Error() != "derive boom" {
		t.Fatalf("derive_failed = %+v", derived)
	}
}

// The host's `readyTimeout` is the one the app gave (RuntimeLoad.ReadyTimeout): an attempt parked longer than it is reported
// as `parked_long`, with what it waits for.
func TestRuntimeTheHostsReadyTimeoutIsTheAppsAndAParkedAttemptIsReportedWithWhatItWaitsFor(t *testing.T) {
	root := tempRoot(t)
	pcPluginSource(t, root, "errors", hostErrorPlugin)
	errs := newHostErrors()
	c := pcNew(t, root, pcOptions{hostError: func(err NodeHostError) { errs.got <- err }})
	ready := 100 * time.Millisecond
	h := c.loadWorkerWith(t, hostErrorOps, nil, func(load *PluginsLoad) { load.ReadyTimeout = &ready })
	go func() { _, _ = h.probe.call("parks", nil) }()
	parked := errs.next(t, "parked_long")
	if parked.Object != "test.parks(a)" || !strings.Contains(parked.WaitingFor, "test.nobody") || (parked.State != "loading" && parked.State != "failed") || parked.Since <= 0 {
		t.Fatalf("parked_long = %+v", parked)
	}
}
