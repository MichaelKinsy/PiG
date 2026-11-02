package plugins

// pi: packages/chord/src/next-node/plugins/build.ts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports packages/chord/test/next-node/plugins/build.test.ts, which runs the real esbuild build, against BuildFacet on
// esbuild's Go API: every row of the import table, failures as results, source maps relative to the plugin and digests
// stable across two checkout paths, inputs hashed as esbuild read them.

var (
	esbuildNode    = BuildOptions{HostPackages: []string{"@earendil-works/chord/next"}, Platform: PlatformNode}
	esbuildBrowser = BuildOptions{
		HostPackages: []string{"@earendil-works/chord/next", "react/jsx-runtime"},
		Platform:     PlatformBrowser,
		PublicPath:   new("/assets/"),
	}
)

// esbuildDiscovered is the plugins under roots and the one named name (test's `discovered`).
func esbuildDiscovered(t *testing.T, name string, roots ...string) ([]Plugin, Plugin) {
	t.Helper()
	discovery, err := DiscoverPlugins(plainRoots(roots...), DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	for _, plugin := range discovery.Plugins {
		if plugin.Name == name {
			return discovery.Plugins, plugin
		}
	}
	t.Fatalf("no plugin %q in %+v", name, discovery)
	return nil, Plugin{}
}

// esbuildBuild is BuildFacet's result, which must not be an error.
func esbuildBuild(t *testing.T, plugin Plugin, kind string, options BuildOptions) BuiltFacet {
	t.Helper()
	built, err := BuildFacet(plugin, kind, options, background)
	if err != nil {
		t.Fatal(err)
	}
	return built
}

// esbuildScript is the script of a build that must have made one (test's `script`: fails with the build's errors).
func esbuildScript(t *testing.T, built BuiltFacet) BuiltFacet {
	t.Helper()
	if built.Kind == BuiltFacetFailed {
		t.Fatalf("build failed: %s", strings.Join(built.Errors, "\n"))
	}
	if built.Kind != BuiltFacetScript {
		t.Fatalf("build is %+v", built)
	}
	return built
}

// esbuildErrors is the errors of a build that must fail (test's `errors`).
func esbuildErrors(t *testing.T, built BuiltFacet) []string {
	t.Helper()
	if built.Kind != BuiltFacetFailed {
		t.Fatal("the build succeeded")
	}
	return built.Errors
}

// esbuildMap is the parsed inline source map of a built script.
func esbuildMap(t *testing.T, built BuiltFacet) map[string]any {
	t.Helper()
	match := regexp.MustCompile(`sourceMappingURL=data:application/json;base64,(\S+)`).FindStringSubmatch(built.Source)
	if match == nil {
		t.Fatal("no inline source map")
	}
	text, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(text, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// esbuildSourcesOf is the sources of a built script's inline source map.
func esbuildSourcesOf(t *testing.T, built BuiltFacet) []string {
	t.Helper()
	var sources []string
	for _, each := range esbuildMap(t, built)["sources"].([]any) {
		sources = append(sources, each.(string))
	}
	return sources
}

// esbuildMatches checks each of found against the pattern at the same index (vitest's `expect.stringMatching`), or the
// string itself when the pattern is not a regular expression (marked by a leading "re:").
func esbuildMatches(t *testing.T, found []string, want ...string) {
	t.Helper()
	if len(found) != len(want) {
		t.Fatalf("errors = %q, want %d of them like %q", found, len(want), want)
	}
	for i, pattern := range want {
		if expression, ok := strings.CutPrefix(pattern, "re:"); ok {
			if !regexp.MustCompile(expression).MatchString(found[i]) {
				t.Errorf("error %d = %q, want it to match %s", i, found[i], expression)
			}
		} else if found[i] != pattern {
			t.Errorf("error %d = %q, want %q", i, found[i], pattern)
		}
	}
}

func esbuildSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeNpmPackage writes an npm package name into dir's node_modules: its package.json (CommonJS, main file) and file
// (test support's `writePackage`, for the one file every package of these tests has).
func writeNpmPackage(t *testing.T, dir, name, file, text string) {
	t.Helper()
	json, err := json.Marshal(map[string]any{"name": name, "version": "1.0.0", "main": file})
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, filepath.Join(dir, "node_modules", name), map[string]string{"package.json": string(json), file: text})
}

// Ports build.test.ts "inlines the plugin's own files and another plugin's file (copied); keeps host packages external".
func TestEsbuildBuildInlinesOwnFilesAndAnotherPluginsFileKeepsHostPackagesExternal(t *testing.T) {
	root := tempRoot(t, "contracts", "notes")
	plugins, plugin := esbuildDiscovered(t, "@test/notes", root)
	options := esbuildNode
	options.Plugins = plugins
	built := esbuildScript(t, esbuildBuild(t, plugin, "worker", options))
	real := realOf(t, root)
	if built.Plugin != "@test/notes" || built.Facet != "worker" || len(built.Assets) != 0 {
		t.Fatalf("built = %s %s with %d assets", built.Plugin, built.Facet, len(built.Assets))
	}
	if !strings.HasPrefix(built.Source, "(function (require, module, exports) {") {
		t.Errorf("source starts %q", built.Source[:60])
	}
	if !strings.Contains(built.Source, `require("@earendil-works/chord/next")`) {
		t.Error("the host package is not required")
	}
	if want := []string{filepath.Join(real, "contracts", "contract.ts")}; !reflect.DeepEqual(built.Copied, want) {
		t.Errorf("copied = %q, want %q", built.Copied, want)
	}
	var inputs []string
	for _, input := range built.Inputs {
		inputs = append(inputs, input.File)
	}
	if want := []string{filepath.Join(real, "contracts", "contract.ts"), filepath.Join(real, "notes", "label.ts"), filepath.Join(real, "notes", "worker.ts")}; !reflect.DeepEqual(inputs, want) {
		t.Errorf("inputs = %q, want %q", inputs, want)
	}
	// Relative to the plugin (a file whose code minified away maps nothing and is left out).
	sources := esbuildSourcesOf(t, built)
	for _, want := range []string{"worker.ts", "../contracts/contract.ts"} {
		if !slices.Contains(sources, want) {
			t.Errorf("sources %q lack %q", sources, want)
		}
	}
	for _, source := range sources {
		if strings.HasPrefix(source, "/") {
			t.Errorf("source %q is absolute", source)
		}
	}
	// Positions only: no copy of the sources' text in the script (Opus N3).
	if _, has := esbuildMap(t, built)["sourcesContent"]; has {
		t.Error("the source map has sourcesContent")
	}
}

// Ports build.test.ts "hashes each input as esbuild read it; the digest is the source's hash".
func TestEsbuildBuildHashesEachInputAsEsbuildReadItTheDigestIsTheSourcesHash(t *testing.T) {
	root := tempRoot(t, "contracts", "notes")
	plugins, plugin := esbuildDiscovered(t, "@test/notes", root)
	options := esbuildNode
	options.Plugins = plugins
	built := esbuildScript(t, esbuildBuild(t, plugin, "worker", options))
	if len(built.Inputs) == 0 {
		t.Fatal("no inputs")
	}
	for _, input := range built.Inputs {
		data, err := os.ReadFile(input.File)
		if err != nil {
			t.Fatal(err)
		}
		if input.Hash != esbuildSHA256(data) {
			t.Errorf("%s: hash %s, want %s", input.File, input.Hash, esbuildSHA256(data))
		}
	}
	if built.Digest != esbuildSHA256([]byte(built.Source)) {
		t.Errorf("digest = %s, want the hash of the source", built.Digest)
	}
}

// Ports build.test.ts "gives the same script and digest from two checkout paths (relative source maps)".
func TestEsbuildBuildGivesTheSameScriptAndDigestFromTwoCheckoutPaths(t *testing.T) {
	roots := []string{tempRoot(t, "contracts", "notes"), tempRoot(t, "contracts", "notes")}
	builds := make([]BuiltFacet, len(roots))
	errs := make([]error, len(roots))
	var wait sync.WaitGroup
	for i, root := range roots {
		wait.Go(func() {
			discovery, err := DiscoverPlugins(plainRoots(root), DiscoveryOptions{Kinds: kinds}, background)
			if err != nil {
				errs[i] = err
				return
			}
			options := esbuildNode
			options.Plugins = discovery.Plugins
			for _, plugin := range discovery.Plugins {
				if plugin.Name == "@test/notes" {
					builds[i], errs[i] = BuildFacet(plugin, "worker", options, background)
				}
			}
		})
	}
	wait.Wait()
	for i := range roots {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		esbuildScript(t, builds[i])
	}
	if builds[0].Digest != builds[1].Digest {
		t.Errorf("digests differ: %s, %s", builds[0].Digest, builds[1].Digest)
	}
	if builds[0].Source != builds[1].Source {
		t.Error("sources differ")
	}
}

// Ports build.test.ts "inlines a package of its dependencies; keeps chord.external packages and Node builtins external on Node".
func TestEsbuildBuildInlinesADependencyKeepsExternalPackagesAndNodeBuiltinsExternalOnNode(t *testing.T) {
	root := tempRoot(t)
	dir := writePlugin(t, root, "deps", "@test/deps", map[string]string{
		"worker.ts": strings.Join([]string{
			`import { pad } from "pad-dep";`,
			`import native from "native-dep";`,
			`import { join } from "node:path";`,
			`import { readFileSync } from "fs";`,
			`import { pluginFile } from "@earendil-works/chord/next/plugins";`,
			"export default { setup: () => [pad, native, join, readFileSync, pluginFile] };",
		}, "\n"),
	}, map[string]any{"dependencies": map[string]any{"pad-dep": "1.0.0"}, "chord": map[string]any{"external": []string{"native-dep"}}})
	writeNpmPackage(t, dir, "pad-dep", "index.js", `exports.pad = (s) => "pad:" + s;`)
	writeNpmPackage(t, dir, "native-dep", "index.js", "module.exports = 1;")
	_, plugin := esbuildDiscovered(t, "@test/deps", root)
	built := esbuildScript(t, esbuildBuild(t, plugin, "worker", esbuildNode))
	if !strings.Contains(built.Source, `"pad:"`) {
		t.Error("the dependency is not inlined")
	}
	for _, external := range []string{"native-dep", "node:path", "fs", "@earendil-works/chord/next/plugins"} {
		if !strings.Contains(built.Source, `require("`+external+`")`) {
			t.Errorf("%s is not required", external)
		}
	}
	// Packages are no inputs: the watcher ignores node_modules.
	var inputs []string
	for _, input := range built.Inputs {
		inputs = append(inputs, input.File)
	}
	if want := []string{filepath.Join(realOf(t, dir), "worker.ts")}; !reflect.DeepEqual(inputs, want) {
		t.Errorf("inputs = %q, want %q", inputs, want)
	}
}

// Ports build.test.ts "refuses a chord.external package and a Node builtin in a browser build".
func TestEsbuildBuildRefusesAChordExternalPackageAndANodeBuiltinInABrowserBuild(t *testing.T) {
	root := tempRoot(t)
	dir := writePlugin(t, root, "web", "@test/web", map[string]string{
		"web.ts": "import native from \"native-dep\";\nimport { join } from \"node:path\";\nexport default { setup: () => [native, join] };",
	}, map[string]any{"chord": map[string]any{"external": []string{"native-dep"}}})
	writeNpmPackage(t, dir, "native-dep", "index.js", "module.exports = 1;")
	_, plugin := esbuildDiscovered(t, "@test/web", root)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "web", esbuildBrowser)),
		`web.ts:1:20: "native-dep": chord.external packages are for Node builds`,
		`web.ts:2:22: "node:path": a Node builtin in a browser build`,
	)
}

