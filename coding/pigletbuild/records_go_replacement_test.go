package pigletbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/piglet"
)

// The lock digest of a local Go replacement is the module's Go source set, as `go build` reads it. A checkout used as a replacement (the Porter extension replaces the whole repository) holds build output and documentation that other processes create and delete while the lock is computed; neither belongs in the digest.
func TestBuildInputsLocalGoReplacementDigestIsItsGoSourceSet(t *testing.T) {
	root := t.TempDir()
	extension := filepath.Join(root, "extension")
	replacement := filepath.Join(root, "checkout")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(extension, "go.mod"), "module example.com/ext\n\ngo 1.26\n\nreplace example.com/checkout => ../checkout\n")
	write(filepath.Join(replacement, "go.mod"), "module example.com/checkout\n\ngo 1.26\n")
	write(filepath.Join(replacement, "checkout.go"), "package checkout\n")
	pigletPath := filepath.Join(root, "piglet.yaml")
	write(pigletPath, "name: release\nextensions:\n  - name: ext\n    origins: [local:extension]\n")
	parsed, err := piglet.Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	cells := []subprocess.CellSpec{{Extensions: []subprocess.ExtConfig{{Name: "ext", Source: extension, ContentHash: strings.Repeat("a", 64)}}}}
	digest := func() string {
		t.Helper()
		inputs, err := buildInputs(parsed, cells)
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range inputs {
			if input.Kind == "extension-dependency" {
				return input.Digest
			}
		}
		t.Fatalf("no local Go replacement input in %#v", inputs)
		return ""
	}
	before := digest()
	write(filepath.Join(replacement, "tmp", "test-fixtures", "rust-target", "release", "artifact"), "build output")
	write(filepath.Join(replacement, "docs", "guide.md"), "documentation")
	if after := digest(); after != before {
		t.Fatalf("non-source files changed the replacement digest: %q, want %q", after, before)
	}
	write(filepath.Join(replacement, "checkout.go"), "package checkout\n\nconst changed = true\n")
	if after := digest(); after == before {
		t.Fatal("a Go source change kept the replacement digest")
	}
}
