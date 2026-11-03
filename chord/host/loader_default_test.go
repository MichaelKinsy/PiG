package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeNodeLoaderFixture lays out a directory whose node_modules holds a chord package the child resolves above the facet modules (the child
// imports createHost from it), and returns the directory.
func writeNodeLoaderFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	chord := filepath.Join(dir, "node_modules", "@earendil-works", "chord")
	if err := os.MkdirAll(chord, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(chord, "package.json"): `{"name":"@earendil-works/chord","type":"module","exports":{".":"./index.mjs"}}`,
		filepath.Join(chord, "index.mjs"):    "export const createHost = () => ({ load: async () => {} });\n",
		filepath.Join(dir, "facet.mjs"):      "export default { setup() {} };\n",
	}
	for path, text := range files {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// packages/chord/src/loader.ts nodeLoader: a FacetLoader that imports the modules and names each facet by its module URL; with no module it loads
// nothing and starts no child; a module that cannot be imported fails the load with the import error naming it, not with a facet.
func TestNodeLoaderIsAFacetLoaderThatNamesFacetsByURLLoadsNothingForNoModulesAndFailsForAMissingModule(t *testing.T) {
	t.Cleanup(func() { _ = defaultNodeLoader.Close() })
	var loader FacetLoader = NodeLoader
	facets, err := loader(nil)
	if err != nil || facets == nil || len(facets) != 0 {
		t.Fatalf("NodeLoader(nil) = %#v, %v, want an empty, non-nil list", facets, err)
	}
	dir := writeNodeLoaderFixture(t)
	facets, err = NodeLoader([]string{filepath.Join(dir, "facet.mjs")})
	if err != nil || len(facets) != 1 || facets[0].Name != fileURL(filepath.Join(dir, "facet.mjs")) {
		t.Fatalf("NodeLoader(facet module) = %+v, %v, want one facet named by its file URL", facets, err)
	}
	missing := filepath.Join(dir, "missing-facet.mjs")
	facets, err = NodeLoader([]string{missing})
	if err == nil || len(facets) != 0 || !strings.Contains(err.Error(), "missing-facet.mjs") || strings.Contains(err.Error(), "is not installed above") {
		t.Fatalf("NodeLoader(missing module) = %#v, %v, want the import error naming the module, with chord resolved", facets, err)
	}
}
