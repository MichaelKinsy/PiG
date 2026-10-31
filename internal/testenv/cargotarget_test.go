// SPDX-License-Identifier: MIT

package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

func cargoRoot(t *testing.T, lock string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{"Cargo.lock": lock, "Cargo.toml": "[package]\nname = \"x\"\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, []string{filepath.Join(root, "Cargo.lock"), filepath.Join(root, "Cargo.toml")}
}

// rustc bakes env!("CARGO_MANIFEST_DIR") into a cached test binary, so two checkouts with the same lock and manifest must not share a target.
func TestSharedCargoTargetDirIsKeyedByTheAbsoluteRootAsWellAsTheContents(t *testing.T) {
	t.Setenv("PIG_TEST_CARGO_CACHE", t.TempDir())
	rootA, inputsA := cargoRoot(t, "version = 4\n")
	rootB, inputsB := cargoRoot(t, "version = 4\n")
	a, err := SharedCargoTargetDir("pkg", inputsA...)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SharedCargoTargetDir("pkg", inputsB...)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("roots %s and %s with identical lock contents share the target %s", rootA, rootB, a)
	}
	again, err := SharedCargoTargetDir("pkg", inputsA...)
	if err != nil {
		t.Fatal(err)
	}
	if again != a {
		t.Fatalf("the same root's target changed: %s then %s", a, again)
	}
	if err := os.WriteFile(inputsA[0], []byte("version = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := SharedCargoTargetDir("pkg", inputsA...)
	if err != nil {
		t.Fatal(err)
	}
	if changed == a {
		t.Fatalf("a changed lock kept the target %s", a)
	}
	other, err := SharedCargoTargetDir("other", inputsA...)
	if err != nil {
		t.Fatal(err)
	}
	if other == changed {
		t.Fatalf("two packages share the target %s", other)
	}
}

func TestSharedCargoTargetDirResolvesARelativeRootToTheSameTarget(t *testing.T) {
	t.Setenv("PIG_TEST_CARGO_CACHE", t.TempDir())
	root, inputs := cargoRoot(t, "version = 4\n")
	absolute, err := SharedCargoTargetDir("pkg", inputs...)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	relative, err := SharedCargoTargetDir("pkg", "Cargo.lock", "Cargo.toml")
	if err != nil {
		t.Fatal(err)
	}
	if relative != absolute {
		t.Fatalf("relative inputs selected %s, absolute %s", relative, absolute)
	}
}
