package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The extension upgrade analysis type checks a Go extension with go/types and go/importer, about 1 MiB of code. The cli package does not link it, stripped or not: the go command builds and runs the analysis driver of the staged SDK (extensions/sdk/upgrade/driver). The test proves it by symbol: no build of cli references a symbol of package upgrade, which imports go/types. cli imports upgrade/plan, the names of that package that read no types, so a binary that links cli links neither the type checker nor its package initializers.
func TestCLIBuildsDoNotLinkTheUpgradeTypeChecker(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the cli package in -short mode")
	}
	// The symbols of upgrade/plan and of the internal package behind it carry a longer path: only package upgrade itself matches.
	const symbol = "github.com/MichaelKinsy/PiG/extensions/sdk/upgrade."
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
		return strings.Contains(string(out), symbol)
	}
	for _, tags := range []string{"", "pig_strip_extension_sdk_go"} {
		if references(tags) {
			t.Errorf("a build of cli with tags %q references a symbol of package upgrade (%s...)", tags, symbol)
		}
	}
}
