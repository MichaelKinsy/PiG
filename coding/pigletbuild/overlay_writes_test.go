package pigletbuild

import (
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Trust-split spec test 8 (docs/specs/extension-factory-trust.md): a build overlay for a Piglet with a fused member and a frontend member writes
// exactly the two generated registries (the fuse registry and the frontend registry) beside the module files (go.mod, go.sum, go.work.sum) and the
// baked Piglet data; nothing else reads as replaced, and the Pig source tree itself is never written.
func TestWriteBuildOverlayForAFusedAndAFrontendMemberReplacesExactlyTheRegistriesAndModuleFiles(t *testing.T) {
	f := newFusedModuleFixture(t, true)
	fused := f.entry()
	frontend := &fusedEntry{
		Name: "frontend", Pkg: "frontendmember", Factory: "Frontend", Root: f.member,
		ModulePath: "example.com/extensions", Package: "example.com/extensions/review",
	}
	before := snapshotTree(t, f.root)

	baked := []byte("name: overlay-test\n")
	path, err := writeBuildOverlay(t.TempDir(), f.root, nil, []fusedEntry{fused}, frontend, nil, Options{BakedSettings: baked}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var overlay struct{ Replace map[string]string }
	if err := json.Unmarshal(data, &overlay); err != nil {
		t.Fatal(err)
	}

	got := slices.Sorted(maps.Keys(overlay.Replace))
	var want []string
	for _, rel := range []string{
		"go.mod", "go.sum", "go.work.sum",
		filepath.Join("coding", "extension", "host", "fusepack", "registry_generated.go"),
		filepath.Join("internal", "frontendpack", "registry_generated.go"),
		filepath.Join(bakedPigletDir, "piglet.yaml"),
	} {
		want = append(want, filepath.Join(f.root, rel))
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("overlay replaces %q, want exactly %q", got, want)
	}

	read := func(rel string) string {
		t.Helper()
		content, err := os.ReadFile(overlay.Replace[filepath.Join(f.root, rel)])
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	if registry := read(filepath.Join("coding", "extension", "host", "fusepack", "registry_generated.go")); registry != renderFuseRegistry([]fusedEntry{fused}) {
		t.Fatalf("fuse registry = %q", registry)
	}
	if registry := read(filepath.Join("internal", "frontendpack", "registry_generated.go")); registry != renderFrontendRegistry(*frontend) {
		t.Fatalf("frontend registry = %q", registry)
	}
	if got := read(filepath.Join(bakedPigletDir, "piglet.yaml")); got != string(baked) {
		t.Fatalf("baked data = %q", got)
	}
	if goMod := read("go.mod"); !strings.Contains(goMod, "example.com/extensions") {
		t.Fatalf("go.mod overlay lacks the fused member's module:\n%s", goMod)
	}
	if after := snapshotTree(t, f.root); !maps.Equal(before, after) {
		t.Fatal("writing the overlay changed the Pig source tree")
	}
}
