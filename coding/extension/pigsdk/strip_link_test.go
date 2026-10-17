package pigsdk

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const pigModule = "github.com/MichaelKinsy/PiG"

// stripGoListDeps runs `go list [-tags tags] -deps cmd/pig`.
func stripGoListDeps(t *testing.T, tags string) []string {
	t.Helper()
	if testing.Short() {
		t.Skip("go list of cmd/pig in -short mode")
	}
	args := []string{"list"}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	out, err := exec.Command("go", append(args, "-deps", pigModule+"/cmd/pig")...).Output()
	if err != nil {
		t.Fatalf("go %s: %v", strings.Join(args, " "), err)
	}
	return strings.Fields(string(out))
}

// D92: a pig_strip_extension_sdk_rust or pig_strip_extension_sdk_python build
// of cmd/pig does not link that SDK's embedded sources; the stock build does.
func TestStripExtensionSDKRustAndPythonBuildOmitsSDKPackages(t *testing.T) {
	stock := stripGoListDeps(t, "")
	for _, tc := range []struct{ tag, pkg string }{
		{"pig_strip_extension_sdk_rust", pigModule + "/extensions/sdk-rs"},
		{"pig_strip_extension_sdk_python", pigModule + "/extensions/sdk-py"},
	} {
		if !slices.Contains(stock, tc.pkg) {
			t.Errorf("the stock cmd/pig does not link %s", tc.pkg)
		}
		if slices.Contains(stripGoListDeps(t, tc.tag), tc.pkg) {
			t.Errorf("a %s build of cmd/pig links %s", tc.tag, tc.pkg)
		}
	}
}

// D92: the Go SDK package stays linked (fused extensions are built on it), so
// a pig_strip_extension_sdk_go build proves its omission by symbol: pigsdk,
// the only production package that reads the embedded sdk.Source, no longer
// references it, and the linker drops the unreferenced embed. The stock
// package references it.
func TestStripExtensionSDKGoBuildOmitsSDKSource(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pigsdk package in -short mode")
	}
	const symbol = pigModule + "/extensions/sdk.Source"
	references := func(tags string) bool {
		archive := filepath.Join(t.TempDir(), "pigsdk.a")
		args := []string{"build", "-o", archive}
		if tags != "" {
			args = append(args, "-tags", tags)
		}
		if out, err := exec.Command("go", append(args, pigModule+"/coding/extension/pigsdk")...).CombinedOutput(); err != nil {
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
		t.Errorf("the stock pigsdk package does not reference %s", symbol)
	}
	if references("pig_strip_extension_sdk_go") {
		t.Errorf("a pig_strip_extension_sdk_go build of pigsdk references %s", symbol)
	}
}
