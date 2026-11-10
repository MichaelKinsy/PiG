package plugins

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Intention checks for packages/chord/test/node/workers/base.test.ts. Pi's worker base bundles a Node entry for a forked
// worker with esbuild; a PiG worker is a Go executable (language mechanics: it has no JavaScript bundle), so no Go test can run that bundle. What each case protects is
// tested where PiG has the same kind of code: the plugin scripts a worker runs are built by BuildFacet (the port of Pi's
// plugin build, build.ts) and evaluated by the Node host, and the surfaces' bases (chord/nextnode/surfaces) are the bundles
// read with the server's `--conditions`. Each test says which case it answers and what the PiG form of it is.

// workerBaseOps is the ops of the lazy-import case.
const workerBaseOps = evaluateOpsPrelude + `
	lazy: async (a) => {
		let reads = 0;
		const hostPackages = {};
		Object.defineProperty(hostPackages, "lazy-pkg", { enumerable: true, get() { reads += 1; return { where: "the host" }; } });
		const facet = ops.run(a, hostPackages);
		const atEvaluation = reads;
		const loaded = await facet.later("lazy-pkg");
		return { atEvaluation, afterTheCall: reads, where: loaded.where ?? loaded.default?.where };
	},
`

// Intention of base.test.ts "the snapshot › splits at a dynamic import: the module and the package it imports load only when
// imported, from the snapshot": a lazily imported module, and what it imports, cost nothing until the code asks for them, and
// what they give is the snapshot's, not the disk's at that time. In a plugin script a lazy import is a computed `import(x)`,
// which BuildFacet turns into a call of the facet's `require`: the Node host answers it from the host packages the build kept
// external, and nothing reads them while the facet is evaluated. A literal `import("./lazy.ts")` is refused at the build (a file
// of a plugin is read through pluginFile), so no lazily loaded file of a plugin can differ from its build.
func TestWorkerBaseALazyImportOfAPluginScriptReadsNothingUntilTheCodeAsksAndALiteralOneIsRefused(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "lazy", "@test/lazy", map[string]string{
		"worker.ts":    "export default { setup: () => {}, later: (name: string) => import(name) };",
		"server.ts":    `export default { setup: () => import("./lazy-file.ts") };`,
		"lazy-file.ts": `export const lazy = "v1";`,
	}, nil)
	script := evaluateBuilt(t, root, "@test/lazy", "worker", []string{"lazy-pkg"})
	got := asMap(t, evaluateProbe(t, workerBaseOps).must("lazy", evaluateArgs(t, script, "none")))
	if got["atEvaluation"] != float64(0) || got["afterTheCall"] != float64(1) || got["where"] != "the host" {
		t.Errorf("host package read %v times at evaluation and %v after the call, answered %v; want 0, 1, the host", got["atEvaluation"], got["afterTheCall"], got["where"])
	}
	_, plugin := esbuildDiscovered(t, "@test/lazy", root)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "server", esbuildNode)),
		`re:^server\.ts:1:\d+: import\("\./lazy-file\.ts"\): a plugin script has no dynamic import .*pluginFile`)
}

