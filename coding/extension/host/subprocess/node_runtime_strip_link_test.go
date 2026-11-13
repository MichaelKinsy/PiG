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

// D92: a pig_strip_node_extensions build of the subprocess package links neither the
// embedded Node runtime archive nor the zstd decoder that unpacks it; the
// stock build links both. The subprocess package imports tui, whose highlighter's full grammar snapshot is a zstd
// frame that pig_strip_syntax_highlight strips, so the decoder's absence is asserted with that tag beside the
// runtime's, and with the runtime tag alone nothing of the runtime pack's unpacker may remain linked. (A cmd/pig
// built with only these tags still links the decoder: the docs bundle, the changelog and the SDK bundles are zstd
// frames too; see TestFullStripBuildOfCmdPigLinksNoZstd.)
func TestStripNodeExtensionsBuildOmitsRuntimeAndZstd(t *testing.T) {
	const runtimeTag = "pig_strip_node_extensions"
	const tag = runtimeTag + ",pig_strip_syntax_highlight"
	const zstd = "github.com/klauspost/compress/zstd"
	const pkg = "github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	if stock := stripGoList(t, "", "-deps", pkg); !slices.Contains(stock, zstd) {
		t.Errorf("the stock subprocess package does not link %s", zstd)
	}
	if stripped := stripGoList(t, tag, "-deps", pkg); slices.ContainsFunc(stripped, func(p string) bool { return p == zstd || strings.HasPrefix(p, zstd+"/") }) {
		t.Errorf("a %s build of the subprocess package links %s", tag, zstd)
	}
	const noderuntimepack = pkg + "/internal/noderuntimepack"
	if stripped := stripGoList(t, runtimeTag, "-deps", pkg); slices.Contains(stripped, noderuntimepack) {
		t.Errorf("a %s build of the subprocess package links %s", runtimeTag, noderuntimepack)
	}
	if stock := stripGoList(t, "", "-f", `{{join .EmbedFiles " "}}`, pkg); !slices.Contains(stock, "runtime-node.pack") {
		t.Errorf("the stock subprocess package does not embed runtime-node.pack: %v", stock)
	}
	if stripped := stripGoList(t, tag, "-f", `{{join .EmbedFiles " "}}`, pkg); slices.Contains(stripped, "runtime-node.pack") {
		t.Errorf("a %s build of the subprocess package embeds runtime-node.pack", tag)
	}
}

// D92: a cmd/pig built with every Piglet strip tag links no zstd decoder. Each zstd frame (the Node runtime pack, the docs
// bundle, the changelog, the SDK bundles) is stripped by some tag, so the decoder goes with the last of them.
func TestFullStripBuildOfCmdPigLinksNoZstd(t *testing.T) {
	if testing.Short() {
		t.Skip("go run and go list of cmd/pig in -short mode")
	}
	const zstd = "github.com/klauspost/compress/zstd"
	out, err := exec.Command("go", "run", "github.com/MichaelKinsy/PiG/coding/piglet/internal/genstripids", "-print-tags").Output()
	if err != nil {
		t.Fatalf("genstripids -print-tags: %v", err)
	}
	tags := strings.TrimSpace(string(out))
	if tags == "" {
		t.Fatal("genstripids printed no tags")
	}
	if stripped := stripGoList(t, tags, "-deps", "github.com/MichaelKinsy/PiG/cmd/pig"); slices.ContainsFunc(stripped, func(p string) bool { return p == zstd || strings.HasPrefix(p, zstd+"/") }) {
		t.Errorf("a cmd/pig built with every strip tag links %s", zstd)
	}
}
