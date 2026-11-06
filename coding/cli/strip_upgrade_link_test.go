package cli

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The extension upgrade analysis type checks a Go extension with go/types and go/importer, about 1 MiB of code. The cli package does not link it, stripped or not: the go command builds and runs the analysis driver of the staged SDK (extensions/sdk/upgrade/driver). The test proves it by symbol: no build of cli references upgrade.Open, the entry of the type checker, so the linker drops go/types from every binary that links cli.
func TestCLIBuildsDoNotLinkTheUpgradeTypeChecker(t *testing.T) {
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
	for _, tags := range []string{"", "pig_strip_extension_sdk_go"} {
		if references(tags) {
			t.Errorf("a build of cli with tags %q references %s", tags, symbol)
		}
	}
}
