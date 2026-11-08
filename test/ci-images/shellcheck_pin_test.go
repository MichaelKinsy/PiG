// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// CI analyzes shell scripts with the ShellCheck that install-shellcheck.sh installs from a sha256-pinned release, not the one apt carries, and
// make pre-push requires the same version. The version has one source: the installer.
func TestShellcheckIsPinnedForCIAndTheGate(t *testing.T) {
	root := repoRoot(t)
	out, err := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(root, "automation/ci/install-shellcheck.sh"), "--print-version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+\n$`).Match(out) {
		t.Fatalf("--print-version printed %q", out)
	}
	security := readWorkflow(t, "security.yml")
	var installs bool
	for _, step := range security.Jobs["source-assurance"].Steps {
		if strings.Contains(step.Run, "install-apt-packages.sh") && strings.Contains(step.Run, "shellcheck") {
			t.Errorf("security.yml installs shellcheck from apt, which is unpinned: %q", step.Run)
		}
		if strings.Contains(step.Run, "automation/ci/install-shellcheck.sh") {
			installs = true
		}
	}
	if !installs {
		t.Error("security.yml's source-assurance job does not run automation/ci/install-shellcheck.sh")
	}
	gate, err := os.ReadFile(filepath.Join(root, "automation/ci/pre-push-gate.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gate), "install-shellcheck.sh --print-version") {
		t.Error("pre-push-gate.sh does not take the shellcheck version from install-shellcheck.sh")
	}
}

// A tarball that does not match the pinned digest is refused before anything is installed.
func TestShellcheckInstallerRefusesAnUnpinnedArchive(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the installer pins Linux release binaries")
	}
	bin := t.TempDir()
	fake := "#!/bin/sh\nwhile [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then printf 'not the release' > \"$2\"; fi; shift; done\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cmd := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(repoRoot(t), "automation/ci/install-shellcheck.sh"))
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "SHELLCHECK_INSTALL_DIR="+dir, "FETCH_CACHE_DIR="+t.TempDir(), "FETCH_RETRY_DELAY=0")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "pinned") {
		t.Fatalf("installer accepted a wrong archive: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(dir, "shellcheck")); err == nil {
		t.Error("installer left a shellcheck behind")
	}
}