// Ports build.test.ts "collects a browser build's CSS into one stylesheet and its assets into content-hashed files".
func TestEsbuildBuildCollectsABrowserBuildsCSSIntoOneStylesheetAndItsAssetsIntoContentHashedFiles(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "web", "@test/web", map[string]string{
		"web.tsx": strings.Join([]string{
			`import "./notes.css";`,
			`import iconUrl from "./assets/icon.svg?url";`,
			"const Icon = () => <img src={iconUrl} />;",
			"export default { setup: () => Icon };",
		}, "\n"),
		"notes.css":       ".notes { color: red; }",
		"assets/icon.svg": "<svg></svg>",
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/web", root)
	built := esbuildScript(t, esbuildBuild(t, plugin, "web", esbuildBrowser))
	if built.CSS == nil || !regexp.MustCompile(`^web-[A-Z0-9]+\.css$`).MatchString(built.CSS.Name) {
		t.Fatalf("css = %+v", built.CSS)
	}
	if !strings.Contains(built.CSS.Text, ".notes{color:red}") {
		t.Errorf("css text = %q", built.CSS.Text)
	}
	if len(built.Assets) != 1 || !regexp.MustCompile(`^icon-[A-Z0-9]+\.svg$`).MatchString(built.Assets[0].Name) {
		t.Fatalf("assets = %+v", built.Assets)
	}
	if !bytes.Equal(built.Assets[0].Bytes, []byte("<svg></svg>")) {
		t.Errorf("asset bytes = %q", built.Assets[0].Bytes)
	}
	if !strings.Contains(built.Source, `"/assets/`+built.Assets[0].Name+`"`) {
		t.Errorf("the asset's public path is not in the source")
	}
	if !strings.Contains(built.Source, `require("react/jsx-runtime")`) {
		t.Error("the JSX runtime is not required")
	}
}

// Ports build.test.ts "refuses CSS and assets in a Node build".
func TestEsbuildBuildRefusesCSSAndAssetsInANodeBuild(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "n", "@test/n", map[string]string{
		"worker.ts": "import \"./a.css\";\nimport url from \"./icon.svg?url\";\nexport default { setup: () => url };",
		"a.css":     "a{}",
		"icon.svg":  "<svg/>",
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/n", root)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "worker", esbuildNode)),
		`worker.ts:1:8: "./a.css": stylesheets and assets are for browser builds`,
		`re:^worker\.ts:2:17: "\./icon\.svg\?url": assets by URL are for browser builds`,
	)
}

// Ports build.test.ts "refuses import.meta.url, __dirname, __filename, a dynamic import() and top-level await, pointing to pluginFile".
func TestEsbuildBuildRefusesImportMetaDirnameFilenameADynamicImportAndTopLevelAwaitPointingToPluginFile(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "bad", "@test/bad", map[string]string{
		"worker.ts": "export const a = import.meta.url;\nexport default { setup: () => [__dirname, __filename] };",
		"server.ts": `export default { setup: () => import("./other.ts") };`,
		"web.ts":    "await Promise.resolve();\nexport default { setup: () => {} };",
		"other.ts":  "export const x = 1;",
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/bad", root)
	meta := esbuildErrors(t, esbuildBuild(t, plugin, "worker", esbuildNode))
	if len(meta) == 0 {
		t.Fatal("no errors")
	}
	for _, found := range meta {
		if !regexp.MustCompile(`^worker\.ts:\d+:\d+: import\.meta\.url, __dirname and __filename .*pluginFile`).MatchString(found) {
			t.Errorf("error %q", found)
		}
	}
	// Without `import.meta` in the file: each free `__dirname` and `__filename` at its own line (example 37).
	writeFiles(t, filepath.Join(root, "bad"), map[string]string{
		"worker.ts": "// The globals.\nconsole.log(__dirname);\nexport default { setup: () => __filename };",
	})
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "worker", esbuildNode)),
		`re:^worker\.ts:2:13: import\.meta\.url, __dirname and __filename .*pluginFile`,
		`re:^worker\.ts:3:\d+: import\.meta\.url, __dirname and __filename .*pluginFile`,
	)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "server", esbuildNode)),
		`re:^server\.ts:1:\d+: import\("\./other\.ts"\): a plugin script has no dynamic import .*pluginFile`,
	)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "web", esbuildNode)),
		"web.ts:1:1: a plugin script has no top-level await (a facet declares synchronously)",
	)
}

