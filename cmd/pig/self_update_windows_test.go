//go:build windows

package main

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// On Windows a standalone installation resolves to the unsupported
// self-update tier (D39): upstream self-updates only npm, pnpm and managed
// installs there, and a running Windows executable is not replaced in place.
// An explicit update of a receipted standalone pig.exe with a newer release
// available is refused with reinstall guidance, and nothing is replaced.
func TestSelfUpdateOnWindowsRefusesReceiptedStandalone(t *testing.T) {
	manSrv, binSrv := newerManifest(t, []byte("new"))
	defer manSrv.Close()
	defer binSrv.Close()
	seedRunningStandaloneReceipt(t, manSrv.URL)
	t.Setenv("PIG_INSTALL_TIER", "")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })
	if code != 1 || stdout != "" {
		t.Fatalf("code = %d, stdout = %q; want 1 and no output", code, stdout)
	}
	if !strings.Contains(stderr, "cannot self-update this installation") || !strings.Contains(stderr, "Executable:") {
		t.Fatalf("stderr missing reinstall remediation:\n%s", stderr)
	}
	after, err := os.Stat(exe)
	if err != nil || !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatalf("running executable changed: before=%v after=%v err=%v", before, after, err)
	}

	target := filepath.Join(t.TempDir(), "pig.exe")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := applyStandaloneUpdate(target, false); err == nil || !strings.Contains(err.Error(), "not supported on Windows") {
		t.Fatalf("applyStandaloneUpdate error = %v, want the Windows refusal", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "old" {
		t.Fatalf("standalone target changed: %q (err=%v)", got, err)
	}
}

// Upstream self-updates an npm install on Windows (package-manager-cli.ts)
// after quarantining the native images it loaded from the package directory
// (prepareWindowsNpmSelfUpdate), because Windows refuses to delete a running
// image. pig's running image there is pig.exe itself: without the quarantine
// npm cannot remove the package directory pig runs from. The next start on
// Windows clears the quarantine.
func TestWindowsNpmSelfUpdateReplacesTheRunningInstallation(t *testing.T) {
	manifest := `{"version":"9.9.9","packageName":"pig","binaries":{}}`
	srv := signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(manifest))
	}))
	defer srv.Close()
	t.Setenv("PIG_UPDATE_URL", srv.URL)
	t.Setenv("PIG_INSTALL_TIER", "")

	root := filepath.Join(t.TempDir(), "node_modules")
	pigExe := filepath.Join(root, "pig", "bin", "pig.exe")
	tools := t.TempDir()
	copyTestBinary(t, pigExe)
	copyTestBinary(t, filepath.Join(tools, "npm.exe"))

	update := exec.Command(pigExe)
	update.Dir = t.TempDir()
	update.Env = append(os.Environ(), npmUpdateFixtureRootEnv+"="+root, "PATH="+tools)
	out, err := update.CombinedOutput()
	if err != nil {
		t.Fatalf("pig update in an npm install: %v\n%s", err, out)
	}
	got, err := os.ReadFile(pigExe)
	if err != nil || string(got) != "install -g --ignore-scripts --min-release-age=0 pig@9.9.9" {
		t.Fatalf("npm did not replace the package: %q (err=%v)\n%s", got, err, out)
	}
	quarantine := filepath.Join(root, ".pig-native-quarantine")
	quarantined, err := filepath.Glob(filepath.Join(quarantine, "*", "pig.exe"))
	if err != nil || len(quarantined) != 1 {
		t.Fatalf("quarantined executables = %v (err=%v), want the one pig.exe that ran", quarantined, err)
	}
	codingagent.CleanupWindowsSelfUpdateQuarantine(filepath.Dir(pigExe))
	if _, err := os.Stat(quarantine); !os.IsNotExist(err) {
		t.Fatalf("quarantine after cleanup: %v, want it removed", err)
	}
}
