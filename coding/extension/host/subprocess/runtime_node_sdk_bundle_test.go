package subprocess_test

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNodeSDKBundleRegeneratesExactly(t *testing.T) {
	t.Parallel()
	shims, err := filepath.Abs(filepath.Join("runtime-node", "shims"))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "bundle")
	command := exec.CommandContext(t.Context(), "node", filepath.Join("..", "..", "..", "..", "automation", "gen", "bundle-pi-sdk.mjs"), shims, output)
	if log, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bundle: %v\n%s", err, log)
	}
	want := bundleDigests(t, output)
	for _, entry := range []string{"index.js", "sdk.js", "tools.js", "inputs.json"} {
		if _, ok := want[entry]; !ok {
			t.Fatalf("compiler did not produce %s", entry)
		}
	}
	got := bundleDigests(t, filepath.Join(shims, "pi-dist", "pi-coding-agent", "sdk-bundle"))
	if !reflect.DeepEqual(got, want) {
		t.Fatal("SDK bundle differs from its pinned module graph: run automation/gen/vendor-pi-dist.sh")
	}
}

func TestNodeLibraryBundlesRegenerateExactly(t *testing.T) {
	t.Parallel()
	shims, err := filepath.Abs(filepath.Join("runtime-node", "shims"))
	if err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	command := exec.CommandContext(t.Context(), "node", filepath.Join("..", "..", "..", "..", "automation", "gen", "bundle-pi-libraries.mjs"), shims, output)
	if log, err := command.CombinedOutput(); err != nil {
		t.Fatalf("library bundles: %v\n%s", err, log)
	}
	for _, pkg := range []string{"pi-ai", "pi-tui"} {
		got := bundleDigests(t, filepath.Join(shims, "pi-dist", pkg, "sdk-bundle"))
		want := bundleDigests(t, filepath.Join(output, pkg))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s bundle differs from its source graph: run automation/gen/vendor-pi-dist.sh", pkg)
		}
	}
}

func bundleDigests(t *testing.T, root string) map[string][sha256.Size]byte {
	t.Helper()
	result := make(map[string][sha256.Size]byte)
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		result[rel] = sha256.Sum256(data)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
