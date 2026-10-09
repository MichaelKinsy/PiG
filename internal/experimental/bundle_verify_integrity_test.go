package experimental

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// packages/chord/src/node/bundle-loader.ts:25-26,147 (FacetBundleLoaderOptions.verifyIntegrity): the entry's SHA-256
// integrity is verified before evaluation unless verifyIntegrity is exactly false; an unset or true value verifies.
func TestFacetBundleLoaderVerifyIntegrityFalseSkipsTheSourceDigest(t *testing.T) {
	directory := t.TempDir()
	entryPath, output := filepath.Join(directory, "entry.ts"), filepath.Join(directory, "bundle")
	writeNodeFacetFile(t, entryPath, "export default { id: 'tampered', setup() {} };\n")
	result, err := BundleFacets(t.Context(), BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "tampered-bundle"}, Entries: []FacetEntrySource{{Name: "tampered", Source: entryPath}}, Outdir: output})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadFacetBundleManifest(result.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(output, manifest.Entries["tampered"].File)
	source, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	writeNodeFacetFile(t, file, string(source)+"\n// tampered after the manifest recorded its digest\n")
	yes, no := true, false
	for _, c := range []struct {
		name   string
		verify *bool
		reject bool
	}{{"unset verifies", nil, true}, {"true verifies", &yes, true}, {"false skips", &no, false}} {
		t.Run(c.name, func(t *testing.T) {
			loader, err := CreateFacetBundleLoader(FacetBundleLoaderOptions{ManifestPath: result.ManifestPath, Entry: "tampered", VerifyIntegrity: c.verify})
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := loader.Load(t.Context())
			if c.reject {
				if err == nil || !strings.Contains(err.Error(), "integrity check failed") {
					t.Fatalf("Load = %v, want the integrity check to fail", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load with VerifyIntegrity=false: %v", err)
			}
			_ = loaded.Dispose(t.Context())
		})
	}
}
