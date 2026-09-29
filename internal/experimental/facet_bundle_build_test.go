package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
