package packagemanager

import (
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// requireEmptyDir fails when anything appears in dir, the process directory of a test that runs package operations without an
// agent directory.
func requireEmptyDir(t *testing.T, dir, after string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("%s wrote %v into the process directory", after, names)
	}
}

// Pi's DefaultPackageManager requires agentDir and resolves it and cwd to absolute paths (package-manager.ts:814-823), so every
// managed npm project and Git checkout it writes is absolute. An empty Go agent directory or cwd must fail before anything is
// written, never create npm/{.gitignore,package.json} or git/ under the process directory.
func TestEmptyAgentDirNeverWritesIntoTheProcessDirectory(t *testing.T) {
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("GIT_TERMINAL_PROMPT", "0") // the Git source is a refused local port, so a regression fails fast instead of prompting
	dir := t.TempDir()
	t.Chdir(dir)
	sm := codingagent.NewInMemorySettingsManager(codingagent.Settings{})
	if sm.AgentDir() != "" {
		t.Fatalf("fixture: in-memory settings have agent directory %q", sm.AgentDir())
	}
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:not-installed-pkg"}, {Source: "https://127.0.0.1:1/example/not-installed.git"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := NewPackageManager(PackageManagerOptions{CWD: "", SettingsManager: sm}).Resolve(nil); err == nil || !strings.Contains(err.Error(), "not absolute") {
		t.Fatalf("Resolve with an empty agent directory = %v, want the not-absolute error", err)
	}
	requireEmptyDir(t, dir, "Resolve")

	if err := InstallManagedNPM("", "", sm, "npm:not-installed-pkg", false); err == nil {
		t.Fatal("InstallManagedNPM with an empty agent directory succeeded")
	}
	requireEmptyDir(t, dir, "InstallManagedNPM")

	if err := InstallManagedGit("", "", sm, "https://127.0.0.1:1/example/not-installed.git", false); err == nil {
		t.Fatal("InstallManagedGit with an empty agent directory succeeded")
	}
	requireEmptyDir(t, dir, "InstallManagedGit")

	ref, err := ParseNpmInstallRef("npm:not-installed-pkg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetLatestNpmVersion("", sm, ref, false); err == nil {
		t.Fatal("GetLatestNpmVersion with an empty agent directory succeeded")
	}
	requireEmptyDir(t, dir, "GetLatestNpmVersion")

	if err := EnsureManagedPackageRoot("npm"); err == nil {
		t.Fatal("EnsureManagedPackageRoot accepted a relative root")
	}
	requireEmptyDir(t, dir, "EnsureManagedPackageRoot")
}
