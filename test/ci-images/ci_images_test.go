package ciimages

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// hostPython is the interpreter PiG itself launches: python3, or on Windows
// the python.exe that actions/setup-python installs and no python3.
func hostPython() string {
	return toolchain.PythonExecutable(runtime.GOOS, exec.LookPath)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	return testenv.ModuleRoot(t)
}

// repoScript returns the path of a repository script the test runs in place. It reads the script first: the go test cache keys a result only on files the test process opens, and the child process that runs the script is invisible to it, so without the read an edited script would replay the earlier pass.
func repoScript(t *testing.T, path string) string {
	t.Helper()
	script := filepath.Join(repoRoot(t), filepath.FromSlash(path))
	if _, err := os.ReadFile(script); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestNpmRuntimeRemovesAdvisoryOverridesFromBundledNpm(t *testing.T) {
	root := repoRoot(t)
	manifestData, err := os.ReadFile(filepath.Join(root, "automation", "images", "npm-runtime", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Overrides map[string]string `json:"overrides"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Overrides) == 0 {
		t.Fatal("npm runtime declares no advisory overrides")
	}
	dockerfile, err := os.ReadFile(filepath.Join(root, "automation", "images", "ci-parity", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	for dependency := range manifest.Overrides {
		bundledPath := "/opt/pig/npm/node_modules/npm/node_modules/" + dependency
		if !strings.Contains(string(dockerfile), bundledPath) {
			t.Errorf("parity image does not remove npm's bundled %s", dependency)
		}
	}
}

// npm's tarball bundles its dependencies, and a lockfile cannot replace a bundled copy. The toolchain and the
// parity image remove npm's bundled http-cache-semantics and postcss-selector-parser, so neither lock describes those
// bundled copies, and each lock pins the patched copy npm then resolves.
func TestNpmLocksPinPatchedCopiesInsteadOfBundledOnes(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range []string{
		filepath.Join("automation", "ci", "npm-toolchain"),
		filepath.Join("automation", "images", "npm-runtime"),
	} {
		var manifest struct {
			Dependencies map[string]string `json:"dependencies"`
		}
		var lock struct {
			Packages map[string]struct {
				Version string `json:"version"`
			} `json:"packages"`
		}
		for name, target := range map[string]any{"package.json": &manifest, "package-lock.json": &lock} {
			data, err := os.ReadFile(filepath.Join(root, dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, target); err != nil {
				t.Fatalf("%s/%s: %v", dir, name, err)
			}
		}
		for _, dependency := range []string{"http-cache-semantics", "postcss-selector-parser"} {
			want, ok := manifest.Dependencies[dependency]
			if !ok {
				t.Errorf("%s does not pin %s directly", dir, dependency)
				continue
			}
			if got := lock.Packages["node_modules/"+dependency].Version; got != want {
				t.Errorf("%s locks %s %q, want %q", dir, dependency, got, want)
			}
			if bundled, ok := lock.Packages["node_modules/npm/node_modules/"+dependency]; ok {
				t.Errorf("%s locks npm's bundled %s %s, which the install removes", dir, dependency, bundled.Version)
			}
		}
	}
}

// npmToolchainFixture copies install-npm-toolchain.sh into a temporary tree with the given package.json and puts a
// fake npm first on PATH. The fake npm ci installs npm with bundled copies of alpha, beta and gamma, and a top-level
// copy of each name in topLevel. It returns the toolchain directory and the command that runs the script.
func npmToolchainFixture(t *testing.T, manifest string, topLevel ...string) (string, *exec.Cmd) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("install-npm-toolchain.sh needs node on PATH:", err)
	}
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "automation", "ci", "install-npm-toolchain.sh"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	script := filepath.Join(root, "automation", "ci", "install-npm-toolchain.sh")
	toolchain := filepath.Join(root, "automation", "ci", "npm-toolchain")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{toolchain, bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakeNpm := "#!/usr/bin/env bash\nset -euo pipefail\n" +
		`test "$*" = "ci --ignore-scripts --no-audit --no-fund"` + "\n" +
		"for name in alpha beta gamma; do mkdir -p node_modules/npm/node_modules/$name; echo '{}' > node_modules/npm/node_modules/$name/package.json; done\n" +
		"for name in " + strings.Join(topLevel, " ") + "; do mkdir -p node_modules/$name; echo '{}' > node_modules/$name/package.json; done\n" +
		"mkdir -p node_modules/npm/bin\necho '{}' > node_modules/npm/package.json\necho 'console.log(\"12.1.0\")' > node_modules/npm/bin/npm-cli.js\n"
	for path, content := range map[string]string{
		script:                                   string(source),
		filepath.Join(toolchain, "package.json"): manifest,
		filepath.Join(bin, "npm"):                fakeNpm,
	} {
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := testenv.ScriptCommand(t, script)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return toolchain, cmd
}

// The toolchain script removes npm's bundled copy of every dependency package.json pins besides npm itself, and only
// those, so npm resolves the pinned top-level copy.
func TestNpmToolchainInstallRemovesBundledCopies(t *testing.T) {
	toolchain, cmd := npmToolchainFixture(t, `{"dependencies":{"alpha":"1.0.0","beta":"1.0.0","npm":"12.1.0"}}`, "alpha", "beta")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install-npm-toolchain.sh: %v\n%s", err, output)
	}
	for name, wantBundled := range map[string]bool{"alpha": false, "beta": false, "gamma": true} {
		_, err := os.Stat(filepath.Join(toolchain, "node_modules", "npm", "node_modules", name))
		if bundled := err == nil; bundled != wantBundled {
			t.Errorf("npm's bundled %s present = %v, want %v", name, bundled, wantBundled)
		}
	}
	if _, err := os.Stat(filepath.Join(toolchain, "node_modules", "npm")); err != nil {
		t.Errorf("the script removed npm itself: %v", err)
	}
}

// A pinned dependency that npm ci did not install at the top level, or a manifest the script cannot read, fails the
// install instead of leaving npm with no copy or with its vulnerable bundled copy.
func TestNpmToolchainInstallFailsClosed(t *testing.T) {
	for name, fixture := range map[string]struct {
		manifest string
		topLevel []string
	}{
		"missing top-level copy": {`{"dependencies":{"alpha":"1.0.0","beta":"1.0.0","npm":"12.1.0"}}`, []string{"alpha"}},
		"unreadable manifest":    {`{"dependencies":`, []string{"alpha", "beta"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, cmd := npmToolchainFixture(t, fixture.manifest, fixture.topLevel...)
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("install-npm-toolchain.sh succeeded\n%s", output)
			}
		})
	}
}

func TestCINpmLocksPinDownloadIntegrity(t *testing.T) {
	root := repoRoot(t)
	cmd := testenv.ScriptCommand(t,
		filepath.Join(root, "automation", "ci", "check-npm-lock-integrity.py"),
		filepath.Join(root, "automation", "images", "ci-parity", "package-lock.json"),
		filepath.Join(root, "automation", "images", "npm-runtime", "package-lock.json"),
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lock integrity check failed: %v\n%s", err, output)
	}
}

func TestCINpmLockCheckRejectsUnverifiedArchive(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "package-lock.json")
	lock := `{"packages":{"":{"name":"fixture"},"node_modules/example":{"version":"1.0.0","resolved":"https://example.test/example.tgz"}}}`
	if err := os.WriteFile(lockPath, []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := testenv.ScriptCommand(t, filepath.Join(repoRoot(t), "automation", "ci", "check-npm-lock-integrity.py"), lockPath)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("lock without integrity digest was accepted")
	}
	if !strings.Contains(string(output), "has no integrity digest") {
		t.Fatalf("unexpected failure: %v\n%s", err, output)
	}
}

func TestCIBuildScriptRejectsUnknownImage(t *testing.T) {
	cmd := testenv.ScriptCommand(t, filepath.Join(repoRoot(t), "automation", "ci", "build-ci-image.sh"), "unknown")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("unknown image was accepted")
	}
	if !strings.Contains(string(output), "usage: automation/ci/build-ci-image.sh <go|parity>") {
		t.Fatalf("unexpected failure: %v\n%s", err, output)
	}
}

func TestCIBuildScriptRejectsPushFlag(t *testing.T) {
	cmd := testenv.ScriptCommand(t, filepath.Join(repoRoot(t), "automation", "ci", "build-ci-image.sh"), "go", "--push")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("unguarded push flag was accepted")
	}
	if !strings.Contains(string(output), "usage: automation/ci/build-ci-image.sh <go|parity>") {
		t.Fatalf("unexpected failure: %v\n%s", err, output)
	}
}

func TestCIBaseSeederRejectsUnknownImage(t *testing.T) {
	cmd := testenv.ScriptCommand(t, filepath.Join(repoRoot(t), "automation", "ci", "seed-ci-image-bases.sh"), "unknown")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("unknown image was accepted")
	}
	if !strings.Contains(string(output), "usage: automation/ci/seed-ci-image-bases.sh <go|parity>") {
		t.Fatalf("unexpected failure: %v\n%s", err, output)
	}
}

func TestCIBaseSeederRequiresOwnedDirectory(t *testing.T) {
	script := filepath.Join(repoRoot(t), "automation", "ci", "seed-ci-image-bases.sh")
	cmd := testenv.ScriptCommand(t, script, "go")
	cmd.Env = append(os.Environ(), "CI_BASE_DIR=")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "CI_BASE_DIR is required") {
		t.Fatalf("missing directory was not rejected: %v\n%s", err, output)
	}

	unowned := filepath.Join(t.TempDir(), "unowned")
	if err := os.Mkdir(unowned, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd = testenv.ScriptCommand(t, script, "go")
	cmd.Env = append(os.Environ(), "CI_BASE_DIR="+unowned)
	output, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "refusing to replace unowned CI_BASE_DIR") {
		t.Fatalf("unowned directory was not rejected: %v\n%s", err, output)
	}
}

func TestCIAPKLocksAreSortedAndUnique(t *testing.T) {
	root := repoRoot(t)
	for _, relative := range []string{
		filepath.Join("automation", "images", "ci-go", "packages.lock"),
		filepath.Join("automation", "images", "ci-parity", "packages.lock"),
	} {
		contents, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(contents)), "\n")
		if !slices.IsSorted(lines) {
			t.Fatalf("%s is not sorted", relative)
		}
		for i, line := range lines {
			if !strings.Contains(line, "=") {
				t.Fatalf("%s line %d does not pin a version: %q", relative, i+1, line)
			}
			if i > 0 && line == lines[i-1] {
				t.Fatalf("%s contains duplicate %q", relative, line)
			}
		}
	}
}
