//go:build !pig_strip_extension_sdk_rust

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The scaffold stages the rust SDK, which a build with pig_strip_extension_sdk_rust does not carry.
func TestRunExtensionInitRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)

	dir := filepath.Join(t.TempDir(), "planner")
	if code := runExtensionInit([]string{dir, "--name", "planner", "--lang", "rust", "--json"}); code != 0 {
		t.Fatalf("rust init exit code = %d, want 0", code)
	}

	if _, err := os.Stat(filepath.Join(stagedSDKDir(t, home, "rust"), "Cargo.toml")); err != nil {
		t.Fatalf("rust init did not stage the Rust SDK: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "lib.rs")); err != nil {
		t.Fatalf("missing rust src/lib.rs: %v", err)
	}
	cargo, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cargo), `name = "planner"`) {
		t.Fatalf("Cargo.toml package name should match factory identity:\n%s", cargo)
	}
	if !strings.Contains(string(cargo), `pig-sdk = "0.1.0"`) {
		t.Fatalf("Cargo.toml missing versioned pig-sdk dependency:\n%s", cargo)
	}
	stagedRs := stagedSDKDir(t, home, "rust")
	if strings.Contains(string(cargo), "path =") || strings.Contains(string(cargo), filepath.ToSlash(stagedRs)) {
		t.Fatalf("Cargo.toml committed a machine-specific SDK path:\n%s", cargo)
	}
	assertScaffoldHasNoYAML(t, dir)
}
