// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func writeCIFixture(t *testing.T, root, path, content string) {
	t.Helper()
	path = filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

// mockedBash returns a command that runs bash with args and the mocks
// directory first on PATH. Git for Windows' bash puts its own tool
// directories ahead of the PATH it inherits, which would select Git's curl,
// tar and rm over the mocks, so bash prepends the directory itself.
func mockedBash(t *testing.T, mocks string, args ...string) *exec.Cmd {
	t.Helper()
	prepend := `PATH="$(cd "$1" && pwd):$PATH" && shift && exec bash "$@"`
	return exec.CommandContext(t.Context(), testenv.Bash(t), append([]string{"-c", prepend, "bash", mocks}, args...)...)
}

// requireBashMake skips a test of a make target when the test's bash has no
// make that receives its environment. Git for Windows ships bash without
// make, and a make from another MSYS installation does not see the variables
// this bash exports.
func requireBashMake(t *testing.T) {
	t.Helper()
	probe := mockedBash(t, t.TempDir(), "-c", `printf 'probe:\n\t@test "$$PIG_MAKE_PROBE" = visible\n' | make -f - probe`)
	probe.Env = append(os.Environ(), "PIG_MAKE_PROBE=visible")
	if output, err := probe.CombinedOutput(); err != nil {
		t.Skipf("no make that receives the environment of %s is on its PATH: %v\n%s", testenv.Bash(t), err, output)
	}
}

func copyCIFixture(t *testing.T, root, path string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	writeCIFixture(t, root, path, string(data))
}

// Sentinel versions prove that verification follows the inputs, not today's pins.
// Docker is replaced at the process boundary; its actual validation shell runs.
func TestCIImageValidationDerivesToolchainInputs(t *testing.T) {
	root := t.TempDir()
	copyCIFixture(t, root, "automation/ci/build-ci-image.sh")
	for _, image := range []string{"ci-go", "ci-parity"} {
		copyCIFixture(t, root, "automation/images/"+image+"/metadata.env")
	}
	for path, content := range map[string]string{
		".go-version":   "9.8.7\n",
		".node-version": "98.7.6\n",
		"internal/coding/pigversion/pigversion.go":   "const UpstreamVersion = \"7.6.5\"\n",
		"automation/images/npm-runtime/package.json": `{"dependencies":{"npm":"8.7.6"}}`,
		".github/workflows/ci.yml":                   "env:\n  RUST_VERSION: 6.5.4\n",
		"bin/docker": `#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  build) exit 0 ;;
  run)
    shift
    while [[ "$1" != bash ]]; do shift; done
    exec "$@"
    ;;
  *) exit 99 ;;
esac
`,
		"bin/go":    "#!/bin/sh\nprintf 'go9.8.7\\n'\n",
		"bin/node":  "#!/bin/sh\nprintf 'v98.7.6\\n'\n",
		"bin/npm":   "#!/bin/sh\nprintf '8.7.6\\n'\n",
		"bin/rustc": "#!/bin/sh\nprintf 'rustc 6.5.4 (fixture)\\n'\n",
		"bin/pi":    "#!/bin/sh\nprintf '7.6.5\\n'\n",
		"bin/tmux":  "#!/bin/sh\nprintf 'tmux fixture\\n'\n",
	} {
		writeCIFixture(t, root, path, content)
	}
	bin := filepath.Join(root, "bin")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SOURCE_REVISION", strings.Repeat("a", 40))
	t.Setenv("CI_BASE_DIR", "")
	// The mocked docker runs the parity validation with the host's python3,
	// so the lock pins that interpreter's version. Windows has python.exe
	// only, so the mocks carry a python3 that runs it. That mock execs the
	// interpreter's own path, sys.executable: a version-manager shim (mise,
	// pyenv, asdf) looks python3 up on PATH again and would exec the mock forever.
	executable, err := exec.CommandContext(t.Context(), hostPython(), "-c", "import sys; print(sys.executable)").Output()
	interpreter := strings.TrimSpace(string(executable))
	if err != nil || !filepath.IsAbs(interpreter) {
		t.Fatalf("host Python interpreter %s: %q, %v", hostPython(), interpreter, err)
	}
	writeCIFixture(t, root, "bin/python3", "#!/bin/sh\nexec '"+filepath.ToSlash(interpreter)+"' \"$@\"\n")
	version, err := mockedBash(t, bin, "-c", `python3 -c 'import sys; print("%d.%d" % sys.version_info[:2])'`).Output()
	if err != nil {
		t.Fatalf("python3 version: %v", err)
	}
	python := strings.TrimSpace(string(version))
	writeCIFixture(t, root, "automation/images/ci-parity/packages.lock", "python-"+python+"="+python+".99-r0\n")
	script := filepath.Join(root, "automation/ci/build-ci-image.sh")
	for _, image := range []string{"go", "parity"} {
		if output, err := mockedBash(t, bin, script, image).CombinedOutput(); err != nil {
			t.Errorf("validate %s with changed authoritative pins: %v\n%s", image, err, output)
		}
	}
	// A wrong executable or interpreter must still fail the identity check.
	writeCIFixture(t, root, "bin/pi", "#!/bin/sh\nprintf 'wrong-version\\n'\n")
	if output, err := mockedBash(t, bin, script, "parity").CombinedOutput(); err == nil {
		t.Fatalf("mismatched oracle accepted: %s", output)
	}
	writeCIFixture(t, root, "bin/pi", "#!/bin/sh\nprintf '7.6.5\\n'\n")
	writeCIFixture(t, root, "automation/images/ci-parity/packages.lock", "python-0.1=0.1.99-r0\n")
	if output, err := mockedBash(t, bin, script, "parity").CombinedOutput(); err == nil {
		t.Fatalf("mismatched python accepted: %s", output)
	}
}

