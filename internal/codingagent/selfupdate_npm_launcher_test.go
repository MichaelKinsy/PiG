package codingagent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const npmLauncherPackage = "@pi-in-go/pig"

// The npm platform packages, listed independently of the production table:
// automation/release/npm/pack_npm.py TARGETS emits exactly these names.
var npmPlatformPackages = []string{
	"@pi-in-go/pig-android-arm64",
	"@pi-in-go/pig-darwin-arm64",
	"@pi-in-go/pig-darwin-x64",
	"@pi-in-go/pig-linux-arm64",
	"@pi-in-go/pig-linux-x64",
	"@pi-in-go/pig-win32-arm64",
	"@pi-in-go/pig-win32-x64",
}

// PackageName is Pi's PACKAGE_NAME (config.ts): the npm package a global
// install owns and the release manifest names. PiG's npm package is the
// scoped launcher, not the unrelated unscoped "pig" package.
func TestPackageNameIsTheNpmLauncherPackage(t *testing.T) {
	if PackageName != npmLauncherPackage {
		t.Fatalf("PackageName = %q, want %q", PackageName, npmLauncherPackage)
	}
}

// One source of truth: pack_npm.py names the launcher and every platform
// package from the same value PackageName holds.
func TestPackNpmNamesMatchPackageName(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	dir := filepath.Join("..", "..", "automation", "release", "npm")
	script := "import json, pack_npm\n" +
		"print(json.dumps({'launcher': pack_npm.LAUNCHER, 'platforms': sorted(pack_npm.package_name(o, c) for o, c in pack_npm.TARGETS.values())}))\n"
	cmd := exec.Command(python, "-c", script)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("read pack_npm names: %v", err)
	}
	var got struct {
		Launcher  string   `json:"launcher"`
		Platforms []string `json:"platforms"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Launcher != PackageName {
		t.Fatalf("pack_npm launcher = %q, PackageName = %q", got.Launcher, PackageName)
	}
	if strings.Join(got.Platforms, ",") != strings.Join(npmPlatformPackages, ",") {
		t.Fatalf("pack_npm platform packages = %v, want %v", got.Platforms, npmPlatformPackages)
	}
}

// The signed manifest names the npm package by default, so a release built
// without --package-name cannot direct `npm install -g` at another package.
func TestUpdateManifestScriptDefaultsToNpmLauncherPackage(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	dir := t.TempDir()
	name := "pig-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("pig-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join("..", "..", "automation", "release", "gen-update-manifest.py")
	out, err := exec.Command(python, script, "--version", "1.2.3", "--base-url", "https://updates.example", "--dir", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("generate manifest: %v\n%s", err, out)
	}
	var manifest UpdateManifest
	if err := json.Unmarshal(out, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.PackageName != npmLauncherPackage {
		t.Fatalf("manifest packageName = %q, want %q", manifest.PackageName, npmLauncherPackage)
	}
	if manifest.PackageName != PackageName {
		t.Fatalf("manifest packageName %q differs from PackageName %q", manifest.PackageName, PackageName)
	}
}

// npm installs the launcher globally and its platform package beneath it (npm),
// beside it (yarn/bun hoisting), or in the virtual store (pnpm). Every layout
// is owned by the launcher, never by the platform package that holds the binary.
func TestOwningPackageOfNpmPlatformBinaryIsTheLauncher(t *testing.T) {
	type layout struct {
		name string
		goos string
		// exe builds the executable path for a platform package "@pi-in-go/pig-<os>-<cpu>".
		exe func(platform string) string
		// dir is the launcher package directory the manager replaces.
		dir string
	}
	layouts := []layout{
		{"npm nested", "linux", func(p string) string {
			return "/home/u/.npm/lib/node_modules/@pi-in-go/pig/node_modules/" + p + "/pig"
		}, "/home/u/.npm/lib/node_modules/@pi-in-go/pig"},
		{"termux npm nested", "android", func(p string) string {
			return "/data/data/com.termux/files/usr/lib/node_modules/@pi-in-go/pig/node_modules/" + p + "/pig"
		}, "/data/data/com.termux/files/usr/lib/node_modules/@pi-in-go/pig"},
		{"hoisted sibling", "linux", func(p string) string {
			return "/home/u/.bun/install/global/node_modules/" + p + "/pig"
		}, "/home/u/.bun/install/global/node_modules/@pi-in-go/pig"},
		{"pnpm virtual store", "linux", func(p string) string {
			return "/home/u/.local/share/pnpm/global/5/node_modules/.pnpm/" + strings.Replace(p, "/", "+", 1) + "@0.3.1/node_modules/" + p + "/pig"
		}, "/home/u/.local/share/pnpm/global/5/node_modules/@pi-in-go/pig"},
		{"windows npm nested", "windows", func(p string) string {
			return `C:\Users\u\AppData\Roaming\npm\node_modules\@pi-in-go\pig\node_modules\` + strings.ReplaceAll(p, "/", `\`) + `\pig.exe`
		}, `C:/Users/u/AppData/Roaming/npm/node_modules/@pi-in-go/pig`},
	}
	for _, l := range layouts {
		for _, platform := range npmPlatformPackages {
			t.Run(l.name+"/"+platform, func(t *testing.T) {
				name, dir := owningPackageFromExecutable(l.goos, l.exe(platform))
				if name != npmLauncherPackage {
					t.Errorf("owning package = %q, want %q", name, npmLauncherPackage)
				}
				if want := filepath.FromSlash(l.dir); dir != want {
					t.Errorf("package dir = %q, want %q", dir, want)
				}
			})
		}
	}
}

// Windows paths compare without case, as ownershipPathLiteral does: a launcher
// directory spelled in another case still owns its nested platform package.
func TestOwningPackageOfNpmPlatformBinaryIgnoresWindowsCase(t *testing.T) {
	exe := `C:\Users\u\AppData\Roaming\npm\NODE_MODULES\@Pi-In-Go\PIG\node_modules\@pi-in-go\pig-win32-x64\pig.exe`
	name, dir := owningPackageFromExecutable("windows", exe)
	if want := filepath.FromSlash("C:/Users/u/AppData/Roaming/npm/NODE_MODULES/@Pi-In-Go/PIG"); name != npmLauncherPackage || dir != want {
		t.Fatalf("owning package = %q in %q, want %q in %q", name, dir, npmLauncherPackage, want)
	}
}

// A package that merely resembles a platform name keeps its own identity, and
// an unrelated nested package keeps the innermost-package rule.
func TestOwningPackageKeepsNonLauncherPackages(t *testing.T) {
	for _, tc := range []struct{ exe, name string }{
		{"/g/node_modules/@pi-in-go/pig-linux-mips/pig", "@pi-in-go/pig-linux-mips"},
		{"/g/node_modules/@other/pig-linux-x64/pig", "@other/pig-linux-x64"},
		{"/g/node_modules/outer/node_modules/inner/bin/pig", "inner"},
	} {
		if name, _ := owningPackageFromExecutable("linux", tc.exe); name != tc.name {
			t.Errorf("%s: owning package = %q, want %q", tc.exe, name, tc.name)
		}
	}
}

// End to end with the real npm layout on disk: ownership detection, then the
// exact command `pig update` hands npm for the signed release.
func TestNpmLauncherLayoutUpdatesTheScopedPackage(t *testing.T) {
	t.Setenv("PIG_INSTALL_TIER", "")
	for _, platform := range npmPlatformPackages {
		t.Run(platform, func(t *testing.T) {
			prefix := t.TempDir()
			root := filepath.Join(prefix, "lib", "node_modules")
			dir := filepath.Join(root, "@pi-in-go", "pig", "node_modules", filepath.FromSlash(platform))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			exe := writeFakeExe(t, dir)
			prov, ambiguous := detectPackageManagerOwnership(runtime.GOOS, exe, fakeCmdRunner{outputs: map[string]string{"npm root -g": root}})
			if ambiguous || prov == nil || prov.PackageOwner != ownerNPM {
				t.Fatalf("ownership=%+v ambiguous=%t", prov, ambiguous)
			}
			if prov.PackageName != npmLauncherPackage {
				t.Fatalf("PackageName = %q, want %q", prov.PackageName, npmLauncherPackage)
			}
			if want := realPathForTest(t, filepath.Join(root, "@pi-in-go", "pig")); realPathForTest(t, prov.PackageDir) != want {
				t.Fatalf("PackageDir = %q, want the launcher directory %q", prov.PackageDir, want)
			}
			command := prov.GetSelfUpdateCommand(nil, SelfUpdatePackageTarget{PackageName: PackageName, InstallSpec: PackageName + "@0.3.1"})
			if command == nil {
				t.Fatal("no update command")
			}
			if len(command.Steps) != 0 {
				t.Fatalf("update uninstalls before installing: %s", command.Display)
			}
			if want := "npm --prefix " + prov.NpmPrefix + " install -g --ignore-scripts --min-release-age=0 @pi-in-go/pig@0.3.1"; prov.NpmPrefix == "" || command.Display != want {
				t.Fatalf("command = %q, want %q", command.Display, want)
			}
		})
	}
}