// Ports build.test.ts "refuses a path leaving the root, a file in no plugin, a package not in its dependencies, a package in a copied file".
func TestEsbuildBuildRefusesAPathLeavingTheRootAFileInNoPluginAPackageNotInItsDependenciesAPackageInACopiedFile(t *testing.T) {
	root := tempRoot(t, "contracts")
	away := tempRoot(t)
	writeFiles(t, away, map[string]string{"far.ts": "export const far = 1;"})
	writePlugin(t, root, "out", "@test/out", map[string]string{
		"worker.ts": strings.Join([]string{
			`import { base } from "../base.ts";`,
			"import { far } from " + esbuildJSON(t, filepath.Join(away, "far.ts")) + ";",
			`import x from "left-out";`,
			"export default { setup: () => [base, far, x] };",
		}, "\n"),
	}, nil)
	writeFiles(t, root, map[string]string{"base.ts": "export const base = 1;"})
	writePlugin(t, root, "copier", "@test/copier", map[string]string{
		"worker.ts": "import { y } from \"../shared/shared.ts\";\nexport default { setup: () => y };",
	}, nil)
	writePlugin(t, root, "shared", "@test/shared", map[string]string{
		"shared.ts": "import y from \"some-package\";\nexport { y };",
	}, nil)
	plugins, out := esbuildDiscovered(t, "@test/out", root)
	options := esbuildNode
	options.Plugins = plugins
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, out, "worker", options)),
		`worker.ts:1:22: "../base.ts": not a file of a plugin in this root`,
		`re:^worker\.ts:2:21: ".*far\.ts": outside the plugin's root \(base code is reached through a host package, other repositories' plugins through dependencies\)$`,
		`worker.ts:3:15: "left-out": neither a host package nor in the dependencies of plugin "@test/out"`,
	)
	_, copier := esbuildDiscovered(t, "@test/copier", root)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, copier, "worker", options)),
		`../shared/shared.ts:1:15: "some-package": a file copied from plugin "@test/shared" may import only host packages and files of its own plugin`,
	)
}

