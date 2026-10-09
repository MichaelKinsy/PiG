package experimental

// pi: packages/chord/src/node.ts

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// upstream: packages/chord/test/bundle.test.ts:26-246.
func TestFacetBundles(t *testing.T) {
	isolateExperimentalTest(t)
	t.Run("builds independent content-addressed entries and loads fresh reloadable generations", func(t *testing.T) {
		// upstream: packages/chord/test/bundle.test.ts:27-120.
		directory := t.TempDir()
		sourceDirectory, output := filepath.Join(directory, "src"), filepath.Join(directory, "bundle")
		writeNodeFacetFile(t, filepath.Join(sourceDirectory, "helper.ts"), `export const decorate = (value: string): string => "generation:" + value;`+"\n")
		entryPath, presentationPath := filepath.Join(sourceDirectory, "entry.ts"), filepath.Join(sourceDirectory, "presentation.ts")
		writeBundleGeneration(t, entryPath, "A")
		writeNodeFacetFile(t, presentationPath, `export default { id: "bundle-presentation", setup() {} };`+"\n")
		entries := []FacetEntrySource{{Name: "presentation", Source: presentationPath}, {Name: "worker", Source: entryPath}}
		build := func(version string) BundleFacetsResult {
			t.Helper()
			result, err := BundleFacets(t.Context(), BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "test-bundle", Version: new(version)}, Entries: entries, Outdir: output, SourceMap: true})
			if err != nil {
				t.Fatal(err)
			}
			return result
		}
		first := build("1")
		firstEntry := first.Manifest.Entries["worker"]
		if !regexp.MustCompile(`^facet-[a-f0-9]{12}-[A-Z0-9]+\.cjs$`).MatchString(firstEntry.File) {
			t.Fatalf("worker file=%q", firstEntry.File)
		}
		if firstEntry.SourceMap == nil || *firstEntry.SourceMap != firstEntry.File+".map" {
			t.Fatalf("source map=%v", firstEntry.SourceMap)
		}
		if !reflect.DeepEqual(firstEntry.ExternalImports, []string{"@earendil-works/chord"}) {
			t.Fatalf("imports=%q", firstEntry.ExternalImports)
		}
		if first.Manifest.Entries["presentation"].File == firstEntry.File {
			t.Fatal("entries share an output file")
		}
		files, err := os.ReadDir(output)
		if err != nil {
			t.Fatal(err)
		}
		var modules []string
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".cjs") {
				modules = append(modules, file.Name())
			}
		}
		if len(modules) != len(entries) {
			t.Fatalf("compiled entries=%q, want one per %v", modules, entries)
		}
		source, err := os.ReadFile(filepath.Join(output, firstEntry.File))
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{`require("@earendil-works/chord")`, "module.exports"} {
			if !strings.Contains(string(source), text) {
				t.Fatalf("source lacks %q", text)
			}
		}
		manifestText, err := os.ReadFile(first.ManifestPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(string(manifestText), "\n") {
			t.Fatal("manifest has no final newline")
		}
		presentation := loadBundleEntry(t, first.ManifestPath, "presentation", nil)
		assertBundleFacetIds(t, presentation, []string{"bundle-presentation"})
		disposeBundleFacets(t, presentation)
		artifact, err := ReadFacetBundleArtifact(FacetBundleArtifactReadOptions{ManifestPath: first.ManifestPath, Entry: "worker"})
		if err != nil {
			t.Fatal(err)
		}
		clone, err := json.Marshal(artifact)
		if err != nil {
			t.Fatal(err)
		}
		var copied any
		if err := json.Unmarshal(clone, &copied); err != nil {
			t.Fatal(err)
		}
		materialized := filepath.Join(directory, "materialized")
		transportedLoader, err := CreateFacetBundleArtifactLoader(FacetBundleArtifactLoaderOptions{Artifact: copied, TemporaryDirectory: materialized})
		if err != nil {
			t.Fatal(err)
		}
		transported, err := transportedLoader.Load(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { disposeBundleFacets(t, transported) })
		assertBundleFacetIds(t, transported, []string{"bundle-provider"})
		materializedFiles, err := os.ReadDir(materialized)
		if err != nil {
			t.Fatal(err)
		}
		// One transported artifact owns one materialized generation.
		if len(materializedFiles) != 1 {
			t.Fatalf("materialized=%v", materializedFiles)
		}
		disposeBundleFacets(t, transported)
		materializedFiles, err = os.ReadDir(materialized)
		if err != nil || len(materializedFiles) != 0 {
			t.Fatalf("released materialization=%v, %v", materializedFiles, err)
		}
		second := build("1")
		if !reflect.DeepEqual(second.Manifest.Entries["worker"], firstEntry) {
			t.Fatal("identical rebuild changed worker entry")
		}
		loader, err := CreateFacetBundleLoader(FacetBundleLoaderOptions{ManifestPath: second.ManifestPath, Entry: "worker"})
		if err != nil {
			t.Fatal(err)
		}
		loadedA, err := loader.Load(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { disposeBundleFacets(t, loadedA) })
		copyA, err := loader.Load(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { disposeBundleFacets(t, copyA) })
		// Go facets are records containing closures, not comparable JS objects; each load must return distinct facet records.
		if len(copyA.Facets) != 1 || len(loadedA.Facets) != 1 || &copyA.Facets[0] == &loadedA.Facets[0] {
			t.Fatal("load reused the same facet record")
		}
		disposeBundleFacets(t, copyA)
		var retained *chord.FacetService
		consumer := chord.Facet{Id: "bundle-consumer", Setup: func(env *chord.FacetEnvironment) error {
			var err error
			retained, err = chord.UseFacetService(env, "test.bundle.generation", true)
			return err
		}}
		host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: append([]chord.Facet{consumer}, loadedA.Facets...)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := host.Dispose(context.Background()); err != nil {
				t.Error(err)
			}
		})
		if got := readBundleGeneration(t, retained); got != "generation:A" {
			t.Fatalf("read=%q", got)
		}
		writeBundleGeneration(t, entryPath, "B")
		third := build("2")
		if third.Manifest.Entries["worker"].File == firstEntry.File {
			t.Fatal("changed source retained its filename")
		}
		loadedB, err := loader.Load(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { disposeBundleFacets(t, loadedB) })
		if err := host.Reload(t.Context(), loadedB.Facets); err != nil {
			t.Fatal(err)
		}
		disposeBundleFacets(t, loadedA)
		if got := readBundleGeneration(t, retained); got != "generation:B" {
			t.Fatalf("retained read=%q", got)
		}
		if err := host.Dispose(t.Context()); err != nil {
			t.Fatal(err)
		}
		disposeBundleFacets(t, loadedB)
	})
	t.Run("loads host externals through the controlled CommonJS require", func(t *testing.T) {
		// upstream: packages/chord/test/bundle.test.ts:122-154.
		directory := t.TempDir()
		externalPath, entryPath := filepath.Join(directory, "host.mjs"), filepath.Join(directory, "entry.ts")
		writeNodeFacetFile(t, externalPath, `export const named = "host-named";`+"\n")
		writeNodeFacetFile(t, entryPath, "import { named } from \"@example/host\";\n"+"export const loadDynamic = () => import(\"@example/dynamic\");\n"+"if (named !== \"host-named\") throw new Error(\"external mismatch\");\n"+"export default { id: \"external-facet\", setup() {} };\n")
		result, err := BundleFacets(t.Context(), BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "external-bundle"}, Entries: []FacetEntrySource{{Name: "worker", Source: entryPath}}, External: []string{"@example/host", "@example/dynamic"}, Outdir: filepath.Join(directory, "bundle")})
		if err != nil {
			t.Fatal(err)
		}
		source, err := os.ReadFile(filepath.Join(directory, "bundle", result.Manifest.Entries["worker"].File))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(source), "import(") || !strings.Contains(string(source), `require("@example/dynamic")`) {
			t.Fatalf("dynamic import was not lowered: %s", source)
		}
		urlPath := filepath.ToSlash(externalPath)
		if !strings.HasPrefix(urlPath, "/") {
			urlPath = "/" + urlPath
		}
		target := (&url.URL{Scheme: "file", Path: urlPath}).String()
		loaded := loadBundleEntry(t, result.ManifestPath, "worker", map[string]string{"@example/host": target, "@example/dynamic": target})
		assertBundleFacetIds(t, loaded, []string{"external-facet"})
		disposeBundleFacets(t, loaded)
	})
	t.Run("builds plugin packages from conventional and configured facet entries", func(t *testing.T) {
		// upstream: packages/chord/test/bundle.test.ts:156-211.
		directory, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		sourceDirectory := filepath.Join(directory, "src")
		writeNodeFacetFile(t, filepath.Join(directory, "package.json"), `{"name":"@example/conventional-plugin","version":"1.2.3","peerDependencies":{"@example/host":"^1.0.0"}}`+"\n")
		writeNodeFacetFile(t, filepath.Join(sourceDirectory, "session.ts"), `import "@example/host/plugin"; export default { id: "package-session", setup() {} };`+"\n")
		writeNodeFacetFile(t, filepath.Join(sourceDirectory, "tui.ts"), `export default { id: "package-tui", setup() {} };`+"\n")
		writeNodeFacetFile(t, filepath.Join(sourceDirectory, "contract.ts"), "export const ignored = true;\n")
		writeNodeFacetFile(t, filepath.Join(sourceDirectory, "presentation.ts"), `export default { id: "configured-tui", setup() {} };`+"\n")
		conventional, err := BundleFacetPackage(t.Context(), BundleFacetPackageOptions{PackagePath: directory, Outdir: filepath.Join(directory, "build"), DefaultFacets: []FacetEntrySource{{Name: "session", Source: "src/session.ts"}, {Name: "tui", Source: "src/tui.ts"}, {Name: "browser", Source: "src/browser.ts"}}})
		if err != nil {
			t.Fatal(err)
		}
		if conventional.PackageDirectory != directory {
			t.Fatalf("package directory=%s", conventional.PackageDirectory)
		}
		if want := (FacetBundlePlugin{Id: "@example/conventional-plugin", Version: new("1.2.3")}); !reflect.DeepEqual(conventional.Manifest.Plugin, want) {
			t.Fatalf("plugin=%#v", conventional.Manifest.Plugin)
		}
		assertBundleEntryNames(t, conventional.ManifestPath, []string{"session", "tui"})
		if !reflect.DeepEqual(conventional.Manifest.Entries["session"].ExternalImports, []string{"@example/host/plugin"}) {
			t.Fatalf("peer imports=%v", conventional.Manifest.Entries["session"].ExternalImports)
		}
		if name := conventional.Manifest.Entries["tui"].SourceMap; name == nil || !strings.HasSuffix(*name, ".cjs.map") {
			t.Fatalf("source map=%v", name)
		}
		writeNodeFacetFile(t, filepath.Join(directory, "package.json"), `{"name":"@example/conventional-plugin","version":"2.0.0","chord":{"facets":{"session":false,"tui":"src/presentation.ts"},"sourceMap":false}}`+"\n")
		configured, err := BundleFacetPackage(t.Context(), BundleFacetPackageOptions{PackagePath: filepath.Join(directory, "package.json"), Outdir: filepath.Join(directory, "build"), DefaultFacets: []FacetEntrySource{{Name: "session", Source: "src/session.ts"}, {Name: "tui", Source: "src/tui.ts"}}})
		if err != nil {
			t.Fatal(err)
		}
		assertBundleEntryNames(t, configured.ManifestPath, []string{"tui"})
		if configured.Manifest.Entries["tui"].SourceMap != nil {
			t.Fatal("sourceMap=false was ignored")
		}
		loaded := loadBundleEntry(t, configured.ManifestPath, "tui", nil)
		assertBundleFacetIds(t, loaded, []string{"configured-tui"})
		disposeBundleFacets(t, loaded)
	})
	t.Run("rejects invalid plugin package entry configuration", func(t *testing.T) {
		// upstream: packages/chord/test/bundle.test.ts:213-227.
		directory := t.TempDir()
		writeNodeFacetFile(t, filepath.Join(directory, "package.json"), `{"name":"invalid-plugin","version":"1.0.0","chord":{"facets":{"tui":"../outside.ts"}}}`+"\n")
		_, err := BundleFacetPackage(t.Context(), BundleFacetPackageOptions{PackagePath: directory, Outdir: filepath.Join(directory, "build")})
		if err == nil || !strings.Contains(err.Error(), "escapes the package directory") {
			t.Fatalf("build error=%v", err)
		}
	})
	t.Run("rejects corrupt entries and invalid module exports", func(t *testing.T) {
		// upstream: packages/chord/test/bundle.test.ts:229-245.
		directory := t.TempDir()
		entryPath, output := filepath.Join(directory, "entry.ts"), filepath.Join(directory, "bundle")
		writeNodeFacetFile(t, entryPath, "export default { id: 'missing-setup' };\n")
		result, err := BundleFacets(t.Context(), BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "invalid-bundle"}, Entries: []FacetEntrySource{{Name: "invalid", Source: entryPath}}, Outdir: output})
		if err != nil {
			t.Fatal(err)
		}
		loader, err := CreateFacetBundleLoader(FacetBundleLoaderOptions{ManifestPath: result.ManifestPath, Entry: "invalid"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := loader.Load(t.Context()); err == nil || !strings.Contains(err.Error(), "has no setup function") {
			t.Fatalf("invalid export=%v", err)
		}
		manifest, err := ReadFacetBundleManifest(result.ManifestPath)
		if err != nil {
			t.Fatal(err)
		}
		writeNodeFacetFile(t, filepath.Join(output, manifest.Entries["invalid"].File), "export default {};\n")
		if _, err := loader.Load(t.Context()); err == nil || !strings.Contains(err.Error(), "integrity check failed") {
			t.Fatalf("corruption=%v", err)
		}
		// packages/chord/src/node/bundle-loader.ts:26 verifyIntegrity is optional, and bundle-loader.ts:147 checks the source
		// unless it is false. A replaced entry that is a valid facet module still fails the check by default and with true, and
		// false loads it.
		writeNodeFacetFile(t, filepath.Join(output, manifest.Entries["invalid"].File), "module.exports = { default: { id: \"replaced\", setup() {} } };\n")
		if _, err := loader.Load(t.Context()); err == nil || !strings.Contains(err.Error(), "integrity check failed") {
			t.Fatalf("replaced entry, default verification=%v", err)
		}
		for _, verify := range []bool{true, false} {
			loader, err := CreateFacetBundleLoader(FacetBundleLoaderOptions{ManifestPath: result.ManifestPath, Entry: "invalid", VerifyIntegrity: &verify})
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := loader.Load(t.Context())
			if verify {
				if err == nil || !strings.Contains(err.Error(), "integrity check failed") {
					t.Fatalf("verifyIntegrity=true: replaced entry=%v", err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("verifyIntegrity=false: replaced entry=%v", err)
			}
			assertBundleFacetIds(t, loaded, []string{"replaced"})
			disposeBundleFacets(t, loaded)
		}
	})
}

func writeBundleGeneration(t *testing.T, path, generation string) {
	t.Helper()
	encoded, err := json.Marshal(generation)
	if err != nil {
		t.Fatal(err)
	}
	writeNodeFacetFile(t, path, "import \"@earendil-works/chord\";\n"+"import { decorate } from \"./helper.ts\";\n"+"const Value = { id: \"test.bundle.generation\", local: true };\n"+"export default { id: \"bundle-provider\", setup(env) {\n"+"  env.provide(Value, { read() { return decorate("+string(encoded)+"); } });\n"+"}};\n")
}
func loadBundleEntry(t *testing.T, path, entry string, external map[string]string) chord.LoadedFacets {
	t.Helper()
	var resolver FacetBundleExternalResolver
	if external != nil {
		resolver = func(specifier string) (string, bool, error) { value, ok := external[specifier]; return value, ok, nil }
	}
	loader, err := CreateFacetBundleLoader(FacetBundleLoaderOptions{ManifestPath: path, Entry: entry, ResolveExternal: resolver})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loader.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { disposeBundleFacets(t, loaded) })
	return loaded
}
func disposeBundleFacets(t *testing.T, loaded chord.LoadedFacets) {
	t.Helper()
	if err := loaded.Dispose(context.Background()); err != nil {
		t.Error(err)
	}
}
func assertBundleFacetIds(t *testing.T, loaded chord.LoadedFacets, want []string) {
	t.Helper()
	ids := make([]string, len(loaded.Facets))
	for i, facet := range loaded.Facets {
		ids[i] = facet.Id
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("facets=%v, want %v", ids, want)
	}
}
func readBundleGeneration(t *testing.T, ref *chord.FacetService) string {
	t.Helper()
	implementation, err := ref.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	local, ok := implementation.(*nodeFacetLocalService)
	if !ok {
		t.Fatalf("service=%T", implementation)
	}
	callback, err := local.generation.property(t.Context(), local.value, "read")
	if err != nil {
		t.Fatal(err)
	}
	value, err := local.generation.invoke(t.Context(), true, callback, &local.value, nil)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	if err := local.generation.decodeJSON(t.Context(), value, &text); err != nil {
		t.Fatal(err)
	}
	return text
}
func assertBundleEntryNames(t *testing.T, path string, want []string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	root, ok := facetJSONObject(raw)
	if !ok {
		t.Fatal("manifest not object")
	}
	entries, _ := facetJSONField(root, "entries")
	fields, ok := facetJSONObject(entries)
	if !ok {
		t.Fatal("entries not object")
	}
	names := make([]string, len(fields))
	for i, field := range fields {
		names[i] = field.key
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("entries=%v, want %v", names, want)
	}
}
