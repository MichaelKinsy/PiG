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
//
// A module that has a go.sum and requires another PiG module must record that module's checksums, because a build outside the workspace (GOWORK=off) downloads the requirement and Go refuses a download that has no go.sum entry. A requirement that a `replace` directive points at a local directory is not downloaded, and `go mod tidy` removes its sums, so it needs none.
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
		if sumErr != nil {
			continue
		}
		for _, missing := range missingChecksums(file, sum, modules) {
			t.Errorf("%s missing checksum for %s", sumPath, missing)
		}
	}
}

// missingChecksums returns the go.sum entries (a module version, and the same with "/go.mod") that file requires of a local PiG module and sum lacks. The root module is skipped, and so is a requirement that file replaces with a local directory.
func missingChecksums(file *modfile.File, sum []byte, local map[string]string) []string {
	var missing []string
	for _, req := range file.Require {
		dir, ok := local[req.Mod.Path]
		if !ok || dir == "." || replacedByDirectory(file, req.Mod) {
			continue
		}
		for _, suffix := range []string{"", "/go.mod"} {
			prefix := req.Mod.Path + " " + req.Mod.Version + suffix + " h1:"
			if !strings.Contains("\n"+string(sum), "\n"+prefix) {
				missing = append(missing, req.Mod.String()+suffix)
			}
		}
	}
	return missing
}

// replacedByDirectory reports whether file has a replace directive that sends mod to a local directory, for every version or for mod's version.
func replacedByDirectory(file *modfile.File, mod module.Version) bool {
	for _, r := range file.Replace {
		if r.Old.Path == mod.Path && (r.Old.Version == "" || r.Old.Version == mod.Version) && modfile.IsDirectoryPath(r.New.Path) {
			return true
		}
	}
	return false
}

// The sum check keeps its force: a module without a replace needs the sums, and only a replace to a local directory excuses them.
func TestMissingChecksumsNeedsSumsUnlessReplacedByDirectory(t *testing.T) {
	const sdk = rootModule + "/extensions/sdk"
	local := map[string]string{rootModule: ".", sdk: "extensions/sdk"}
	parse := func(goMod string) *modfile.File {
		file, err := modfile.Parse("go.mod", []byte(goMod), nil)
		if err != nil {
			t.Fatal(err)
		}
		return file
	}
	const header = "module example.com/m\n\ngo 1.26.0\n\nrequire " + sdk + " v0.7.0\n"
	const sums = sdk + " v0.7.0 h1:a=\n" + sdk + " v0.7.0/go.mod h1:b=\n"
	for _, tc := range []struct {
		name, goMod, sum string
		want             int
	}{
		{"no replace, sums present", header, sums, 0},
		{"no replace, no sums", header, "", 2},
		{"no replace, go.mod sum only", header, sdk + " v0.7.0/go.mod h1:b=\n", 1},
		{"no replace, other version summed", header, sdk + " v0.6.0 h1:a=\n" + sdk + " v0.6.0/go.mod h1:b=\n", 2},
		{"directory replace, no sums", header + "replace " + sdk + " => ../sdk\n", "", 0},
		{"directory replace for the version, no sums", header + "replace " + sdk + " v0.7.0 => ../sdk\n", "", 0},
		{"directory replace for another version, no sums", header + "replace " + sdk + " v0.6.0 => ../sdk\n", "", 2},
		{"replace by a module, no sums", header + "replace " + sdk + " => example.com/fork v1.0.0\n", "", 2},
		{"the root module needs none", "module example.com/m\n\ngo 1.26.0\n\nrequire " + rootModule + " v0.7.0\n", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := missingChecksums(parse(tc.goMod), []byte(tc.sum), local); len(got) != tc.want {
				t.Fatalf("missing checksums = %v, want %d", got, tc.want)
			}
		})
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
	if want := pigversion.PigVersion; manifest.Release.Version != want {
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

// Between releases main requires extensions/sdk at the pseudo-version of a main commit. The release plan must refuse that pin for any release
// until make set-version --release-modules replaces it with the release tag, so a release cannot publish a root module that requires a commit.
func TestModuleTagsScriptRefusesAPseudoVersionPin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("module-tags.sh runs on the Linux release runner")
	}
	root := repoRoot(t)
	script := filepath.Join(root, "automation", "release", "module-tags.sh")
	dir := t.TempDir()
	const pin = "v0.3.1-0.20261020232631-3ee123c6d5f4"
	goMod := "module " + rootModule + "\n\ngo 1.26.0\n\nrequire " + rootModule + "/extensions/sdk " + pin + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "extensions", "sdk"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extensions", "sdk", "go.mod"), []byte("module "+rootModule+"/extensions/sdk\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", script, "0.5.2", filepath.Join(dir, "go.mod")).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "the release commit must require v0.5.2") {
		t.Fatalf("module-tags.sh 0.5.2 did not refuse a pseudo-version pin: %v\n%s", err, out)
	}
}