// Intention of base.test.ts "module locations and stack traces › import.meta and __dirname name each source file, a local
// __dirname stays the module's own": a bundle of many files must not give every file the bundle's location, and a module's own
// `__dirname` binding is not replaced. A plugin script has no module location at all (its files are one script), so BuildFacet
// refuses `import.meta` and a free `__dirname` or `__filename` in each file, naming that file and line (the PiG form of "name
// each source file"), and leaves a local `const __dirname` alone: it is built, and the script evaluates to the module's own value.
func TestWorkerBaseModuleLocationsAreRefusedInEachFileNamingItAndALocalDirnameStaysTheModulesOwn(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "where", "@test/where", map[string]string{
		"worker.ts":    "import { here } from \"./sub/here.ts\";\nexport default { setup: () => here };",
		"web.ts":       "import { there } from \"./sub/there.ts\";\nexport default { setup: () => there };",
		"server.ts":    "import { local } from \"./sub/local.ts\";\nexport default { setup: () => local };",
		"sub/here.ts":  "export const here = { meta: import.meta.dirname };",
		"sub/there.ts": "export const there = { file: __filename };",
		"sub/local.ts": "const __dirname = \"the module's own\";\nexport const local = { dirname: __dirname };",
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/where", root)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "worker", esbuildNode)),
		`re:^sub/here\.ts:1:\d+: import\.meta\.url, __dirname and __filename .*pluginFile`)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "web", esbuildNode)),
		`re:^sub/there\.ts:1:\d+: import\.meta\.url, __dirname and __filename .*pluginFile`)
	local := esbuildScript(t, esbuildBuild(t, plugin, "server", esbuildNode))
	if !strings.Contains(local.Source, "the module's own") {
		t.Fatalf("a local __dirname was replaced: %s", local.Source)
	}
	// The files the build read are recorded, as for every other build.
	var inputs []string
	for _, input := range local.Inputs {
		inputs = append(inputs, input.File)
	}
	slices.Sort(inputs)
	dir := realOf(t, filepath.Join(root, "where"))
	if want := []string{filepath.Join(dir, "server.ts"), filepath.Join(dir, "sub", "local.ts")}; !reflect.DeepEqual(inputs, want) {
		t.Fatalf("inputs = %q, want %q", inputs, want)
	}
	if got := runBuiltScript(t, local.Source, "JSON.stringify(module.exports.default.setup())"); got != `{"dirname":"the module's own"}` {
		t.Errorf("the script evaluates to %s", got)
	}
}

// runBuiltScript evaluates a built facet script on Node as vm.Script does, with a `require` that answers nothing, and returns
// what expression (over `module`) prints.
func runBuiltScript(t *testing.T, source, expression string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	driver := `const vm = require("node:vm");
const fn = new vm.Script(require("node:fs").readFileSync(0, "utf8")).runInThisContext();
const module = { exports: {} };
fn(() => ({}), module, module.exports);
console.log(` + expression + `);`
	command := exec.Command(node, "-e", driver)
	command.Stdin = strings.NewReader(source)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// Intention of base.test.ts "module locations and stack traces › maps stack traces to the source files and lines with
// --enable-source-maps, in the entry and in a chunk": an error thrown in bundled code names the files and lines of the sources,
// in the entry and in a file the entry reaches through a second file (Pi's chunk). Node's `--enable-source-maps` is Pi's
// mechanism; PiG's is mapStack in the Node host, which reads each script's inline source map (BuildFacet's), so the same
// throw is read through the probe: the file and line of the throw, of the file that called it, and of the entry.
func TestWorkerBaseAStackThroughTheEntryAndTwoFilesNamesEachSourceFileAndLine(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "chain", "@test/chain", map[string]string{
		"worker.ts": "import { failLater } from \"./lazy.ts\";\nexport default { setup: () => {}, fail: () => failLater() };",
		"lazy.ts":   "import { fail } from \"./fail.ts\";\n\nexport function failLater(): void {\n\tfail();\n}",
		"fail.ts":   "const at: string = \"chain\";\n\nexport function fail(): void {\n\tthrow new Error(at);\n}",
	}, nil)
	script := evaluateBuilt(t, root, "@test/chain", "worker", nil)
	got := asMap(t, evaluateProbe(t, stackOps).must("mapped", evaluateArgs(t, script, "none")))
	mapped, _ := got["mapped"].(string)
	for _, want := range []string{`chain/fail\.ts:4:`, `chain/lazy\.ts:4:`, `chain/worker\.ts:2:`} {
		if !regexp.MustCompile(want).MatchString(mapped) {
			t.Errorf("mapped stack lacks %s:\n%s", want, mapped)
		}
	}
}