// Ports build.test.ts "inlines another plugin's files of the same root by relative path; refuses one of another root and names by plugin name".
func TestEsbuildBuildInlinesAnotherPluginsFilesOfTheSameRootByRelativePathRefusesOneOfAnotherRootAndNamesByPluginName(t *testing.T) {
	first := tempRoot(t)
	second := tempRoot(t)
	writePlugin(t, first, "contracts", "@test/contracts", map[string]string{
		"contract.ts": `export const name = "contract";`,
		"other.ts":    "import { name } from \"./contract.ts\";\nexport const other = name;",
	}, nil)
	writePlugin(t, second, "elsewhere", "@test/elsewhere", map[string]string{"contract.ts": `export const name = "far";`}, nil)
	writePlugin(t, first, "user", "@test/user", map[string]string{
		"worker.ts": strings.Join([]string{
			`import { name } from "../contracts/contract.ts";`,
			`import { other } from "../contracts/other.ts";`,
			"export default { setup: () => [name, other] };",
		}, "\n"),
	}, nil)
	writePlugin(t, first, "far", "@test/far", map[string]string{
		"worker.ts": "import { name } from " + esbuildJSON(t, filepath.Join(second, "elsewhere", "contract.ts")) + ";\nexport default { setup: () => name };",
	}, nil)
	writePlugin(t, first, "named", "@test/named", map[string]string{
		"worker.ts": "import { name } from \"@test/contracts/contract\";\nexport default { setup: () => name };",
	}, nil)
	discovery, err := DiscoverPlugins(plainRoots(first, second), DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	options := esbuildNode
	options.Plugins = discovery.Plugins
	build := esbuildBuilder(t, discovery.Plugins, "worker", options)
	real := realOf(t, first)
	if got, want := esbuildScript(t, build("@test/user")).Copied, []string{filepath.Join(real, "contracts", "contract.ts"), filepath.Join(real, "contracts", "other.ts")}; !reflect.DeepEqual(got, want) {
		t.Errorf("copied = %q, want %q", got, want)
	}
	esbuildMatches(t, esbuildErrors(t, build("@test/far")),
		`re:^worker\.ts:1:22: ".*": outside the plugin's root \(base code is reached through a host package`,
	)
	esbuildMatches(t, esbuildErrors(t, build("@test/named")),
		`worker.ts:1:22: "@test/contracts/contract": neither a host package nor in the dependencies of plugin "@test/named"`,
	)
}

// Ports build.test.ts "returns a syntax error and a missing facet as failed results, never a throw".
func TestEsbuildBuildReturnsASyntaxErrorAndAMissingFacetAsFailedResultsNeverAThrow(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "syntax", "@test/syntax", map[string]string{"worker.ts": "export default {"}, nil)
	_, plugin := esbuildDiscovered(t, "@test/syntax", root)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "worker", esbuildNode)), `re:^worker\.ts:1:\d+: Expected `)
	want := BuiltFacet{Kind: BuiltFacetFailed, Plugin: "@test/syntax", Facet: "server", Errors: []string{`plugin "@test/syntax" has no server facet`}}
	if built := esbuildBuild(t, plugin, "server", esbuildNode); !reflect.DeepEqual(built, want) {
		t.Errorf("built = %#v, want %#v", built, want)
	}
}

// An input that cannot be read is a build error whose text is Node's (Pi's build reads it with `readFile`): the code, what it
// means, and the path. (Pi's text has the location of esbuild's own library in front of it, an artifact of its JavaScript API.)
func TestEsbuildBuildAnInputThatCannotBeReadIsNodesErrorText(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone.ts")
	_, err := os.ReadFile(missing)
	if got, want := nodeReadError(err, missing), "ENOENT: no such file or directory, open '"+missing+"'"; got != want {
		t.Errorf("a missing input: %q, want %q", got, want)
	}
	if got, want := nodeReadError(&fs.PathError{Op: "open", Path: missing, Err: fs.ErrPermission}, missing), "EACCES: permission denied, open '"+missing+"'"; got != want {
		t.Errorf("a forbidden input: %q, want %q", got, want)
	}
	if got, want := nodeReadError(errors.New("another failure"), missing), "another failure"; got != want {
		t.Errorf("another failure: %q, want %q", got, want)
	}
}

// Ports buildFacet's `context.abortSignal?.throwIfAborted()`: a build whose context was aborted before it returns the
// abort's cause and builds nothing.
func TestEsbuildBuildAnAbortedContextIsItsCause(t *testing.T) {
	root := tempRoot(t, "contracts", "notes")
	plugins, plugin := esbuildDiscovered(t, "@test/notes", root)
	options := esbuildNode
	options.Plugins = plugins
	ctx, cancel := context.WithCancelCause(chordctx.Background())
	cause := os.ErrDeadlineExceeded
	cancel(cause)
	built, err := BuildFacet(plugin, "worker", options, ctx)
	if !errors.Is(err, cause) || !reflect.DeepEqual(built, BuiltFacet{}) {
		t.Fatalf("built = %+v, err = %v, want the cause %v", built, err, cause)
	}
}

