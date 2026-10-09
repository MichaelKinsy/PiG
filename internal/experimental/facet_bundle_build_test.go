package experimental

// pi: packages/chord/src/bundler.ts

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// upstream: packages/chord/src/node/bundle.ts:163-181 and package.ts:158-209. Record validation precedes the locale-sorted compiler loop; a Go map must not select the first diagnostic.
func TestBundleFacetsPreservesRecordValidationOrder(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name    string
		options BundleFacetsOptions
		want    string
	}{
		{"plugin before entries", BundleFacetsOptions{}, "Facet bundle plugin ID must not be empty"},
		{"empty explicit version", BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "p", Version: new("")}}, "Facet bundle plugin version must not be empty"},
		{"empty entries", BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "p"}}, "Facet bundle must contain at least one entry"},
		{"insertion before sorting", BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "p"}, Entries: []FacetEntrySource{{"z", ""}, {"a", ""}}}, "Facet bundle entry z must have a source path"},
		{"integer before insertion", BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "p"}, Entries: []FacetEntrySource{{"z", ""}, {"10", ""}, {"2", ""}}}, "Facet bundle entry 2 must have a source path"},
		{"overwrite first position", BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "p"}, Entries: []FacetEntrySource{{"z", ""}, {"a", ""}, {"z", "fixed.ts"}}}, "Facet bundle entry a must have a source path"},
		{"empty entry name", BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "p"}, Entries: []FacetEntrySource{{"", "x.ts"}}}, "Facet bundle entry name must not be empty"},
		{"external after entries", BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "p"}, Entries: []FacetEntrySource{{"a", "x.ts"}}, External: []string{""}}, "Facet bundle external import must not be empty"},
	} {
		t.Run(row.name, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "not-created")
			row.options.Outdir = filepath.Join(parent, "bundle")
			_, err := BundleFacets(t.Context(), row.options)
			if err == nil || err.Error() != row.want {
				t.Fatalf("error=%v, want %q", err, row.want)
			}
			if _, err := os.Stat(parent); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("validation created output parent: %v", err)
			}
		})
	}
	got, err := normalizeFacetSources([]FacetEntrySource{{"z", "old"}, {"10", "ten"}, {"2", "two"}, {"a", "a"}, {"z", "new"}, {"01", "not-index"}})
	want := []FacetEntrySource{{"2", "two"}, {"10", "ten"}, {"z", "new"}, {"a", "a"}, {"01", "not-index"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized=%v, %v; want %v", got, err, want)
	}
}

// upstream: packages/chord/src/node/bundle.ts:40-80,87-111. This drives the shared compiler, not a synthesized manifest or a JavaScript evaluator.
func TestBundleFacetsOwnsOutputPathsVersionAndReplacement(t *testing.T) {
	isolateExperimentalTest(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "entry.ts"), []byte(`export default { id: "selected", setup() {} };`), 0o600); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	options := BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "direct"}, Entries: []FacetEntrySource{{Name: "opaque/worker", Source: "entry.ts"}}, Outdir: "bundle", WorkingDirectory: &root}
	built, err := BundleFacets(cancelled, options)
	if err != nil {
		t.Fatal(err)
	}
	if built.ManifestPath != filepath.Join(root, "bundle", FacetBundleManifestFile) || built.Manifest.Plugin.Version != nil {
		t.Fatalf("result=%+v", built)
	}
	entry := built.Manifest.Entries["opaque/worker"]
	if entry.SourceMap != nil || strings.ContainsAny(entry.File, `/\\`) {
		t.Fatalf("entry=%+v", entry)
	}
	manifest, err := os.ReadFile(built.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), `"version"`) || !strings.HasSuffix(string(manifest), "\n") {
		t.Fatalf("manifest=%s", manifest)
	}
	before := string(manifest)
	options.Entries[0].Source = "missing.ts"
	if _, err := BundleFacets(t.Context(), options); err == nil || !strings.HasPrefix(err.Error(), "Could not bundle facet entry opaque/worker") {
		t.Fatalf("build failure=%v", err)
	}
	manifest, err = os.ReadFile(built.ManifestPath)
	if err != nil || string(manifest) != before {
		t.Fatalf("failed build changed prior manifest: %s, %v", manifest, err)
	}
	paths, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasPrefix(path.Name(), ".bundle.tmp-") || strings.HasPrefix(path.Name(), "bundle.old-") {
			t.Fatalf("retained build directory %s", path.Name())
		}
	}
	options.Entries[0].Source = "entry.ts"
	options.Plugin.Version = new("next")
	options.SourceMap = true
	built, err = BundleFacets(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	*options.Plugin.Version = "caller mutation"
	if built.Manifest.Plugin.Version == nil || *built.Manifest.Plugin.Version != "next" {
		t.Fatalf("manifest borrowed caller version: %+v", built.Manifest.Plugin)
	}
	entry = built.Manifest.Entries["opaque/worker"]
	if entry.SourceMap == nil {
		t.Fatal("explicit source map omitted")
	}
	if _, err := os.Stat(filepath.Join(root, "bundle", *entry.SourceMap)); err != nil {
		t.Fatal(err)
	}
}

