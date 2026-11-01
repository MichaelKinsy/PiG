package plugins

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/next-node/plugins/runtime.test.ts. Tests of the plugin runtime (slice 10 §3, D11): loading and
// reloading hosts with complete sets of shipped scripts (unchanged digests kept as the same facet, removed = loaded minus
// shipped, failed builds and evaluations kept old), and that importing it loads no esbuild. The runtime runs, as Pi's own
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
var pcHostPackages = []string{"@earendil-works/chord/next"}

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
		`import { constant, defineFacet, local, str } from "@earendil-works/chord/next";`,
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
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: []string{"worker"}}, background)
	if err != nil {
		t.Fatal(err)
	}
	options := BuildOptions{HostPackages: pcHostPackages, Platform: PlatformNode, Plugins: discovery.Plugins}
	scripts := make([]PluginScript, len(discovery.Plugins))
	for i, plugin := range discovery.Plugins {
		built, err := BuildFacet(plugin, "worker", options, background)
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
	cache := t.TempDir()
	runtime := NewPluginRuntime(NodeOptions{CacheDir: cache})
	parent := goHost(t)
	if _, err := parent.Load(nil, background); err != nil {
		t.Fatal(err)
	}
	address := workerKind.Address(nil)
	node, report, err := runtime.Load(RuntimeLoad{
		Kind: "worker", Core: []CoreFacet{probeCore(t, cache, pcLabelOps+ops)}, Scripts: scripts,
		Parent: parent, Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	return pcRuntimeHost{runtime: runtime, node: node, report: report, probe: probeOn(t, parent)}
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

// runtime.test.ts › the runtime's imports › loads no esbuild: a worker that only evaluates never imports it. Pi's script,
// run on Node as Pi's test runs it, against the runtime.ts of the extracted tree (the file the subprocess runs).
func TestRuntimeLoadsNoEsbuildAWorkerThatOnlyEvaluatesNeverImportsIt(t *testing.T) {
	tree, err := extractNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Join(tree, "pi", "chord", "src", "next-node", "plugins", "runtime.ts")
	script := strings.Join([]string{
		`import { registerHooks } from "node:module";`,
		"registerHooks({ resolve(specifier, context, next) {",
		`  if (specifier === "esbuild") throw new Error("esbuild imported");`,
		"  return next(specifier, context);",
		"} });",
		fmt.Sprintf("const { createPluginRuntime } = await import(%s);", pcJSON(t, runtime)),
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
