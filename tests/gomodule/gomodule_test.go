// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package gomodule guards the public Go module contract that makes
// `go install github.com/MichaelKinsy/PiG/cmd/pig@vX.Y.Z` work.
//
// Go refuses `go install pkg@version` for a module whose go.mod carries a
// replace or exclude directive, so the root module requires every nested PiG
// module it imports at the release version and resolves it locally only
// through go.work. The release tags each such nested module
// (<dir>/vX.Y.Z) on the same commit as the root tag (vX.Y.Z); see
// docs/project/RELEASING.md and automation/release/module-tags.sh.
package gomodule

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"

	"github.com/MichaelKinsy/PiG/coding/pigversion"
)

const rootModule = "github.com/MichaelKinsy/PiG"

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
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

func TestRootModuleIsGoInstallable(t *testing.T) {
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
	// The release commit requires each nested module at its own version. A
	// nested module that changes after that release's tag is required at the
	// next patch version instead, so the published tag's go.sum hash never
	// changes; the next release commit moves PigVersion to that version.
	want := "v" + pigversion.PigVersion
	next := nextPatchVersion(t, want)
	work := parseWork(t, root)
	for dir, mod := range nested {
		if mod.Version != want && mod.Version != next {
			t.Errorf("root go.mod requires %s %s; want %s (the release tags %s/%s on the root release commit) or, after the module changes, %s", mod.Path, mod.Version, want, dir, want, next)
		}
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

// nextPatchVersion returns vMAJOR.MINOR.PATCH+1 for vMAJOR.MINOR.PATCH.
func nextPatchVersion(t *testing.T, version string) string {
	t.Helper()
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		t.Fatalf("PigVersion %s is not MAJOR.MINOR.PATCH", version)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		t.Fatalf("PigVersion %s has a non-numeric patch: %v", version, err)
	}
	return "v" + parts[0] + "." + parts[1] + "." + strconv.Itoa(patch+1)
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

// TestRootGoSumPinsNestedModuleHashes proves the go.sum lines for each nested
// PiG module equal the hashes the Go checksum database will record once the
// module is tagged: the h1 hash of the module zip built from the directory's
// tracked files under module@version. With the lines present, builds with
// GOWORK=off verify the published module instead of failing with a missing
// go.sum entry. A change under a nested module directory changes its hash;
// update go.sum with the lines this test prints.
func TestRootGoSumPinsNestedModuleHashes(t *testing.T) {
	root := repoRoot(t)
	sum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]bool{}
	for line := range strings.Lines(string(sum)) {
		lines[strings.TrimSpace(line)] = true
	}
	for dir, mod := range nestedRequirements(t, root) {
		zipHash, modHash := moduleHashes(t, root, dir, mod)
		for _, want := range []string{
			mod.Path + " " + mod.Version + " " + zipHash,
			mod.Path + " " + mod.Version + "/go.mod " + modHash,
		} {
			if !lines[want] {
				t.Errorf("go.sum is missing or stale for %s; want the line:\n%s", dir, want)
			}
		}
	}
}

type diskFile struct {
	rel, abs string
}

func (f diskFile) Path() string                 { return f.rel }
func (f diskFile) Lstat() (fs.FileInfo, error)  { return os.Lstat(f.abs) }
func (f diskFile) Open() (io.ReadCloser, error) { return os.Open(f.abs) }

func moduleHashes(t *testing.T, root, dir string, mod module.Version) (string, string) {
	t.Helper()
	moduleDir := filepath.Join(root, filepath.FromSlash(dir))
	var files []modzip.File
	for _, rel := range trackedFiles(t, moduleDir) {
		files = append(files, diskFile{rel: rel, abs: filepath.Join(moduleDir, filepath.FromSlash(rel))})
	}
	zipPath := filepath.Join(t.TempDir(), "module.zip")
	out, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := modzip.Create(out, mod, files); err != nil {
		t.Fatalf("build %s module zip: %v", dir, err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	zipHash, err := dirhash.HashZip(zipPath, dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	goMod, err := os.ReadFile(filepath.Join(moduleDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	modHash, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(goMod)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return zipHash, modHash
}

// trackedFiles lists the module directory's files relative to it: the
// Git-tracked ones when a checkout is available (what a tag publishes),
// otherwise every file on disk (a source archive is already tracked-only).
func trackedFiles(t *testing.T, moduleDir string) []string {
	t.Helper()
	if _, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command("git", "-C", moduleDir, "ls-files", "-z", "--", ".")
		if out, err := cmd.Output(); err == nil && len(out) > 0 {
			var files []string
			for rel := range strings.SplitSeq(strings.TrimRight(string(out), "\x00"), "\x00") {
				files = append(files, rel)
			}
			return files
		}
	}
	var files []string
	err := filepath.WalkDir(moduleDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(moduleDir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestModuleTagsScriptListsEveryReleaseTag runs the release's tag planner
// against the real go.mod and against a go.mod that go install would refuse.
func TestModuleTagsScriptListsEveryReleaseTag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("module-tags.sh runs on the Linux release runner")
	}
	root := repoRoot(t)
	script := filepath.Join(root, "automation", "release", "module-tags.sh")
	// The version the next release commit tags: PigVersion on a release
	// commit, the next patch version after a nested module changed.
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

// TestReleaseWorkflowTagsNestedModulesOnTheReleaseCommit keeps the release
// workflow validating the module graph and tagging nested modules on
// $GITHUB_SHA, the same commit the draft release tags vVERSION.
func TestReleaseWorkflowTagsNestedModulesOnTheReleaseCommit(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release-candidate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	if got := strings.Count(workflow, `automation/release/module-tags.sh "$VERSION" go.mod`); got != 2 {
		t.Fatalf("release-candidate.yml runs module-tags.sh %d times, want 2 (source validation and publish tagging)", got)
	}
	tagStep := strings.Index(workflow, "name: Tag the nested Go modules on the release commit")
	release := strings.Index(workflow, "name: Create the draft GitHub Release")
	if tagStep < 0 || release < 0 || tagStep > release {
		t.Fatal("release-candidate.yml must tag the nested Go modules before it creates the draft release")
	}
	for _, want := range []string{`-f object="$GITHUB_SHA"`, `--target "$GITHUB_SHA"`} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release-candidate.yml is missing %q", want)
		}
	}
}