func TestCIImageWorkflowWatchesAuthoritativePins(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github/workflows/ci-images.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		On map[string]struct{ Paths []string }
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"push", "pull_request"} {
		paths := "\n" + strings.Join(workflow.On[event].Paths, "\n") + "\n"
		for _, input := range []string{".go-version", ".node-version", "internal/coding/pigversion/pigversion.go", ".github/workflows/ci.yml"} {
			if !strings.Contains(paths, "\n"+input+"\n") {
				t.Errorf("%s ignores authoritative image input %s", event, input)
			}
		}
	}
}

func TestCIImageSeederUsesDockerfileDigests(t *testing.T) {
	root := t.TempDir()
	copyCIFixture(t, root, "automation/ci/seed-ci-image-bases.sh")
	digest := "@sha256:" + strings.Repeat("a", 64)
	writeCIFixture(t, root, "automation/images/ci-go/Dockerfile", "ARG GO_IMAGE=fixture/go"+digest+"\n")
	writeCIFixture(t, root, "automation/images/ci-parity/Dockerfile", "ARG GO_BUILDER=fixture/builder"+digest+"\nARG WOLFI_BASE=fixture/wolfi"+digest+"\n")
	for name, script := range map[string]string{
		"curl": "#!/bin/sh\nexit 0\n",
		// Like the real `sha256sum -c -`, read the whole check list: exiting
		// unread would SIGPIPE the seeder's echo and fail its pipefail pipeline.
		"sha256sum": "#!/bin/sh\ncat >/dev/null\n",
		"tar": `#!/usr/bin/env bash
set -euo pipefail
while [[ "$1" != -C ]]; do shift; done
cp "$MOCK_CRANE" "$2/crane"
`,
		"crane":  "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PULL_LOG\"\n",
		"docker": "#!/bin/sh\nif [ \"$1\" = load ]; then printf 'Loaded image: fixture\\n'; fi\n",
		"rm":     "#!/bin/sh\n# The mock crane does not create archives.\nexit 0\n",
	} {
		writeCIFixture(t, root, "bin/"+name, script)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MOCK_CRANE", filepath.Join(root, "bin/crane"))
	t.Setenv("PULL_LOG", filepath.Join(root, "pulls"))
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("PLATFORM", "linux/amd64")
	for _, image := range []string{"go", "parity"} {
		t.Setenv("CI_BASE_DIR", filepath.Join(root, "bases-"+image))
		cmd := mockedBash(t, filepath.Join(root, "bin"), filepath.Join(root, "automation/ci/seed-ci-image-bases.sh"), image)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("seed %s: %v\n%s", image, err, output)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "pulls"))
	if err != nil {
		t.Fatal(err)
	}
	for _, image := range []string{"go", "builder", "wolfi"} {
		if !strings.Contains(string(data), "fixture/"+image+digest) {
			t.Errorf("seeder did not use Dockerfile's %s pin:\n%s", image, data)
		}
	}
}