// A computed `import(x)` becomes a call of the script's `require` (`supported: { "dynamic-import": false }`): the script,
// evaluated as `vm.Script` does with a `require` that answers, resolves it through that `require`.
func TestEsbuildBuildAComputedImportRunsThroughRequire(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "computed", "@test/computed", map[string]string{
		"worker.ts": "export default { setup: (name: string) => import(name) };",
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/computed", root)
	built := esbuildScript(t, esbuildBuild(t, plugin, "worker", esbuildNode))
	if strings.Contains(built.Source, "import(") || !strings.Contains(built.Source, "require(") {
		t.Fatalf("the computed import is not a require: %s", built.Source)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	driver := `const vm = require("node:vm");
const fn = new vm.Script(require("node:fs").readFileSync(0, "utf8")).runInThisContext();
const module = { exports: {} };
fn((specifier) => ({ answered: specifier }), module, module.exports);
module.exports.default.setup("some-package").then((value) => console.log(JSON.stringify(value.answered)));`
	command := exec.Command(node, "-e", driver)
	command.Stdin = strings.NewReader(built.Source)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != `"some-package"` {
		t.Errorf("the require answered %q", out)
	}
}

// A build error's column is esbuild's own, in bytes (the JavaScript API `build.ts` reads reports the same number: `location.column`
// is "0-based, in bytes"), and a free `__dirname`'s is where the script's source map puts it, in UTF-16 code units, after
// non-ASCII text on their line.
func TestEsbuildBuildColumnsAreBytesForErrorsAndUTF16CodeUnitsForGlobals(t *testing.T) {
	root := tempRoot(t)
	prefix := `const s = "éé😀"; `
	writePlugin(t, root, "wide", "@test/wide", map[string]string{
		"worker.ts": prefix + "export default {",
		"server.ts": "console.log(\"é😀\", __dirname);\nexport default { setup: () => 1 };",
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/wide", root)
	got := esbuildErrors(t, esbuildBuild(t, plugin, "worker", esbuildNode))
	if column := len(prefix+"export default {") + 1; utf16Length(prefix+"export default {")+1 == column || len(got) != 1 || !strings.HasPrefix(got[0], "worker.ts:1:"+strconv.Itoa(column)+": Expected ") {
		t.Errorf("syntax error = %q, want it at byte column %d", got, column)
	}
	got = esbuildErrors(t, esbuildBuild(t, plugin, "server", esbuildNode))
	want := "server.ts:1:" + strconv.Itoa(utf16Length(`console.log("é😀", `)+1) + ": " + noModuleIdentity
	if len(got) != 1 || got[0] != want {
		t.Errorf("__dirname = %q, want %q", got, want)
	}
}

// Not upstream's test: the digest is "the same on every machine", and Pi's JavaScript API runs the same esbuild (0.28.2) as
// the Go API, so the scripts Pi's `buildFacet` makes of the fixture plugins and these are the same bytes: the digests and
// lengths below are what Pi's own `buildFacet` (build.ts of Pi 9823079ad, run in Node 24 on esbuild 0.28.2) printed for the
// fixture plugins as they are at that commit (test/next-node/fixtures/plugins, the contract imported by relative path
// `../contracts/contract.ts`), built with `hostPackages: ["@earendil-works/chord/next"]`, `platform: "node"`.
func TestBuildMakesTheSameScriptsAsPisBuildFacetForTheFixtures(t *testing.T) {
	root := tempRoot(t, "contracts", "notes", "files")
	discovery, err := DiscoverPlugins(plainRoots(root), DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	type key struct{ plugin, kind string }
	want := map[key]struct {
		digest string
		length int
	}{
		{"@test/files", "worker"}: {"a6470a1c2ed9ae1beff006680ae77656532e4b8f222f669f04baaa434caa25a1", 2486},
		{"@test/notes", "server"}: {"b419d92e93a0f2b95d5613c62b6830f160e5282d52e26575180b72d1998d0158", 2217},
		{"@test/notes", "worker"}: {"9ce9029f0cfc4a5459b16bf3abf1af2406a82b1b87c1ddd983c35e8dc58b841b", 3043},
	}
	got := 0
	for _, plugin := range discovery.Plugins {
		for kind := range plugin.Facets {
			built, err := BuildFacet(plugin, kind, BuildOptions{HostPackages: []string{"@earendil-works/chord/next"}, Platform: PlatformNode, Plugins: discovery.Plugins}, background)
			if err != nil || built.Kind != BuiltFacetScript {
				t.Fatalf("%s %s: %+v, %v", plugin.Name, kind, built, err)
			}
			expected, ok := want[key{plugin.Name, kind}]
			if !ok {
				t.Fatalf("unexpected facet %s %s", plugin.Name, kind)
			}
			if built.Digest != expected.digest || len(built.Source) != expected.length {
				t.Errorf("%s %s: digest %s length %d, Pi's %s %d", plugin.Name, kind, built.Digest, len(built.Source), expected.digest, expected.length)
			}
			got++
		}
	}
	if got != len(want) {
		t.Fatalf("built %d facets, want %d", got, len(want))
	}
}

// esbuildAPI is the app's API of slice 12's tests: two versions, a shared module and per-kind modules.
var esbuildAPI = AppApi{Name: "@acme/app", Versions: []string{"v3", "v2"}}

// esbuildAPIKind is each kind's host packages with the API: v3's shared and kind modules, and v2 (test's `apiKind`).
func esbuildAPIKind(kind string) BuildOptions {
	platform := PlatformNode
	if kind == "web" {
		platform = PlatformBrowser
	}
	return BuildOptions{
		HostPackages: []string{"@acme/app/v3", "@acme/app/v3/" + kind, "@acme/app/v2"},
		Platform:     platform,
		Api:          &esbuildAPI,
		Kinds:        kinds,
	}
}

// esbuildNodeKinds is NODE with the app's kinds and the plugins (test's `{ ...NODE, kinds: KINDS, plugins }`).
func esbuildNodeKinds(plugins []Plugin) BuildOptions {
	options := esbuildNode
	options.Kinds = kinds
	options.Plugins = plugins
	return options
}

// esbuildJSON is JSON.stringify of a string.
func esbuildJSON(t *testing.T, value string) string {
	t.Helper()
	text, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// esbuildPackage writes an npm package name into dir's node_modules (test support's `writePackage`): its package.json (CommonJS
// unless manifest says `type`, with main as its entry: upstream takes the first of the files) and files, which may replace
// the package.json.
func esbuildPackage(t *testing.T, dir, name, main string, files map[string]string, manifest map[string]any) {
	t.Helper()
	fields := map[string]any{"name": name, "version": "1.0.0", "main": main}
	maps.Copy(fields, manifest)
	text, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]string{"package.json": string(text)}
	maps.Copy(all, files)
	writeFiles(t, filepath.Join(dir, "node_modules", name), all)
}

// esbuildBuilder builds the facet of kind of the plugin of the given name among plugins.
func esbuildBuilder(t *testing.T, plugins []Plugin, kind string, options BuildOptions) func(name string) BuiltFacet {
	t.Helper()
	return func(name string) BuiltFacet {
		t.Helper()
		for _, plugin := range plugins {
			if plugin.Name == name {
				return esbuildBuild(t, plugin, kind, options)
			}
		}
		t.Fatalf("no plugin %q", name)
		return BuiltFacet{}
	}
}

// esbuildInputFiles is the files of a build's inputs.
func esbuildInputFiles(built BuiltFacet) []string {
	files := []string{}
	for _, input := range built.Inputs {
		files = append(files, input.File)
	}
	return files
}

// Ports build.test.ts "keeps a listed version and the kind's module external, and the plugins component's pluginFile".
func TestEsbuildBuildKeepsAListedVersionAndTheKindsModuleExternalAndThePluginsComponentsPluginFile(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "good", "@test/good", map[string]string{
		"worker.ts": strings.Join([]string{
			`import { defineFacet } from "@acme/app/v3";`,
			`import { Tools } from "@acme/app/v3/worker";`,
			`import { pluginFile } from "@earendil-works/chord/next/plugins";`,
			"export default defineFacet(() => [Tools, pluginFile]);",
		}, "\n"),
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/good", root)
	built := esbuildScript(t, esbuildBuild(t, plugin, "worker", esbuildAPIKind("worker")))
	if !strings.Contains(built.Source, `require("@acme/app/v3/worker")`) {
		t.Errorf("the kind's module is not required: %s", built.Source)
	}
	if want := (&BuiltApi{Name: "@acme/app", Version: "v3"}); !reflect.DeepEqual(built.Api, want) {
		t.Errorf("api = %+v, want %+v", built.Api, want)
	}
}

// Ports build.test.ts "refuses an unlisted version, no version, another kind's module and Chord itself".
func TestEsbuildBuildRefusesAnUnlistedVersionNoVersionAnotherKindsModuleAndChordItself(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "bad", "@test/bad", map[string]string{
		"worker.ts": strings.Join([]string{
			`import { a } from "@acme/app/v1";`,
			`import { b } from "@acme/app";`,
			`import { c } from "@acme/app/v3/web";`,
			`import { d } from "@earendil-works/chord/next";`,
			"export default { setup: () => [a, b, c, d] };",
		}, "\n"),
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/bad", root)
	// Chord is refused even when the app lists it as a host package.
	options := esbuildAPIKind("worker")
	options.HostPackages = append(slices.Clone(options.HostPackages), "@earendil-works/chord/next")
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "worker", options)),
		`worker.ts:1:19: "@acme/app/v1": @acme/app/v1 is not available: this app provides v3, v2`,
		`worker.ts:2:19: "@acme/app": import a version of the app's API (this app provides v3, v2)`,
		`worker.ts:3:19: "@acme/app/v3/web": not available to worker facets`,
		`worker.ts:4:19: "@earendil-works/chord/next": plugins import Chord through the app's API ("@acme/app/v3"), never directly`,
	)
}

// Ports build.test.ts "refuses two versions in one facet, naming both with their importers".
func TestEsbuildBuildRefusesTwoVersionsInOneFacetNamingBothWithTheirImporters(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "mixed", "@test/mixed", map[string]string{
		"worker.ts": "import { a } from \"@acme/app/v3\";\nimport { b } from \"./old.ts\";\nexport default { setup: () => [a, b] };",
		"old.ts":    `export { b } from "@acme/app/v2";`,
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/mixed", root)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "worker", esbuildAPIKind("worker"))),
		"one facet imports one version of @acme/app: this one imports @acme/app/v2 (old.ts) and @acme/app/v3 (worker.ts)",
	)
}

// Ports build.test.ts "names the app's API in the refusal of a path leaving the root".
func TestEsbuildBuildNamesTheAppsAPIInTheRefusalOfAPathLeavingTheRoot(t *testing.T) {
	root := tempRoot(t)
	away := tempRoot(t)
	writeFiles(t, away, map[string]string{"base.ts": "export const base = 1;"})
	writePlugin(t, root, "out", "@test/out", map[string]string{
		"worker.ts": "import { base } from " + esbuildJSON(t, filepath.Join(away, "base.ts")) + ";\nexport default { setup: () => base };",
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/out", root)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "worker", esbuildAPIKind("worker"))),
		`re:base code is reached through the app's API \("@acme/app/v3"\)`,
	)
}

// Ports build.test.ts "allows a file of the facet's own kind; refuses one of another kind, naming the chain that reached it".
func TestEsbuildBuildAllowsAFileOfTheFacetsOwnKindRefusesOneOfAnotherKindNamingTheChainThatReachedIt(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "notes", "@test/notes", map[string]string{
		"contract.ts":        `export const shared = "shared";`,
		"contract.worker.ts": `export const tools = "tools";`,
		"contract.web.tsx":   `export const panel = "panel";`,
		"view.ts":            `export { panel } from "./contract.web.tsx";`,
		"worker.ts": strings.Join([]string{
			`import { shared } from "./contract.ts";`,
			`import { tools } from "./contract.worker.ts";`,
			"export default { setup: () => [shared, tools] };",
		}, "\n"),
		"server.ts": "import { panel } from \"./view.ts\";\nexport default { setup: () => panel };",
	}, nil)
	plugins, plugin := esbuildDiscovered(t, "@test/notes", root)
	esbuildScript(t, esbuildBuild(t, plugin, "worker", esbuildNodeKinds(plugins)))
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "server", esbuildNodeKinds(plugins))),
		`view.ts:1:23: "./contract.web.tsx": a web file, imported into a server facet (server.ts → view.ts → contract.web.tsx)`,
	)
}

// Ports build.test.ts "checks another plugin's copied files; never a package's file names (pdfjs's pdf.worker.mjs)".
func TestEsbuildBuildChecksAnotherPluginsCopiedFilesNeverAPackagesFileNames(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "notes", "@test/notes", map[string]string{"contract.web.tsx": `export const panel = "panel";`}, nil)
	user := writePlugin(t, root, "user", "@test/user", map[string]string{
		"server.ts": "import { panel } from \"../notes/contract.web.tsx\";\nexport default { setup: () => panel };",
		"worker.ts": "import { list } from \"@bob/notes/web\";\nimport pdf from \"pdfjs-like\";\nexport default { setup: () => [list, pdf] };",
	}, map[string]any{"dependencies": map[string]any{"@bob/notes": "1.0.0", "pdfjs-like": "1.0.0"}})
	esbuildPackage(t, user, "@bob/notes", "contract.ts", map[string]string{
		"contract.ts":      "export const list = 1;",
		"contract.web.tsx": "export const list = 2;",
	}, map[string]any{"exports": map[string]any{".": "./contract.ts", "./web": "./contract.web.tsx"}})
	esbuildPackage(t, user, "pdfjs-like", "index.mjs", map[string]string{
		"index.mjs":            `export { default } from "./build/pdf.worker.mjs";`,
		"build/pdf.worker.mjs": "export default 1;",
	}, map[string]any{"type": "module", "main": "index.mjs"})
	plugins, plugin := esbuildDiscovered(t, "@test/user", root)
	options := esbuildNodeKinds(plugins)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "server", options)),
		`server.ts:1:23: "../notes/contract.web.tsx": a web file, imported into a server facet (server.ts → ../notes/contract.web.tsx)`,
	)
	esbuildScript(t, esbuildBuild(t, plugin, "worker", options))
}

