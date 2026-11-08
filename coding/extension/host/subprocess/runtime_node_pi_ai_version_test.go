//go:build !pig_strip_node_extensions

package subprocess

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// The Node runtime's vendored pi-ai is what an extension imports PI_AI_VERSION from. Pi's constant is the package
// version (packages/ai/src/utils/version.ts, kept in sync with package.json), so the vendored module must export the
// package version of the pin and not PiG's composite release version. The vendored sdk-bundle carries its own copy
// of the constant (the bundle of Pi's built output, which the sdk entry point exports), so each copy is checked.
func TestVendoredPiAiExportsThePinnedPackageVersion(t *testing.T) {
	root := filepath.Join("runtime-node", "shims", "pi-dist", "pi-ai")
	for _, copyOfConstant := range []struct{ file, pattern string }{
		{filepath.Join("utils", "version.js"), `(?m)^export const PI_AI_VERSION = "([^"]+)";$`},
		{filepath.Join("sdk-bundle", "chunk-DQ3IPTWT.js"), `(?m)^var PI_AI_VERSION = "([^"]+)";$`},
	} {
		body, err := os.ReadFile(filepath.Join(root, copyOfConstant.file))
		if err != nil {
			t.Fatal(err)
		}
		match := regexp.MustCompile(copyOfConstant.pattern).FindSubmatch(body)
		if match == nil {
			t.Fatalf("%s does not define PI_AI_VERSION as a string literal", copyOfConstant.file)
		}
		if got := string(match[1]); got != pigversion.UpstreamPackageVersion {
			t.Errorf("%s defines PI_AI_VERSION %q, pinned Pi package version is %q", copyOfConstant.file, got, pigversion.UpstreamPackageVersion)
		}
	}
}
