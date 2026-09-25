package piglet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAC2ResolveExtensionsReturnsSelectedOrigin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionDir := filepath.Join(root, "extensions", "review")
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(root, "piglet.yaml")
	data := []byte("name: review\nextensions:\n  - name: review\n    origins:\n      - local:extensions/missing\n      - local:extensions/review\n")
	if err := os.WriteFile(pigletPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	resolved, errs := ResolveExtensions(parsed)
	if len(errs) > 0 {
		t.Fatalf("ResolveExtensions() errors = %v", errs)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved count = %d, want 1", len(resolved))
	}
	if resolved[0].Origin != "local:extensions/review" {
		t.Fatalf("selected origin = %#v", resolved[0].Origin)
	}
	wantPath := canonicalPath(extensionDir)
	if resolved[0].Path != wantPath {
		t.Fatalf("resolved path = %q, want %q", resolved[0].Path, wantPath)
	}
}
