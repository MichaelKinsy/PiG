package ciimages

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
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
