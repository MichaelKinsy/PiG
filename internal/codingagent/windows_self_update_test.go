package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ports utils/windows-self-update.ts: each loaded image inside the package
// directory moves into a run directory under node_modules/.pig-native-quarantine
// at its package-relative path and is copied back; images outside the package,
// and a second spelling of one image, are left alone. The next start removes
// the quarantine.
func TestQuarantineNativeDependenciesMovesLoadedImagesAndCopiesThemBack(t *testing.T) {
	root := filepath.Join(t.TempDir(), "node_modules")
	packageDir := filepath.Join(root, "pig")
	image := filepath.Join(packageDir, "bin", "pig.exe")
	outside := filepath.Join(root, "other", "lib.dll")
	for path, content := range map[string]string{image: "running pig", outside: "other package"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	loaded := []string{image, outside, filepath.Join(packageDir, "bin", "missing.dll")}
	if filepath.Separator == '\\' {
		loaded = append(loaded, strings.ToUpper(image))
	}

	if err := quarantineNativeDependencies(packageDir, loaded); err != nil {
		t.Fatal(err)
	}
	quarantineRoot := filepath.Join(root, quarantineDirName)
	quarantined, err := filepath.Glob(filepath.Join(quarantineRoot, "*", "bin", "*"))
	if err != nil || len(quarantined) != 1 || filepath.Base(quarantined[0]) != "pig.exe" {
		t.Fatalf("quarantined = %v (err=%v), want only bin/pig.exe", quarantined, err)
	}
	for _, path := range []string{image, quarantined[0]} {
		if got, err := os.ReadFile(path); err != nil || string(got) != "running pig" {
			t.Fatalf("%s = %q (err=%v), want the loaded image", path, got, err)
		}
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "other package" {
		t.Fatalf("image outside the package changed: %q (err=%v)", got, err)
	}

	CleanupWindowsSelfUpdateQuarantine(filepath.Dir(image))
	if _, err := os.Stat(quarantineRoot); !os.IsNotExist(err) {
		t.Fatalf("quarantine after cleanup: %v, want it removed", err)
	}
}

// Outside a node_modules tree there is no quarantine: nothing moves.
func TestQuarantineNativeDependenciesOutsideNodeModulesIsANoOp(t *testing.T) {
	packageDir := t.TempDir()
	image := filepath.Join(packageDir, "pig.exe")
	if err := os.WriteFile(image, []byte("running pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := quarantineNativeDependencies(packageDir, []string{image}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(packageDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("package dir entries = %v (err=%v), want only pig.exe", entries, err)
	}
	if _, ok := getQuarantineRoot(""); ok {
		t.Fatal("an unknown package directory has a quarantine root")
	}
}
