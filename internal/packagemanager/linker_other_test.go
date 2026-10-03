//go:build !windows

package packagemanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// Android forbids execve of npm and git under the app data directory (EACCES),
// so the package manager's child starts must go through the linker. The fake
// linker does not exist, so a start that reaches it fails naming the linker,
// and a direct start of the data-directory file would not.
func TestPackageProcessesStartThroughTheLinker(t *testing.T) {
	dataDir := t.TempDir()
	bin := filepath.Join(dataDir, "usr", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"npm", "git"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("\x7fELF"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	linker := "/nonexistent-android/linker64"
	t.Cleanup(linkerexec.SetStarterForTest(linkerexec.Starter{
		Linker:   linker,
		DataDir:  dataDir,
		ReadHead: func(string) ([]byte, error) { return []byte("\x7fELF"), nil },
	}))
	t.Setenv("PATH", bin)

	for _, name := range []string{"npm", "git"} {
		err := RunPackageProcess(dataDir, name, "--version")
		if err == nil || !strings.Contains(err.Error(), linker) {
			t.Errorf("RunPackageProcess(%s) error = %v, want a start through %s", name, err, linker)
		}
		_, err = RunPackageCapture(dataDir, name, "--version")
		if err == nil || !strings.Contains(err.Error(), linker) {
			t.Errorf("RunPackageCapture(%s) error = %v, want a start through %s", name, err, linker)
		}
	}
}
