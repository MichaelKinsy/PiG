package experimental

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"testing"
)

// upstream: packages/chord/test/bundle.test.ts:122-154 resolves host externals through the controlled require; a transported
// artifact resolves them through the loader's ResolveExternal too, and fails to load when it refuses one.
func TestFacetBundleArtifactLoaderResolvesExternals(t *testing.T) {
	directory := t.TempDir()
	externalPath, entryPath := filepath.Join(directory, "host.mjs"), filepath.Join(directory, "entry.ts")
	writeNodeFacetFile(t, externalPath, `export const named = "host-named";`+"\n")
	writeNodeFacetFile(t, entryPath, "import { named } from \"@example/host\";\n"+"if (named !== \"host-named\") throw new Error(\"external mismatch\");\n"+"export default { id: \"artifact-external-facet\", setup() {} };\n")
	result, err := BundleFacets(t.Context(), BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "artifact-external"}, Entries: []FacetEntrySource{{Name: "worker", Source: entryPath}}, External: []string{"@example/host"}, Outdir: filepath.Join(directory, "bundle")})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := ReadFacetBundleArtifact(FacetBundleArtifactReadOptions{ManifestPath: result.ManifestPath, Entry: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	var transported any
	if err := json.Unmarshal(encoded, &transported); err != nil {
		t.Fatal(err)
	}
	urlPath := filepath.ToSlash(externalPath)
	if urlPath[0] != '/' {
		urlPath = "/" + urlPath
	}
	target := (&url.URL{Scheme: "file", Path: urlPath}).String()

	var asked []string
	loader, err := CreateFacetBundleArtifactLoader(FacetBundleArtifactLoaderOptions{
		Artifact: transported, TemporaryDirectory: filepath.Join(directory, "materialized"),
		ResolveExternal: func(specifier string) (string, bool, error) {
			asked = append(asked, specifier)
			return target, true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loader.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { disposeBundleFacets(t, loaded) })
	assertBundleFacetIds(t, loaded, []string{"artifact-external-facet"})
	if len(asked) != 1 || asked[0] != "@example/host" {
		t.Fatalf("ResolveExternal was asked for %v, want the bundle's one external", asked)
	}
}
