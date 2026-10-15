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

// A fused frontend member's module graph is part of the Binary identity: its
// go.mod and go.sum pin external modules, and the source of a local
// replacement the fused build promotes pins that module.
func TestBuildInputsFrontendDigestCoversItsModuleGraph(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "mine")
	shared := filepath.Join(root, "shared")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(member, "go.mod"), "module example.com/mine\n\ngo 1.26\n\nrequire example.com/dep v1.1.0\n\nreplace example.com/shared => ../shared\n")
	write(filepath.Join(member, "go.sum"), "example.com/dep v1.1.0 h1:a=\n")
	write(filepath.Join(member, "mine.go"), "package mine\n")
	write(filepath.Join(shared, "go.mod"), "module example.com/shared\n\ngo 1.26\n")
	write(filepath.Join(shared, "shared.go"), "package shared\n")
	pigletPath := filepath.Join(root, "piglet.yaml")
	write(pigletPath, "name: release\nslots:\n  frontend:\n    member: ./mine\n")
	parsed, err := piglet.Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	digests := func() map[string]string {
		t.Helper()
		inputs, err := buildInputs(parsed, nil)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, input := range inputs {
			out[input.Kind] = input.Digest
		}
		if out["frontend"] == "" || out["frontend-dependency"] == "" {
			t.Fatalf("frontend inputs missing in %#v", inputs)
		}
		return out
	}
	before := digests()
	write(filepath.Join(member, "go.sum"), "example.com/dep v1.1.0 h1:b=\n")
	afterSum := digests()
	if afterSum["frontend"] == before["frontend"] {
		t.Fatal("a go.sum change kept the frontend digest")
	}
	write(filepath.Join(shared, "shared.go"), "package shared\n\nconst changed = true\n")
	if after := digests(); after["frontend-dependency"] == afterSum["frontend-dependency"] {
		t.Fatal("a local replacement source change kept the frontend dependency digest")
	}
}
