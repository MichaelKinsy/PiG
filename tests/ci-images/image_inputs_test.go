// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	if runtime.GOOS == "windows" {
		t.Skip("builds the Linux CI images with Linux tools; it runs on the Linux CI hosts")
	}
	root := t.TempDir()
	copyCIFixture(t, root, "automation/ci/build-ci-image.sh")
	for _, image := range []string{"ci-go", "ci-parity"} {
		copyCIFixture(t, root, "automation/images/"+image+"/metadata.env")
	}
	for path, content := range map[string]string{
		"go.mod":                          "module fixture\n\ntoolchain go9.8.7\n",
		".node-version":                   "98.7.6\n",
		"coding/pigversion/pigversion.go": "const UpstreamVersion = \"7.6.5\"\n",
		"automation/images/npm-runtime/package.json": `{"dependencies":{"npm":"8.7.6"}}`,
		".github/workflows/ci.yml":                   "env:\n  RUST_VERSION: 6.5.4\n",
		"automation/images/ci-parity/packages.lock":  "python-3.12=3.12.99-r0\n",
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
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SOURCE_REVISION", strings.Repeat("a", 40))
	t.Setenv("CI_BASE_DIR", "")
	for _, image := range []string{"go", "parity"} {
		cmd := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(root, "automation/ci/build-ci-image.sh"), image)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("validate %s with changed authoritative pins: %v\n%s", image, err, output)
		}
	}
	// A wrong executable must still fail the identity check.
	writeCIFixture(t, root, "bin/pi", "#!/bin/sh\nprintf 'wrong-version\\n'\n")
	cmd := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(root, "automation/ci/build-ci-image.sh"), "parity")
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("mismatched oracle accepted: %s", output)
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
		for _, input := range []string{"go.mod", ".node-version", "coding/pigversion/pigversion.go", ".github/workflows/ci.yml"} {
			if !strings.Contains(paths, "\n"+input+"\n") {
				t.Errorf("%s ignores authoritative image input %s", event, input)
			}
		}
	}
}

func TestCIImageSeederUsesDockerfileDigests(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("builds the Linux CI images with Linux tools; it runs on the Linux CI hosts")
	}
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
		cmd := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(root, "automation/ci/seed-ci-image-bases.sh"), image)
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
