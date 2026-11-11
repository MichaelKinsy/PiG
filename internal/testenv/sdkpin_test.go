package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootSDKRequirementReadsTheRootGoMod(t *testing.T) {
	root := t.TempDir()
	const pin = "v0.7.1-0.20261108053302-4e194449dcd7"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/MichaelKinsy/PiG\n\ngo 1.27.0\n\nrequire "+rootSDKModulePath+" "+pin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := RootSDKRequirement(root)
	if err != nil || got != pin {
		t.Fatalf("RootSDKRequirement = %q, %v; want %q", got, err, pin)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/MichaelKinsy/PiG\n\ngo 1.27.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := RootSDKRequirement(root); err == nil {
		t.Fatalf("RootSDKRequirement of a root without the requirement = %q, want an error", got)
	}
}

// The repository's own root: the helper finds the requirement the in-tree modules are pinned to.
func TestRootSDKRequirementOfTheRepository(t *testing.T) {
	if got, err := RootSDKRequirement(filepath.Join("..", "..")); err != nil || got == "" {
		t.Fatalf("the repository root go.mod: %q, %v", got, err)
	}
}
