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
	discovery, err := DiscoverPlugins(roots, DiscoveryOptions{Kinds: kinds}, background)
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
			discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
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

// Ports build.test.ts "refuses a path outside the plugin, a package not in its dependencies, and a package in a copied file".
func TestEsbuildBuildRefusesAPathOutsideThePluginAPackageNotInItsDependenciesAndAPackageInACopiedFile(t *testing.T) {
	root := tempRoot(t, "contracts")
	writePlugin(t, root, "out", "@test/out", map[string]string{
		"worker.ts": "import { base } from \"../base.ts\";\nimport x from \"left-out\";\nexport default { setup: () => [base, x] };",
	}, nil)
	writeFiles(t, root, map[string]string{"base.ts": "export const base = 1;"})
	writePlugin(t, root, "copier", "@test/copier", map[string]string{
		"worker.ts": "import { y } from \"@test/shared/shared\";\nexport default { setup: () => y };",
	}, nil)
	writePlugin(t, root, "shared", "@test/shared", map[string]string{
		"shared.ts": "import y from \"some-package\";\nexport { y };",
	}, nil)
	plugins, out := esbuildDiscovered(t, "@test/out", root)
	options := esbuildNode
	options.Plugins = plugins
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, out, "worker", options)),
		`worker.ts:1:22: "../base.ts": outside the plugin (base code is reached through a host package, packages through dependencies)`,
		`worker.ts:2:15: "left-out": neither a host package, in the dependencies of plugin "@test/out" nor a discovered plugin "left-out"`,
	)
	_, copier := esbuildDiscovered(t, "@test/copier", root)
	esbuildMatches(t, esbuildErrors(t, esbuildBuild(t, copier, "worker", options)),
		`../shared/shared.ts:1:15: "some-package": a file copied from plugin "@test/shared" may import only host packages and files of its own plugin`,
	)
}

// Ports build.test.ts "imports another plugin's file by name from any root; refuses relative paths into it, names a missing plugin".
func TestEsbuildBuildImportsAnotherPluginsFileByNameFromAnyRootRefusesRelativePathsIntoItNamesAMissingPlugin(t *testing.T) {
	first := tempRoot(t)
	second := tempRoot(t)
	writePlugin(t, second, "elsewhere", "@test/contracts", map[string]string{
		"contract.ts": `export const name = "contract";`,
		"other.ts":    "import { name } from \"./contract.ts\";\nexport const other = name;",
	}, nil)
	writePlugin(t, first, "user", "@test/user", map[string]string{
		"worker.ts": strings.Join([]string{
			`import { name } from "@test/contracts/contract";`,
			`import { other } from "@test/contracts/other";`,
			"export default { setup: () => [name, other] };",
		}, "\n"),
	}, nil)
	writePlugin(t, first, "relative", "@test/relative", map[string]string{
		"worker.ts": "import { name } from \"../user/worker.ts\";\nexport default { setup: () => name };",
	}, nil)
	writePlugin(t, first, "missing", "@test/missing", map[string]string{
		"worker.ts": "import { name } from \"@test/absent/contract\";\nexport default { setup: () => name };",
	}, nil)
	discovery, err := DiscoverPlugins([]string{first, second}, DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	options := esbuildNode
	options.Plugins = discovery.Plugins
	build := func(name string) BuiltFacet {
		for _, plugin := range discovery.Plugins {
			if plugin.Name == name {
				return esbuildBuild(t, plugin, "worker", options)
			}
		}
		t.Fatalf("no plugin %q", name)
		return BuiltFacet{}
	}
	user := esbuildScript(t, build("@test/user"))
	var copied []string
	for _, file := range user.Copied {
		relative, err := filepath.Rel(realOf(t, second), file)
		if err != nil {
			t.Fatal(err)
		}
		copied = append(copied, filepath.ToSlash(relative))
	}
	slices.Sort(copied)
	if want := []string{"elsewhere/contract.ts", "elsewhere/other.ts"}; !reflect.DeepEqual(copied, want) {
		t.Errorf("copied = %q, want %q", copied, want)
	}
	esbuildMatches(t, esbuildErrors(t, build("@test/relative")),
		`worker.ts:1:22: "../user/worker.ts": a relative path into plugin "@test/user" (import its files by name: "@test/user/…")`,
	)
	esbuildMatches(t, esbuildErrors(t, build("@test/missing")),
		`worker.ts:1:22: "@test/absent/contract": neither a host package, in the dependencies of plugin "@test/missing" nor a discovered plugin "@test/absent"`,
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
// lengths below are what Pi's own `buildFacet` printed for the same plugins (test/next-node/fixtures/plugins, Pi 2c8d1feb,
// built with `hostPackages: ["@earendil-works/chord/next"]`, `platform: "node"`).
func TestBuildMakesTheSameScriptsAsPisBuildFacetForTheFixtures(t *testing.T) {
	root := tempRoot(t, "contracts", "notes", "files")
	discovery, err := DiscoverPlugins([]string{root}, DiscoveryOptions{Kinds: kinds}, background)
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
