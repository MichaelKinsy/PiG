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
// package version of the pin and not PiG's composite release version.
func TestVendoredPiAiExportsThePinnedPackageVersion(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("runtime-node", "shims", "pi-dist", "pi-ai", "utils", "version.js"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^export const PI_AI_VERSION = "([^"]+)";$`).FindSubmatch(body)
	if match == nil {
		t.Fatalf("version.js does not export PI_AI_VERSION as a string literal:\n%s", body)
	}
	if got := string(match[1]); got != pigversion.UpstreamPackageVersion {
		t.Fatalf("vendored pi-ai exports PI_AI_VERSION %q, pinned Pi package version is %q", got, pigversion.UpstreamPackageVersion)
	}
}
