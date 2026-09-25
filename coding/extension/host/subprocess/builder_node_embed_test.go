package subprocess

import "testing"

// pi-tui-utils.mjs imports the vendored get-east-asian-width package, so the
// materialized runtime must carry it; without it every extension importing
// @earendil-works/pi-tui fails to load.
func TestNodeRuntimeEmbedsVendoredEastAsianWidth(t *testing.T) {
	for _, name := range []string{"index.js", "lookup.js", "lookup-data.js", "utilities.js", "package.json"} {
		if _, err := nodeRuntimeFS.ReadFile("runtime-node/shims/get-east-asian-width/" + name); err != nil {
			t.Errorf("embedded runtime lacks shims/get-east-asian-width/%s: %v", name, err)
		}
	}
}
