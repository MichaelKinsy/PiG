package experimental

// Pins packages/chord/src/node/manifest.ts by value: the bundle format discriminators and the manifest file name
// (manifest.ts:1-5) that the vendored Node bundle loader and the Go builder must agree on, and FacetBundlePlugin's
// optional version (manifest.ts:25-28), which JSON.stringify omits when undefined and keeps when it is an empty string.
// The bundle tests read and write manifests through the same Go constants, so a changed literal passes them.

import (
	"encoding/json"
	"testing"
)

func TestFacetBundleManifestConstantsAndPluginAreTheOnesManifestTSDeclares(t *testing.T) {
	if FacetBundleFormat != "chord.facet-bundle" || FacetBundleFormatVersion != 2 || FacetBundleManifestFile != "chord-facets.json" ||
		FacetBundleArtifactFormat != "chord.facet-bundle-artifact" || FacetBundleArtifactFormatVersion != 2 {
		t.Fatalf("constants = %q %d %q %q %d", FacetBundleFormat, FacetBundleFormatVersion, FacetBundleManifestFile, FacetBundleArtifactFormat, FacetBundleArtifactFormatVersion)
	}
	empty := ""
	for _, tc := range []struct {
		plugin FacetBundlePlugin
		want   string
	}{
		{FacetBundlePlugin{Id: "demo"}, `{"id":"demo"}`},
		{FacetBundlePlugin{Id: "demo", Version: new("1.2.3")}, `{"id":"demo","version":"1.2.3"}`},
		{FacetBundlePlugin{Id: "demo", Version: &empty}, `{"id":"demo","version":""}`},
	} {
		encoded, err := json.Marshal(tc.plugin)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("plugin %+v = %s, %v; want %s", tc.plugin, encoded, err, tc.want)
		}
	}
}
