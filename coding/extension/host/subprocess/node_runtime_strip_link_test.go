package subprocess

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// stripGoList runs `go list [-tags tags] args...` from this package's module.
func stripGoList(t *testing.T, tags string, args ...string) []string {
	t.Helper()
	if testing.Short() {
		t.Skip("go list of cmd/pig in -short mode")
	}
	cmd := []string{"list"}
	if tags != "" {
		cmd = append(cmd, "-tags", tags)
	}
	out, err := exec.Command("go", append(cmd, args...)...).Output()
	if err != nil {
		t.Fatalf("go %s: %v", strings.Join(append(cmd, args...), " "), err)
	}
	return strings.Fields(string(out))
}

// skipWithoutNodeExtensions skips a test that runs a Node extension in a
// build or process without the node-extensions feature (D92).
func skipWithoutNodeExtensions(t testing.TB) {
	t.Helper()
	if err := nodeRuntimeUnavailable(); err != nil {
		t.Skip(err)
	}
}

// D92: a pig_strip_node_extensions build of cmd/pig links neither the
// embedded Node runtime archive nor the zstd decoder that unpacks it; the
// stock build links both.
func TestStripNodeExtensionsBuildOmitsRuntimeAndZstd(t *testing.T) {
	const tag = "pig_strip_node_extensions"
	const zstd = "github.com/klauspost/compress/zstd"
	const pkg = "github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	if stock := stripGoList(t, "", "-deps", "github.com/MichaelKinsy/PiG/cmd/pig"); !slices.Contains(stock, zstd) {
		t.Errorf("the stock cmd/pig does not link %s", zstd)
	}
	if stripped := stripGoList(t, tag, "-deps", "github.com/MichaelKinsy/PiG/cmd/pig"); slices.ContainsFunc(stripped, func(p string) bool { return p == zstd || strings.HasPrefix(p, zstd+"/") }) {
		t.Errorf("a %s build of cmd/pig links %s", tag, zstd)
	}
	if stock := stripGoList(t, "", "-f", `{{join .EmbedFiles " "}}`, pkg); !slices.Contains(stock, "runtime-node.zip") {
		t.Errorf("the stock subprocess package does not embed runtime-node.zip: %v", stock)
	}
	if stripped := stripGoList(t, tag, "-f", `{{join .EmbedFiles " "}}`, pkg); slices.Contains(stripped, "runtime-node.zip") {
		t.Errorf("a %s build of the subprocess package embeds runtime-node.zip", tag)
	}
}
