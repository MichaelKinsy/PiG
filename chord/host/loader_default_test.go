package host

import (
	"path/filepath"
	"strings"
	"testing"
)

// packages/chord/src/loader.ts nodeLoader: it is a FacetLoader that imports fresh copies of facet modules; with no module it loads nothing and
// starts no child, and a module that cannot be imported fails the load with the child's error rather than yielding a facet.
func TestNodeLoaderIsAFacetLoaderThatLoadsNothingForNoModulesAndFailsForAMissingOne(t *testing.T) {
	var loader FacetLoader = NodeLoader
	facets, err := loader(nil)
	if err != nil || facets == nil || len(facets) != 0 {
		t.Fatalf("NodeLoader(nil) = %#v, %v, want an empty, non-nil list", facets, err)
	}
	missing := filepath.Join(t.TempDir(), "missing-facet.mjs")
	facets, err = NodeLoader([]string{missing})
	if err == nil || len(facets) != 0 || !strings.Contains(err.Error(), "@earendil-works/chord is not installed above") {
		t.Fatalf("NodeLoader(missing module) = %#v, %v, want the import error of the child and no facet", facets, err)
	}
}
