package packagecontent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
)

// Pi 0.87.1 pi-manifest.ts:16-33 and package-manager.ts:2153-2202 treat
// malformed metadata as absent, keep valid sibling fields, and do not emit a
// diagnostic. This uses the same synthetic manifests as the CLI parity test.
func TestStartupUsesDiscoveryManifestParsing(t *testing.T) {
	fixtures := "../../test/parity/scenarios/extensions-runtime/testdata/package-manifests/agent/packages"
	entries, err := os.ReadDir(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			root := t.TempDir()
			if err := os.CopyFS(root, os.DirFS(filepath.Join(fixtures, entry.Name()))); err != nil {
				t.Fatal(err)
			}
			want, err := Discover(root)
			if err != nil {
				t.Fatal(err)
			}
			got, _, issues, err := ValidateConfiguredForStartupWithResolver(root, nil, func(string) (extsource.Definition, error) {
				return extsource.Definition{Language: "node", Form: extsource.Factory}, nil
			})
			if err != nil || len(issues) != 0 {
				t.Fatalf("startup = %v, issues = %v", err, issues)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("startup = %#v, discovery = %#v", got, want)
			}
			inspected, _, err := InspectConfigured(root, map[Kind][]string{Extensions: {}})
			if err != nil || !reflect.DeepEqual(inspected, want) {
				t.Fatalf("inspection = %#v, %v; discovery = %#v", inspected, err, want)
			}
			if strings.HasPrefix(entry.Name(), "bad-") {
				if _, err := Validate(root); err == nil {
					t.Fatal("explicit install validation must remain strict (D57)")
				}
			}
		})
	}
}
