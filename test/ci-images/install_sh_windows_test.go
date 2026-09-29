//go:build windows

package ciimages

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// install.sh installs Linux and macOS releases only. Under Git for Windows'
// sh, uname reports MINGW64_NT, so the installer refuses before any
// download, points at the release archives, and installs nothing.
func TestInstallShRefusesWindows(t *testing.T) {
	root := t.TempDir()
	fakebin := filepath.Join(root, "fakebin")
	if err := os.MkdirAll(fakebin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakebin, "curl"), []byte(fakeCurl), 0o755); err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(root, "fixtures")
	if err := os.MkdirAll(fixtures, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	installDir := filepath.Join(home, "bin")
	cmd := exec.Command(testenv.Sh(t), filepath.Join(repoRoot(t), "docs", "site", "public", "install.sh"))
	cmd.Env = []string{
		"PATH=" + filepath.ToSlash(fakebin) + ":/usr/bin:/bin",
		"HOME=" + home,
		"FIXTURES=" + fixtures,
		"PIG_INSTALL_DIR=" + installDir,
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("install.sh = %v, want exit status 1\nstderr:\n%s", err, stderr.String())
	}
	for _, want := range []string{"unsupported operating system MINGW", "download a release archive from GitHub instead"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr does not report %q:\n%s", want, stderr.String())
		}
	}
	if requests, err := os.ReadFile(filepath.Join(fixtures, ".requests")); err == nil {
		t.Fatalf("install.sh downloaded before refusing:\n%s", requests)
	}
	if _, err := os.Stat(installDir); !os.IsNotExist(err) {
		t.Fatalf("install directory exists after the refusal: %v", err)
	}
}
