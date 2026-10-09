// SPDX-License-Identifier: MIT

// Package gomodule guards the public Go module contract. Dependency versions and checksums describe published modules, independently of the PiG development version. Workspace builds use the local SDK; the separate module-publication CI gate verifies that required tags exist remotely.
package gomodule

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"

	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"github.com/MichaelKinsy/PiG/automation/release/modulehash"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

const rootModule = "github.com/MichaelKinsy/PiG"

func repoRoot(t *testing.T) string {
	t.Helper()
	return testenv.ModuleRoot(t)
}

func parseMod(t *testing.T, path string) *modfile.File {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := modfile.Parse(path, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// nestedRequirements returns the root go.mod's requirements on nested PiG
// modules, keyed by their directory relative to the repository root.
func nestedRequirements(t *testing.T, root string) map[string]module.Version {
	t.Helper()
	out := map[string]module.Version{}
	for _, req := range parseMod(t, filepath.Join(root, "go.mod")).Require {
		if dir, ok := strings.CutPrefix(req.Mod.Path, rootModule+"/"); ok {
			out[dir] = req.Mod
		}
	}
	return out
}

func TestRootModuleInstallDirectivesAndWorkspace(t *testing.T) {
	root := repoRoot(t)
	file := parseMod(t, filepath.Join(root, "go.mod"))
	if file.Module == nil || file.Module.Mod.Path != rootModule {
		t.Fatalf("root go.mod module path is not %s", rootModule)
	}
	for _, r := range file.Replace {
		t.Errorf("root go.mod replaces %s => %s; go install %s/cmd/pig@version refuses any replace directive (resolve local modules through go.work instead)", r.Old.Path, r.New.Path, rootModule)
	}
	for _, e := range file.Exclude {
		t.Errorf("root go.mod excludes %s %s; go install pkg@version refuses any exclude directive", e.Mod.Path, e.Mod.Version)
	}

	nested := nestedRequirements(t, root)
	if _, ok := nested["extensions/sdk"]; !ok {
		t.Fatalf("root go.mod does not require %s/extensions/sdk", rootModule)
	}
	work := parseWork(t, root)
	for dir, mod := range nested {
		nestedMod := parseMod(t, filepath.Join(root, filepath.FromSlash(dir), "go.mod"))
		if nestedMod.Module == nil || nestedMod.Module.Mod.Path != mod.Path {
			t.Errorf("%s/go.mod does not declare module %s", dir, mod.Path)
		}
		if len(nestedMod.Replace) > 0 {
			t.Errorf("%s/go.mod has replace directives; a published nested module must not", dir)
		}
		if !work["./"+dir] {
			t.Errorf("go.work does not use ./%s; local builds would try to download %s %s", dir, mod.Path, mod.Version)
		}
	}
	if !work["."] {
		t.Error("go.work does not use the root module")
	}
}

func parseWork(t *testing.T, root string) map[string]bool {
	t.Helper()
	path := filepath.Join(root, "go.work")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	work, err := modfile.ParseWork(path, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	uses := map[string]bool{}
	for _, use := range work.Use {
		uses[filepath.ToSlash(use.Path)] = true
	}
	return uses
}

// TestLocalModuleRequirementsHaveChecksums checks the committed dependency records, not hashes of the developing SDK. Release preparation hashes are tested by set-version and modulehash; published bytes must never be rehashed from a newer working tree.
func TestLocalModuleRequirementsHaveChecksums(t *testing.T) {
	root := repoRoot(t)
	paths, err := modulehash.TrackedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	modules := map[string]string{}
	files := map[string]*modfile.File{}
	for _, path := range paths {
		if filepath.Base(path) != "go.mod" {
			continue
		}
		file := parseMod(t, filepath.Join(root, filepath.FromSlash(path)))
		files[path] = file
		if file.Module != nil {
			modules[file.Module.Mod.Path] = filepath.ToSlash(filepath.Dir(path))
		}
	}
	for path, file := range files {
		sumPath := strings.TrimSuffix(path, "go.mod") + "go.sum"
		sum, sumErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(sumPath)))
		if sumErr != nil && !os.IsNotExist(sumErr) {
			t.Fatal(sumErr)
		}
		for _, req := range file.Require {
			dir, local := modules[req.Mod.Path]
			if !local {
				continue
			}
			if dir == "." || sumErr != nil {
				continue
			}
			for _, suffix := range []string{"", "/go.mod"} {
				prefix := req.Mod.Path + " " + req.Mod.Version + suffix + " h1:"
				if !strings.Contains("\n"+string(sum), "\n"+prefix) {
					t.Errorf("%s missing checksum for %s%s", sumPath, req.Mod, suffix)
				}
			}
		}
	}
}

func TestStandardReleaseUsesPigVersion(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "piglets", "standard", "pig-standard.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Release struct {
			Version string `yaml:"version"`
		} `yaml:"release"`
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if want := pigversion.PigVersion + "-dev"; manifest.Release.Version != want {
		t.Fatalf("Standard release.version = %q, want %q; run make set-version VERSION=%s", manifest.Release.Version, want, pigversion.PigVersion)
	}
}

// TestModuleTagsScriptListsEveryReleaseTag runs the release's tag planner
// against the real go.mod and against a go.mod that go install would refuse.
func TestModuleTagsScriptListsEveryReleaseTag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("module-tags.sh runs on the Linux release runner")
	}
	root := repoRoot(t)
	script := filepath.Join(root, "automation", "release", "module-tags.sh")
	version := strings.TrimPrefix(nestedRequirements(t, root)["extensions/sdk"].Version, "v")
	out, err := exec.Command("bash", script, version, filepath.Join(root, "go.mod")).CombinedOutput()
	if err != nil {
		t.Fatalf("module-tags.sh %s: %v\n%s", version, err, out)
	}
	want := []string{"v" + version}
	for dir := range nestedRequirements(t, root) {
		want = append(want, dir+"/v"+version)
	}
	got := strings.Fields(string(out))
	if got[0] != want[0] || len(got) != len(want) {
		t.Fatalf("module-tags.sh printed %q, want the root tag first and %q", got, want)
	}
	for _, tag := range want {
		if !strings.Contains("\n"+string(out), "\n"+tag+"\n") {
			t.Errorf("module-tags.sh output is missing %s:\n%s", tag, out)
		}
	}

	if out, err := exec.Command("bash", script, "9.9.9", filepath.Join(root, "go.mod")).CombinedOutput(); err == nil {
		t.Errorf("module-tags.sh accepted a version the nested requirements do not match:\n%s", out)
	}

	dir := t.TempDir()
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	replaced := string(goMod) + "\nreplace " + rootModule + "/extensions/sdk => ./extensions/sdk\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(replaced), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = exec.Command("bash", script, version, filepath.Join(dir, "go.mod")).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "replace or exclude") {
		t.Errorf("module-tags.sh accepted a go.mod with a replace directive: %v\n%s", err, out)
	}
}
