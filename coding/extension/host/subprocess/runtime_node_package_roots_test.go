package subprocess_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The Pi packages other than pi-coding-agent that the runtime serves from shim modules have a package root laid out like Pi's
// (node_pi_package_layout_test.go): the pinned package's package.json beside the vendored dist, and the entries Pi's package.json
// names as small modules that forward to the runtime module serving the package. The generator is
// automation/gen/vendor-pi-package-roots.mjs; the expected entries are written out here so a change of either shows.
var vendoredPackageRootEntries = map[string]string{
	"pi-agent-core/dist/index.js": "export * from \"../../../pi-agent-core.mjs\";\n",
	"pi-tui/dist/index.js":        "export * from \"../../../pi-tui.mjs\";\n",
	"pi-ai/dist/index.js":         "export * from \"../../../pi-ai.mjs\";\n",
	"pi-ai/dist/models.js":        "export * from \"../../../pi-ai-models.mjs\";\n",
	"pi-ai/dist/oauth.js":         "export * from \"../../../pi-ai-oauth.mjs\";\n",
	"pi-ai/dist/providers/all.js": "export * from \"../../sdk-bundle/providers.js\";\n",
	"pi-ai/dist/cli.js":           "import \"../cli.js\";\n",
}

// vendoredPackageRoots names the pinned package each vendored package root takes its package.json from.
var vendoredPackageRoots = []string{"pi-agent-core", "pi-tui", "pi-ai"}

func TestVendoredPiPackageRootsMatchThePinnedPackages(t *testing.T) {
	root := filepath.Join("runtime-node", "shims", "pi-dist")
	for _, name := range vendoredPackageRoots {
		want := readPinned(t, append(append([]string{}, pinnedPackageDist[name][:len(pinnedPackageDist[name])-1]...), "package.json")...)
		got, err := os.ReadFile(filepath.Join(root, name, "package.json"))
		if err != nil {
			t.Errorf("%s has no package.json: %v", name, err)
		} else if !bytes.Equal(got, want) {
			t.Errorf("shims/pi-dist/%s/package.json differs from the pinned package: run automation/gen/vendor-pi-dist.sh", name)
		}
	}
	for rel, want := range vendoredPackageRootEntries {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%v", err)
		} else if string(got) != want {
			t.Errorf("shims/pi-dist/%s = %q, want %q", rel, got, want)
		}
	}
}