// Ports build.test.ts "refuses another kind's facet file and a per-kind name of any case (Opus L1, M2)".
func TestEsbuildBuildRefusesAnotherKindsFacetFileAndAPerKindNameOfAnyCase(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "f", "@test/f", map[string]string{
		"worker.ts":       "import { a } from \"./web.tsx\";\nimport { b } from \"./contract.Web.ts\";\nexport default { setup: () => [a, b] };",
		"web.tsx":         "export const a = 1;",
		"contract.Web.ts": "export const b = 2;",
	}, nil)
	plugins, plugin := esbuildDiscovered(t, "@test/f", root)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "worker", esbuildNodeKinds(plugins))),
		`worker.ts:1:19: "./web.tsx": a web file, imported into a worker facet (worker.ts → web.tsx)`,
		`worker.ts:2:19: "./contract.Web.ts": a web file, imported into a worker facet (worker.ts → contract.Web.ts)`,
	)
}

// Ports build.test.ts "names the chain of an import cycle without hanging (Sol)".
func TestEsbuildBuildNamesTheChainOfAnImportCycleWithoutHanging(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "cycle", "@test/cycle", map[string]string{
		"worker.ts":  "import { value } from \"./a.ts\";\nexport default { setup: () => value };",
		"a.ts":       "import \"./worker.ts\";\nexport { value } from \"./bridge.ts\";",
		"bridge.ts":  `export { value } from "./bad.web.ts";`,
		"bad.web.ts": "export const value = 1;",
	}, nil)
	plugins, plugin := esbuildDiscovered(t, "@test/cycle", root)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "worker", esbuildNodeKinds(plugins))),
		`bridge.ts:1:23: "./bad.web.ts": a web file, imported into a worker facet (worker.ts → a.ts → bridge.ts → bad.web.ts)`,
	)
}