// upstream: packages/chord/src/node/package.ts:30-49,158-209 and packages/coding-agent/src/experimental/plugins/package.ts:83-99. Only the server wrapper supplies session/tui conventions; direct package callers supply their own ordered record.
func TestBundleFacetPackageUsesCallerConventionsAndSharedServerCompiler(t *testing.T) {
	isolateExperimentalTest(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"package.json":   `{"name":"example-plugin","version":"1","peerDependencies":{"@example/host":"*"}}`,
		"src/session.ts": `import "@example/host/subpath"; export default { id: "session", setup() {} };`,
		"src/tui.ts":     `export default { id: "tui", setup() {} };`,
	} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	options := BundleFacetPackageOptions{PackagePath: root, Outdir: "selected", DefaultFacets: []FacetEntrySource{{Name: "chosen", Source: "src/session.ts"}, {Name: "absent", Source: "src/absent.ts"}}}
	result, err := BundleFacetPackage(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.PackageDirectory != canonical || result.PackageJsonPath != filepath.Join(canonical, "package.json") || result.ManifestPath != filepath.Join(canonical, "selected", FacetBundleManifestFile) {
		t.Fatalf("package paths=%+v", result)
	}
	if len(result.Manifest.Entries) != 1 {
		t.Fatalf("entries=%v; want only caller's chosen entry", result.Manifest.Entries)
	}
	chosen := result.Manifest.Entries["chosen"]
	if chosen.SourceMap == nil || !reflect.DeepEqual(chosen.ExternalImports, []string{"@example/host/subpath"}) {
		t.Fatalf("chosen=%+v", chosen)
	}
	options.DefaultFacets = nil
	if _, err := BundleFacetPackage(t.Context(), options); err == nil || err.Error() != "Facet package example-plugin has no configured or conventional facet entries" {
		t.Fatalf("injected defaults: %v", err)
	}
	options.DefaultFacets = []FacetEntrySource{{Name: "z", Source: ""}, {Name: "a", Source: ""}}
	if _, err := BundleFacetPackage(t.Context(), options); err == nil || err.Error() != "Facet package default entry z must have a source path" {
		t.Fatalf("default validation order=%v", err)
	}
	server, err := CreateServerPluginPackage(t.TempDir(), "00000000-0000-4000-8000-000000000001", root)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := server.Build(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].EntryName != "tui" || artifacts[0].Plugin.Id != "example-plugin" || artifacts[0].SourceMapContents == nil {
		t.Fatalf("server artifacts=%+v", artifacts)
	}
	contents, err := os.ReadFile(server.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest FacetBundleManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != 2 || manifest.Entries["session"].File == "" || manifest.Entries["tui"].File == "" {
		t.Fatalf("server conventions=%+v", manifest.Entries)
	}
}

// upstream: packages/chord/src/node/bundle.ts:27-30,94-115. minify, define, platform and target reach the compiler; each option changes the bytes of the emitted bundle.
func TestBundleFacetsAppliesMinifyDefinePlatformAndTarget(t *testing.T) {
	isolateExperimentalTest(t)
	root := t.TempDir()
	source := "const longDescriptiveName = (value) => value ?? 1;\nexport default { id: typeof MODE === \"string\" ? MODE : \"unset\", platform: typeof process, fn: longDescriptiveName };\n"
	if err := os.WriteFile(filepath.Join(root, "entry.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	build := func(t *testing.T, options BundleFacetsOptions) string {
		t.Helper()
		options.Plugin = FacetBundlePlugin{Id: "options"}
		options.Entries = []FacetEntrySource{{Name: "main", Source: "entry.ts"}}
		options.Outdir = "bundle"
		options.WorkingDirectory = &root
		built, err := BundleFacets(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(root, "bundle", built.Manifest.Entries["main"].File))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	plain := build(t, BundleFacetsOptions{})
	if !strings.Contains(plain, "longDescriptiveName") || !strings.Contains(plain, "??") {
		t.Fatalf("default build is minified or lowered for node22.19:\n%s", plain)
	}
	if minified := build(t, BundleFacetsOptions{Minify: true}); strings.Contains(minified, "longDescriptiveName") || len(minified) >= len(plain) {
		t.Fatalf("Minify did not minify:\n%s", minified)
	}
	if defined := build(t, BundleFacetsOptions{Define: map[string]string{"MODE": `"prod"`}}); !strings.Contains(defined, `"prod"`) {
		t.Fatalf("Define was not applied:\n%s", defined)
	}
	if lowered := build(t, BundleFacetsOptions{Target: []string{"es2019"}}); strings.Contains(lowered, "??") {
		t.Fatalf("Target es2019 kept ??:\n%s", lowered)
	}
	if lowered := build(t, BundleFacetsOptions{Target: []string{"es2019", "chrome90"}}); strings.Contains(lowered, "??") {
		t.Fatalf("a target list was not applied:\n%s", lowered)
	}
	// The node platform leaves built-ins external; the browser and neutral platforms cannot resolve them.
	if err := os.WriteFile(filepath.Join(root, "builtin.ts"), []byte(`import { sep } from "node:path"; export default { sep };`), 0o600); err != nil {
		t.Fatal(err)
	}
	builtin := func(platform FacetBundlePlatform) (BundleFacetsResult, error) {
		return BundleFacets(t.Context(), BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "options"}, Entries: []FacetEntrySource{{Name: "main", Source: "builtin.ts"}}, Outdir: "builtin", WorkingDirectory: &root, Platform: platform})
	}
	for _, platform := range []FacetBundlePlatform{"", FacetBundlePlatformNode} {
		built, err := builtin(platform)
		if err != nil || !slices.Equal(built.Manifest.Entries["main"].ExternalImports, []string{"node:path"}) {
			t.Fatalf("platform %q: %+v, %v", platform, built, err)
		}
	}
	for _, platform := range []FacetBundlePlatform{FacetBundlePlatformBrowser, FacetBundlePlatformNeutral} {
		if _, err := builtin(platform); err == nil || !strings.Contains(err.Error(), "node:path") {
			t.Fatalf("platform %q resolved a Node built-in: %v", platform, err)
		}
	}
	// The neutral platform defaults to es2022, which keeps ??.
	if neutral := build(t, BundleFacetsOptions{Platform: FacetBundlePlatformNeutral}); !strings.Contains(neutral, "??") {
		t.Fatalf("neutral platform lowered ?? under the es2022 default:\n%s", neutral)
	}
	if browser := build(t, BundleFacetsOptions{Platform: FacetBundlePlatformBrowser, Target: []string{"es2019"}}); strings.Contains(browser, "??") {
		t.Fatalf("browser platform ignored Target:\n%s", browser)
	}
	// esbuild 0.28.2's JavaScript API rejects these options before it builds. An unknown platform throws a plain Error, so bundle.ts adds no detail; a separator inside a target or a define key is a build message (lib/main.js validateAndJoinStringArray and the define loop; probed against esbuild 0.28.2, whose message also carries a location in lib/main.js).
	for _, invalid := range []struct {
		options BundleFacetsOptions
		want    string
	}{
		{BundleFacetsOptions{Platform: "wasm"}, "Could not bundle facet entry main"},
		{BundleFacetsOptions{Target: []string{"es2019,chrome90"}}, "Could not bundle facet entry main\nInvalid target: es2019,chrome90"},
		{BundleFacetsOptions{Define: map[string]string{"MODE=x": `"prod"`}}, "Could not bundle facet entry main\nInvalid define: MODE=x"},
		{BundleFacetsOptions{Platform: "wasm", Target: []string{"a,b"}}, "Could not bundle facet entry main\nInvalid target: a,b"},
	} {
		options := invalid.options
		options.Plugin = FacetBundlePlugin{Id: "options"}
		options.Entries = []FacetEntrySource{{Name: "main", Source: "entry.ts"}}
		options.Outdir = "bad"
		options.WorkingDirectory = &root
		if _, err := BundleFacets(t.Context(), options); err == nil || err.Error() != invalid.want {
			t.Errorf("%+v: error=%v, want %q", invalid.options, err, invalid.want)
		}
	}
	// An empty target list passes no target, as esbuild does for target: [] and target: [""].
	for _, targets := range [][]string{{}, {""}} {
		if empty := build(t, BundleFacetsOptions{Target: targets}); !strings.Contains(empty, "??") {
			t.Fatalf("target %q lowered ??:\n%s", targets, empty)
		}
	}
}

// upstream: packages/chord/src/node/bundle.ts:40 (entries: Record<string, string>) and package.ts defaultFacets. A record is a JSON object whose
// keys enumerate as in Object.entries: integer names ascending first, the others in insertion order, a repeated name once at its first position.
func TestFacetEntrySourcesIsTheOrderedRecord(t *testing.T) {
	entries := FacetEntrySources{{"z", "old"}, {"10", "ten"}, {"2", "two"}, {"a", "a"}, {"z", "new"}, {"01", "not-index"}}
	encoded, err := json.Marshal(entries)
	if want := `{"2":"two","10":"ten","z":"new","a":"a","01":"not-index"}`; err != nil || string(encoded) != want {
		t.Fatalf("Marshal = %s, %v; want %s", encoded, err, want)
	}
	var decoded FacetEntrySources
	if err := json.Unmarshal([]byte(`{"b":"1","2":"x","a":"2","b":"3"}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if want := (FacetEntrySources{{"2", "x"}, {"b", "3"}, {"a", "2"}}); !reflect.DeepEqual(decoded, want) {
		t.Fatalf("Unmarshal = %v; want %v", decoded, want)
	}
	// Options carry the record as one field, in both directions.
	var options BundleFacetsOptions
	if err := json.Unmarshal([]byte(`{"Entries":{"b":"b.ts","a":"a.ts"}}`), &options); err != nil || !reflect.DeepEqual(options.Entries, FacetEntrySources{{"b", "b.ts"}, {"a", "a.ts"}}) {
		t.Fatalf("options Entries = %v, %v", options.Entries, err)
	}
	for _, bad := range []string{`["a"]`, `{"a":1}`} {
		if err := json.Unmarshal([]byte(bad), &decoded); err == nil {
			t.Errorf("Unmarshal(%s) accepted a non-record", bad)
		}
	}
}
