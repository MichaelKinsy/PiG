package experimental

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// upstream: chord/src/node/bundle-loader.ts: `if (options.verifyIntegrity !== false) verifySource(source, entry)`; an edited entry fails with "Facet bundle integrity check failed for <file>" unless verification is turned off.
// Pi source: packages/chord/src/node/bundle-loader.ts (verifyIntegrity)
// mutation-checked: not forwarding VerifyIntegrity fails it
// Pi: packages/chord/src/node/bundle-loader.ts:26 (verifyIntegrity)
func TestFacetBundleLoaderVerifiesIntegrityUnlessDisabled(t *testing.T) {
	isolateExperimentalTest(t)
	directory := t.TempDir()
	source := filepath.Join(directory, "src", "tampered.ts")
	writeNodeFacetFile(t, source, `export default { id: "bundle-tampered", setup() {} };`+"\n")
	result, err := BundleFacets(t.Context(), BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "test-integrity"}, Entries: []FacetEntrySource{{Name: "tampered", Source: source}}, Outdir: filepath.Join(directory, "bundle")})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(filepath.Dir(result.ManifestPath), result.Manifest.Entries["tampered"].File)
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(original, []byte("\n// edited after the build\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	load := func(verify *bool) error {
		loader, err := CreateFacetBundleLoader(FacetBundleLoaderOptions{ManifestPath: result.ManifestPath, Entry: "tampered", VerifyIntegrity: verify})
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := loader.Load(t.Context())
		if err == nil {
			disposeBundleFacets(t, loaded)
		}
		return err
	}
	for name, verify := range map[string]*bool{"default": nil, "explicit": new(true)} {
		if err := load(verify); err == nil || !strings.Contains(err.Error(), "Facet bundle integrity check failed for "+result.Manifest.Entries["tampered"].File) {
			t.Fatalf("%s verification of an edited entry: %v", name, err)
		}
	}
	if err := load(new(false)); err != nil {
		t.Fatalf("an edited entry must load when verification is off: %v", err)
	}
}
