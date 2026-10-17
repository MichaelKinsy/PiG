package coding

import (
	"os/exec"
	"strings"
	"testing"
)

// The public coding package must not link the SDK sources the staged-SDK check compares against (coding/extension/pigsdk embeds
// extensions/sdk): a program that imports coding and builds as a Go extension cell would compile against the staged SDK, which
// omits bundle.go, and fail with `undefined: sdk.BundledFiles`. The pig command registers the check instead
// (subprocess.SetDefaultStagedSDKVerifier).
func TestCodingDoesNotImportThePigSDKStager(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for dep := range strings.SplitSeq(string(out), "\n") {
		if strings.HasSuffix(dep, "/coding/extension/pigsdk") {
			t.Fatalf("package coding links %s", dep)
		}
	}
}
