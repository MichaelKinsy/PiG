//go:build !pig_strip_node_extensions

package subprocess

import (
	"bytes"
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
	body, err := os.ReadFile(filepath.Join(root, "utils", "version.js"))
	if err != nil {
		t.Fatal(err)
	}
	checkPiAiVersion(t, "utils/version.js", regexp.MustCompile(`(?m)^export const PI_AI_VERSION = "([^"]+)";$`), body)

	// The bundle's chunk file names are content hashes that change on every regeneration, so locate the chunk by the
	// constant it defines rather than by name.
	chunks, err := filepath.Glob(filepath.Join(root, "sdk-bundle", "chunk-*.js"))
	if err != nil {
		t.Fatal(err)
	}
	bundlePattern := regexp.MustCompile(`(?m)^var PI_AI_VERSION = "([^"]+)";$`)
	found := 0
	for _, chunk := range chunks {
		body, err := os.ReadFile(chunk)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(body, []byte("PI_AI_VERSION")) {
			continue
		}
		if bundlePattern.Match(body) {
			found++
		}
		checkPiAiVersion(t, filepath.ToSlash(chunk), bundlePattern, body)
	}
	if found != 1 {
		t.Fatalf("sdk-bundle has %d chunks defining PI_AI_VERSION as a string literal, want 1", found)
	}
}

func checkPiAiVersion(t *testing.T, name string, pattern *regexp.Regexp, body []byte) {
	t.Helper()
	match := pattern.FindSubmatch(body)
	if match == nil {
		t.Fatalf("%s does not define PI_AI_VERSION as a string literal", name)
	}
	if got := string(match[1]); got != pigversion.UpstreamPackageVersion {
		t.Errorf("%s defines PI_AI_VERSION %q, pinned Pi package version is %q", name, got, pigversion.UpstreamPackageVersion)
	}
}