// walkNames is every path under dir, relative to it.
func walkNames(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err == nil {
			names = append(names, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	return names
}

// Intention of base.test.ts "a Node entry's inputs (nodeBaseInputs) › records the entry and what it imports with the given
// conditions, installed packages left out, nothing written": the record of what a build read is the entry and the files of
// the app it imports, each with the bytes' hash, never an installed package, and the build writes nothing. BuildFacet's Inputs
// are that record for a facet; its inputs are the facet's file and the plugin files it imports, a package it bundles is left out,
// and the plugin's directory is as it was. (The conditions case is TestConditionsResolves... in chord/nextnode/surfaces.)
func TestWorkerBaseAFacetsInputsAreItsFileAndThePluginFilesItImportsNoPackageNothingWritten(t *testing.T) {
	root := tempRoot(t)
	dir := writePlugin(t, root, "graph", "@test/graph", map[string]string{
		"worker.ts":   "import { where } from \"installed\";\nimport { more } from \"./sub/more.ts\";\nexport default { setup: () => [where, more] };",
		"sub/more.ts": "export const more = \"more\";",
	}, map[string]any{"dependencies": map[string]string{"installed": "1.0.0"}})
	writeNpmPackage(t, dir, "installed", "index.js", `exports.where = "installed";`)
	before := walkNames(t, dir)
	_, plugin := esbuildDiscovered(t, "@test/graph", root)
	built := esbuildScript(t, esbuildBuild(t, plugin, "worker", esbuildNode))
	var inputs []string
	for _, input := range built.Inputs {
		inputs = append(inputs, input.File)
		bytes, err := os.ReadFile(input.File)
		if err != nil {
			t.Fatal(err)
		}
		if input.Hash != model.ContentHash(bytes) {
			t.Errorf("%s: hash %s is not the bytes' (%s)", input.File, input.Hash, model.ContentHash(bytes))
		}
	}
	slices.Sort(inputs)
	real := realOf(t, dir)
	if want := []string{filepath.Join(real, "sub", "more.ts"), filepath.Join(real, "worker.ts")}; !reflect.DeepEqual(inputs, want) {
		t.Fatalf("inputs = %q, want %q", inputs, want)
	}
	if !strings.Contains(built.Source, `"installed"`) {
		t.Errorf("the installed package is not in the script: %s", built.Source)
	}
	if after := walkNames(t, dir); !reflect.DeepEqual(after, before) {
		t.Errorf("the build wrote: before %q, after %q", before, after)
	}
}

// Intention of base.test.ts "a Node entry's inputs (nodeBaseInputs) › scans the static graph best effort: an import that does
// not resolve is left out; no tsconfig paths, as Node": the graph is read as Node resolves it, so a `tsconfig.json`'s `paths` do
// not redirect an import. For a facet's build: BuildFacet ignores a tsconfig (TsconfigRaw "{}"), so `pkg` is the installed package
// and `alias.ts` is no input. Pi's scan leaves an import that does not resolve out because it only records what a server compares;
// a facet's build is the script a plugin runs, so an import that does not resolve fails it, as a failed result (never a thrown
// error) naming the file, line and column, with no script and so no inputs.
func TestWorkerBaseABuildReadsNoTsconfigPathsAndAnImportThatDoesNotResolveIsAFailedResultNamingIt(t *testing.T) {
	root := tempRoot(t)
	dir := writePlugin(t, root, "static", "@test/static", map[string]string{
		"worker.ts":     "import { where } from \"pkg\";\nexport default { setup: () => where };",
		"alias.ts":      "export const where = \"alias\";",
		"tsconfig.json": `{"compilerOptions":{"baseUrl":".","paths":{"pkg":["./alias.ts"]}}}`,
	}, map[string]any{"dependencies": map[string]string{"pkg": "1.0.0"}})
	writeNpmPackage(t, dir, "pkg", "index.js", `exports.where = "installed";`)
	_, plugin := esbuildDiscovered(t, "@test/static", root)
	built := esbuildScript(t, esbuildBuild(t, plugin, "worker", esbuildNode))
	if !strings.Contains(built.Source, `"installed"`) || strings.Contains(built.Source, `"alias"`) {
		t.Errorf("a tsconfig path redirected the import: %s", built.Source)
	}
	var inputs []string
	for _, input := range built.Inputs {
		inputs = append(inputs, filepath.Base(input.File))
	}
	if !slices.Equal(inputs, []string{"worker.ts"}) {
		t.Errorf("inputs = %q, want only worker.ts", inputs)
	}
	writeFiles(t, dir, map[string]string{"worker.ts": "import { where } from \"pkg\";\ntry {\n\trequire(\"./missing.cjs\");\n} catch {}\nexport default { setup: () => where };"})
	failed := esbuildBuild(t, plugin, "worker", esbuildNode)
	esbuildMatches(t, esbuildErrors(t, failed), `worker.ts:3:10: Could not resolve "./missing.cjs"`)
	if failed.Source != "" || len(failed.Inputs) != 0 {
		t.Errorf("a failed build has a script or inputs: %+v", failed)
	}
}

// Intention of base.test.ts "the snapshot › runs every imported module, as Node does: sideEffects: false drops no side-effect
// import". Pi's worker base sets esbuild's ignoreAnnotations so a bundle runs what Node would run. A worker's plugin scripts are
// built by Pi's build.ts, which does not set it, and BuildFacet is its port: there is no PiG bundle that sets it, so the case's
// own assertion has no PiG subject. What is testable, and what keeps PiG from drifting from the build Pi runs, is the behaviour
// itself against Pi: the scripts below are byte-for-byte (digest and length) what Pi's own `buildFacet` (build.ts of Pi
// c23b9ec1, Node 24, esbuild 0.28.2) made of the same two plugins. An installed package that says `sideEffects: false` has its
// bare import dropped (the plugin never runs its effect), and the plugin's own package saying it drops nothing of the plugin's
// own files. A Go process links every package and runs each init by the language, so no Go form of the worker base can differ.
func TestWorkerBaseASideEffectsFalsePackageIsDroppedByAPluginBuildAsPisBuildFacetDoesTheOwnPackageNothing(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "own", "@test/se", map[string]string{
		"worker.ts": "import \"./effect.ts\";\nexport default { setup: () => \"entry\" };",
		"effect.ts": `console.log("effect");`,
	}, map[string]any{"sideEffects": false})
	installed := writePlugin(t, root, "installed", "@test/se2", map[string]string{
		"worker.ts": "import \"fx\";\nexport default { setup: () => \"entry\" };",
	}, map[string]any{"dependencies": map[string]string{"fx": "1.0.0"}})
	esbuildPackage(t, installed, "fx", "index.js", map[string]string{"index.js": `console.log("effect");`}, map[string]any{"sideEffects": false})
	plugins, own := esbuildDiscovered(t, "@test/se", root)
	built := esbuildScript(t, esbuildBuild(t, own, "worker", esbuildNodeKinds(plugins)))
	if !strings.Contains(built.Source, `console.log("effect")`) {
		t.Errorf("the plugin's own file lost its effect: %s", built.Source)
	}
	if want := "e6d0081e5808b48188c753f1e53f2bde0fb6bda4183d66f1c2377a1fe2079482"; built.Digest != want || len(built.Source) != 988 {
		t.Errorf("own package: digest %s length %d, Pi's %s 988", built.Digest, len(built.Source), want)
	}
	_, other := esbuildDiscovered(t, "@test/se2", root)
	built = esbuildScript(t, esbuildBuild(t, other, "worker", esbuildNodeKinds(plugins)))
	if strings.Contains(built.Source, `console.log("effect")`) {
		t.Errorf("the package that says sideEffects: false kept its effect: %s", built.Source)
	}
	if want := "c9c53fe5ccde9fc9abdbb8d17549189f6e6fbab43eae45bf768019bef1ffdeb0"; built.Digest != want || len(built.Source) != 922 {
		t.Errorf("installed package: digest %s length %d, Pi's %s 922", built.Digest, len(built.Source), want)
	}
}