// Ports build.test.ts "builds a plugin of a root that lies under a node_modules (an app installed from npm with its plugins)".
func TestEsbuildBuildBuildsAPluginOfARootThatLiesUnderANodeModules(t *testing.T) {
	app := tempRoot(t)
	root := filepath.Join(app, "node_modules", "@acme", "app", "plugins")
	writePlugin(t, root, "notes", "@acme/notes", map[string]string{
		"worker.ts": "import { x } from \"./helper.ts\";\nimport { c } from \"../contracts/contract.ts\";\nexport default { setup: () => [x, c] };",
		"helper.ts": "export const x = 1;",
	}, nil)
	writePlugin(t, root, "contracts", "@acme/contracts", map[string]string{"contract.ts": "export const c = 1;"}, nil)
	plugins, plugin := esbuildDiscovered(t, "@acme/notes", root)
	options := esbuildNode
	options.Plugins = plugins
	if copied, want := esbuildScript(t, esbuildBuild(t, plugin, "worker", options)).Copied, []string{filepath.Join(realOf(t, root), "contracts", "contract.ts")}; !reflect.DeepEqual(copied, want) {
		t.Errorf("copied = %q, want %q", copied, want)
	}
}

// Ports build.test.ts "checks per-kind files in a package that is plugin code (a chord field), not in a library".
func TestEsbuildBuildChecksPerKindFilesInAPackageThatIsPluginCodeNotInALibrary(t *testing.T) {
	root := tempRoot(t)
	user := writePlugin(t, root, "user", "@test/user", map[string]string{
		"worker.ts": "import { panel } from \"@bob/plugins/notes/contract.web.tsx\";\nexport default { setup: () => panel };",
		"server.ts": "import { a } from \"@bob/plugins/notes/web.tsx\";\nexport default { setup: () => a };",
	}, map[string]any{"dependencies": map[string]any{"@bob/plugins": "1.0.0"}})
	writeFiles(t, filepath.Join(user, "node_modules", "@bob", "plugins"), map[string]string{
		"package.json":           `{"name":"@bob/plugins","chord":{"plugins":["notes"]}}`,
		"notes/package.json":     `{"name":"@bob/notes"}`,
		"notes/contract.web.tsx": `export const panel = "panel";`,
		"notes/web.tsx":          "export const a = 1;",
	})
	plugins, plugin := esbuildDiscovered(t, "@test/user", root)
	options := esbuildNodeKinds(plugins)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "worker", options)),
		`worker.ts:1:23: "@bob/plugins/notes/contract.web.tsx": a web file, imported into a worker facet (worker.ts → node_modules/@bob/plugins/notes/contract.web.tsx)`,
	)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "server", options)),
		`server.ts:1:19: "@bob/plugins/notes/web.tsx": a web file, imported into a server facet (server.ts → node_modules/@bob/plugins/notes/web.tsx)`,
	)
}

// Ports build.test.ts "leaves a library unchecked by kind, also one nested in a plugin package's own node_modules".
func TestEsbuildBuildLeavesALibraryUncheckedByKindAlsoOneNestedInAPluginPackagesOwnNodeModules(t *testing.T) {
	root := tempRoot(t)
	user := writePlugin(t, root, "p", "@test/p", map[string]string{
		"server.ts": "import x from \"@bob/plugins/notes/contract.ts\";\nexport default { setup: () => x };",
	}, map[string]any{"dependencies": map[string]any{"@bob/plugins": "1.0.0"}})
	writeFiles(t, filepath.Join(user, "node_modules", "@bob", "plugins"), map[string]string{
		"package.json":       `{"name":"@bob/plugins","chord":{"plugins":["./notes"]}}`,
		"notes/package.json": `{"name":"@bob/notes","dependencies":{"pdfjs-like":"1.0.0"}}`,
		"notes/contract.ts":  "import pdf from \"pdfjs-like\";\nexport default pdf;",
		"notes/node_modules/pdfjs-like/package.json":         `{"name":"pdfjs-like","type":"module","main":"index.mjs"}`,
		"notes/node_modules/pdfjs-like/index.mjs":            `export { default } from "./build/pdf.worker.mjs";`,
		"notes/node_modules/pdfjs-like/build/pdf.worker.mjs": "export default 1;",
	})
	plugins, plugin := esbuildDiscovered(t, "@test/p", root)
	esbuildScript(t, esbuildBuild(t, plugin, "server", esbuildNodeKinds(plugins)))
}

