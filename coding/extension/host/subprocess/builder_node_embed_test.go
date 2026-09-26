package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

// pi-tui's utils.js imports the vendored get-east-asian-width package, so the
// materialized runtime must carry it; without it every extension importing
// @earendil-works/pi-tui fails to load.
func TestNodeRuntimeEmbedsVendoredEastAsianWidth(t *testing.T) {
	for _, name := range []string{"index.js", "lookup.js", "lookup-data.js", "utilities.js", "package.json"} {
		if _, err := nodeRuntimeFS.ReadFile("runtime-node/shims/get-east-asian-width/" + name); err != nil {
			t.Errorf("embedded runtime lacks shims/get-east-asian-width/%s: %v", name, err)
		}
	}
}

// The runtime's Pi modules import the vendored Pi dist files, yaml, marked
// and partial-json, so the materialized runtime must carry every one of them.
func TestNodeRuntimeEmbedsVendoredPiDistAndDependencies(t *testing.T) {
	for _, dir := range []string{"pi-dist", "yaml", "marked", "partial-json"} {
		root := filepath.Join("runtime-node", "shims", dir)
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if _, err := nodeRuntimeFS.ReadFile(filepath.ToSlash(path)); err != nil {
				t.Errorf("embedded runtime lacks %s: %v", filepath.ToSlash(path), err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
