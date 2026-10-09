package codingagent

import (
	"path/filepath"
	"testing"
)

// config.ts:393-405 getPackageDir: PI_PACKAGE_DIR overrides the package directory, with a leading ~ expanded (normalizePath); without it a compiled binary's directory is the package directory. PIG_PACKAGE_DIR is the PiG spelling and wins.
func TestGetPackageDirHonorsTheEnvironmentOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_PACKAGE_DIR", "")
	t.Setenv("PI_PACKAGE_DIR", "")
	executable := GetPackageDir()
	if executable == "" {
		t.Fatal("a compiled binary has a package directory")
	}
	t.Setenv("PI_PACKAGE_DIR", "~/pi-store/pkg")
	if got, want := GetPackageDir(), filepath.Join(home, "pi-store", "pkg"); got != want {
		t.Fatalf("PI_PACKAGE_DIR = %q, want %q", got, want)
	}
	explicit := filepath.Join(t.TempDir(), "pig-pkg")
	t.Setenv("PIG_PACKAGE_DIR", explicit)
	if got := GetPackageDir(); got != explicit {
		t.Fatalf("PIG_PACKAGE_DIR = %q, want %q over PI_PACKAGE_DIR", got, explicit)
	}
	t.Setenv("PIG_PACKAGE_DIR", "")
	t.Setenv("PI_PACKAGE_DIR", "")
	if got := GetPackageDir(); got != executable {
		t.Fatalf("without an override = %q, want %q", got, executable)
	}
}
