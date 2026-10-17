package cli

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// D92: a Piglet Binary that compiled out the Go extension SDK does not link the SDK upgrade analysis. The analysis type checks a Go extension (go/types, go/importer),
// about 0.7 MiB of code a Binary without the Go SDK can never run. Stripping the SDK is proven by symbol: the stock coding/cli references upgrade.Open, and a
// pig_strip_extension_sdk_go build of it does not, so the linker drops the type checker.
func TestStripExtensionSDKGoBuildOmitsTheUpgradeAnalysis(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the cli package in -short mode")
	}
	const symbol = "github.com/MichaelKinsy/PiG/extensions/sdk/upgrade.Open"
	references := func(tags string) bool {
		archive := filepath.Join(t.TempDir(), "cli.a")
		args := []string{"build", "-o", archive}
		if tags != "" {
			args = append(args, "-tags", tags)
		}
		if out, err := exec.Command("go", append(args, "github.com/MichaelKinsy/PiG/coding/cli")...).CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		out, err := exec.Command("go", "tool", "nm", archive).Output()
		if err != nil {
			t.Fatalf("go tool nm: %v", err)
		}
		return slices.ContainsFunc(strings.Split(string(out), "\n"), func(line string) bool {
			return strings.HasSuffix(strings.TrimSpace(line), " "+symbol)
		})
	}
	if !references("") {
		t.Errorf("the stock cli package does not reference %s", symbol)
	}
	if references("pig_strip_extension_sdk_go") {
		t.Errorf("a pig_strip_extension_sdk_go build of cli references %s", symbol)
	}
}