// Ports build.test.ts "refuses a package file whose real path is outside the plugin's node_modules".
func TestEsbuildBuildRefusesAPackageFileWhoseRealPathIsOutsideThePluginsNodeModules(t *testing.T) {
	app := tempRoot(t)
	writeFiles(t, app, map[string]string{
		"src/core.ts":      "export const SECRET = 'base code';",
		"src/package.json": `{ "name": "base", "main": "core.ts" }`,
	})
	root := filepath.Join(app, "plugins")
	a := writePlugin(t, root, "a", "@test/a", map[string]string{
		"worker.ts": "import { SECRET } from \"base\";\nexport default { setup: () => SECRET };",
	}, map[string]any{"dependencies": map[string]any{"base": "file:../../src"}})
	if err := os.MkdirAll(filepath.Join(a, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	// npm's `file:` dependency: a link out of node_modules.
	testenv.RequireDirectoryLink(t, filepath.Join(app, "src"), filepath.Join(a, "node_modules", "base"))
	b := writePlugin(t, root, "b", "@test/b", map[string]string{
		"worker.ts": "import { S } from \"dep\";\nexport default { setup: () => S };",
	}, map[string]any{"dependencies": map[string]any{"dep": "1.0.0"}})
	esbuildPackage(t, b, "dep", "index.js", map[string]string{"index.js": `export { SECRET as S } from "../../../../src/core.ts";`}, nil)
	c := writePlugin(t, root, "c", "@test/c", map[string]string{
		"worker.ts": "import { S } from \"dep\";\nexport default { setup: () => S };",
	}, map[string]any{"dependencies": map[string]any{"dep": "1.0.0"}})
	esbuildPackage(t, c, "dep", "package.json", map[string]string{"package.json": `{ "name": "dep", "main": "index.js" }`}, nil)
	// A link inside the package pointing out of it.
	testenv.RequireSymlink(t, filepath.Join(app, "src", "core.ts"), filepath.Join(c, "node_modules", "dep", "index.js"))
	discovery, err := DiscoverPlugins(plainRoots(root), DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	options := esbuildNode
	options.Plugins = discovery.Plugins
	build := esbuildBuilder(t, discovery.Plugins, "worker", options)
	outside := `re:: resolves outside the plugin's node_modules \(.*src/core\.ts\)$`
	esbuildMatches(t, esbuildErrors(t, build("@test/a")), outside)
	esbuildMatches(t, esbuildErrors(t, build("@test/b")), outside)
	esbuildMatches(t, esbuildErrors(t, build("@test/c")), outside)
}

// Ports build.test.ts "refuses a facet file linked out of its plugin, or to a file of another kind (Sol)".
func TestEsbuildBuildRefusesAFacetFileLinkedOutOfItsPluginOrToAFileOfAnotherKind(t *testing.T) {
	root := tempRoot(t)
	away := tempRoot(t)
	writeFiles(t, away, map[string]string{"facet.ts": "export default { setup: () => {} };"})
	out := writePlugin(t, root, "out", "@test/out", map[string]string{}, nil)
	testenv.RequireSymlink(t, filepath.Join(away, "facet.ts"), filepath.Join(out, "worker.ts"))
	kind := writePlugin(t, root, "kind", "@test/kind", map[string]string{"web.tsx": "export default { setup: () => {} };"}, nil)
	testenv.RequireSymlink(t, filepath.Join(kind, "web.tsx"), filepath.Join(kind, "worker.ts"))
	discovery, err := DiscoverPlugins(plainRoots(root), DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	build := esbuildBuilder(t, discovery.Plugins, "worker", esbuildNodeKinds(discovery.Plugins))
	esbuildMatches(t, esbuildErrors(t, build("@test/out")), `re:^worker\.ts: the facet file links out of its plugin`)
	esbuildMatches(t, esbuildErrors(t, build("@test/kind")), "worker.ts: links to a web file (web.tsx), not a worker facet")
}

// Ports build.test.ts "refuses a Node builtin in a copied file (Sol)".
func TestEsbuildBuildRefusesANodeBuiltinInACopiedFile(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "other", "@test/other", map[string]string{
		"contract.ts": "import { basename } from \"node:path\";\nexport const value = basename(\"x/y\");",
	}, nil)
	writePlugin(t, root, "user", "@test/user", map[string]string{
		"worker.ts": "import { value } from \"../other/contract.ts\";\nexport default { setup: () => value };",
	}, nil)
	plugins, plugin := esbuildDiscovered(t, "@test/user", root)
	options := esbuildNode
	options.Plugins = plugins
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, plugin, "worker", options)),
		`../other/contract.ts:1:26: "node:path": a file copied from plugin "@test/other" may import only host packages and files of its own plugin`,
	)
}

// Ports build.test.ts "inlines a dependency's shared and per-kind contract files as its exports map them".
func TestEsbuildBuildInlinesADependencysSharedAndPerKindContractFilesAsItsExportsMapThem(t *testing.T) {
	root := tempRoot(t)
	dir := writePlugin(t, root, "stats", "@test/stats", map[string]string{
		"worker.ts": "import { Notes } from \"@bob/notes\";\nexport default { setup: () => Notes };",
		"web.tsx":   "import { Panel } from \"@bob/notes/web\";\nexport default { setup: () => Panel };",
	}, map[string]any{"dependencies": map[string]any{"@bob/notes": "1.0.0"}})
	esbuildPackage(t, dir, "@bob/notes", "contract.ts", map[string]string{
		"contract.ts":      `export const Notes = "notes v1";`,
		"contract.web.tsx": `export const Panel = "panel v1";`,
	}, map[string]any{"exports": map[string]any{".": "./contract.ts", "./web": "./contract.web.tsx"}, "type": "module"})
	_, plugin := esbuildDiscovered(t, "@test/stats", root)
	nodeOptions := esbuildNode
	nodeOptions.Kinds = kinds
	worker := esbuildScript(t, esbuildBuild(t, plugin, "worker", nodeOptions))
	if !strings.Contains(worker.Source, "notes v1") {
		t.Errorf("the dependency's shared file is not inlined: %s", worker.Source)
	}
	browserOptions := esbuildBrowser
	browserOptions.Kinds = kinds
	web := esbuildScript(t, esbuildBuild(t, plugin, "web", browserOptions))
	if !strings.Contains(web.Source, "panel v1") {
		t.Errorf("the dependency's web file is not inlined: %s", web.Source)
	}
	// Packages are not the watcher's inputs.
	if got, want := esbuildInputFiles(worker), []string{filepath.Join(realOf(t, dir), "worker.ts")}; !reflect.DeepEqual(got, want) {
		t.Errorf("inputs = %q, want %q", got, want)
	}
}

// Ports build.test.ts "allows host packages only, refusing other files, packages and builtins with a pointer to a directory".
func TestEsbuildBuildAllowsHostPackagesOnlyRefusingOtherFilesPackagesAndBuiltinsWithAPointerToADirectory(t *testing.T) {
	root := tempRoot(t)
	writeFiles(t, root, map[string]string{
		"hello.server.ts": "import { defineFacet } from \"@earendil-works/chord/next\";\nexport default defineFacet(() => {});",
		"bad.server.ts": strings.Join([]string{
			`import { a } from "./hello.server.ts";`,
			`import b from "some-package";`,
			`import { join } from "node:path";`,
			"export default { setup: () => [a, b, join] };",
		}, "\n"),
	})
	discovery, err := DiscoverPlugins(plainRoots(root), DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	options := esbuildNode
	options.Plugins = discovery.Plugins
	build := esbuildBuilder(t, discovery.Plugins, "server", options)
	if got, want := esbuildSourcesOf(t, esbuildScript(t, build("hello"))), []string{"hello.server.ts"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sources = %q, want %q", got, want)
	}
	hint := `a single-file plugin imports only host packages (the app's API); make it a directory "bad/" with a package.json`
	esbuildMatches(t, esbuildErrors(t, build("bad")),
		`bad.server.ts:1:19: "./hello.server.ts": `+hint,
		`bad.server.ts:2:15: "some-package": `+hint,
		`bad.server.ts:3:22: "node:path": `+hint,
	)
}
