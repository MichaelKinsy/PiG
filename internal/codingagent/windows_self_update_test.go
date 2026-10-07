package codingagent

import (
	"os"
	"path/filepath"
	"runtime"
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

	if err := quarantineNativeDependencies(t.Context(), packageDir, loaded); err != nil {
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
	if err := quarantineNativeDependencies(t.Context(), packageDir, []string{image}); err != nil {
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

// npm nests the platform package that holds pig.exe inside the launcher
// package PackageName, and an update replaces the launcher's whole tree, so
// the quarantine lives in the node_modules that holds the installed launcher,
// as upstream's lives in the node_modules that holds its installed package
// (windows-self-update.ts getQuarantineRoot from getPackageDir). Hoisted
// (yarn, bun, a local npm install) and pnpm layouts already keep the nearest
// node_modules outside every package the update replaces.
func TestQuarantineRootIsBesideTheInstalledLauncher(t *testing.T) {
	top := filepath.Join(t.TempDir(), "node_modules")
	launcher := filepath.Join(top, filepath.FromSlash(PackageName))
	platform := filepath.FromSlash(PackageName + "-win32-x64")
	store := filepath.Join(top, ".pnpm", strings.ReplaceAll(PackageName, "/", "+")+"-win32-x64@1.0.0", "node_modules")
	cases := map[string]struct{ packageDir, want string }{
		"npm nests the platform package in the launcher": {filepath.Join(launcher, "node_modules", platform), top},
		"hoisted beside the launcher":                    {filepath.Join(top, platform), top},
		"pnpm virtual store":                             {filepath.Join(store, platform), store},
		"unscoped package with a bin directory":          {filepath.Join(top, "pig", "bin"), top},
		"another package's nested dependency":            {filepath.Join(top, "other", "node_modules", "dep"), filepath.Join(top, "other", "node_modules")},
	}
	if runtime.GOOS == "windows" {
		// Windows paths compare without case, and quarantineNativeDependencies
		// passes the namespaced form normalizeWindowsPath returns.
		upper := strings.ToUpper(top)
		cases["npm nest spelled in another case"] = struct{ packageDir, want string }{filepath.Join(upper, strings.ToUpper(filepath.FromSlash(PackageName)), "Node_Modules", platform), upper}
		cases["npm nest as a namespaced path"] = struct{ packageDir, want string }{`\\?\` + filepath.Join(launcher, "node_modules", platform), `\\?\` + top}
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := getQuarantineRoot(tc.packageDir)
			if want := filepath.Join(tc.want, quarantineDirName); !ok || got != want {
				t.Fatalf("getQuarantineRoot(%s) = %q, %v; want %q", tc.packageDir, got, ok, want)
			}
		})
	}
}
