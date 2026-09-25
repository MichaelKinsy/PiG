package runtimecell

import (
	"path/filepath"
	"testing"
)

func TestCargoTargetDirectoryHonorsAbsoluteAndRelativeOverrides(t *testing.T) {
	workingDirectory := t.TempDir()
	absolute := filepath.Join(t.TempDir(), "cargo-target")
	t.Setenv("CARGO_TARGET_DIR", absolute)
	if got := cargoTargetDirectory(workingDirectory); got != absolute {
		t.Fatalf("absolute target = %q, want %q", got, absolute)
	}

	t.Setenv("CARGO_TARGET_DIR", "shared-target")
	want := filepath.Join(workingDirectory, "shared-target")
	if got := cargoTargetDirectory(workingDirectory); got != want {
		t.Fatalf("relative target = %q, want %q", got, want)
	}
}
