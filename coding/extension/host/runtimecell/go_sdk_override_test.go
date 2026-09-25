package runtimecell

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/mod/modfile"
)

// TestGoSDKPathOverrideIgnoresMissingReplace guards the field bug where a packed
// Go cell emitted an SDK replace pointing at a path that only existed on the
// extension author's machine (for example, /Users/example/PiG/extensions/sdk).
// A foreign or stale replace directory must be ignored so findSDKRoot can fall
// back to the staged / source-tree SDK, rather than baked into the generated
// go.mod as a broken replacement directory.
func TestGoSDKPathOverrideIgnoresMissingReplace(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "does", "not", "exist")
	writeGoMod(t, root, "module example.com/ext\n\ngo 1.26\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => "+modfile.AutoQuote(missing)+"\n")

	if got := GoSDKPathOverride(root); got != "" {
		t.Fatalf("override for a non-existent replace path should be ignored, got %q", got)
	}
}

// TestGoSDKPathOverrideResolvesRealReplace proves the happy path still works: a
// relative replace to a directory that actually contains an SDK go.mod resolves
// to its cleaned absolute path.
func TestGoSDKPathOverrideResolvesRealReplace(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "vendor-sdk")
	if err := os.MkdirAll(sdk, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoMod(t, sdk, "module github.com/MichaelKinsy/PiG/extensions/sdk\n\ngo 1.26\n")
	writeGoMod(t, root, "module example.com/ext\n\ngo 1.26\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => ./vendor-sdk\n")

	got := GoSDKPathOverride(root)
	want := filepath.Clean(sdk)
	if got != want {
		t.Fatalf("override should resolve real replace: got %q want %q", got, want)
	}
}

// TestGoSDKPathOverrideNoReplace returns empty when no SDK replace is declared.
func TestGoSDKPathOverrideNoReplace(t *testing.T) {
	root := t.TempDir()
	writeGoMod(t, root, "module example.com/ext\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n")

	if got := GoSDKPathOverride(root); got != "" {
		t.Fatalf("override with no SDK replace should be empty, got %q", got)
	}
}

func writeGoMod(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
