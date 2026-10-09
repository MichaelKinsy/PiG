package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// package-manager.ts:1373-1393,1428-1433 packageSourcesMatch computes the stored
// key, then the input key, for each configured package; resolvePath throws for an
// invalid file: URL, so `pi remove` reports that error instead of "No matching
// package found". With no configured packages nothing is resolved (Pi 0.87.1
// prints "No matching package found" then).
func TestRemoveInvalidFileURLSourceSurfacesResolveError(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	removed, err := packageManagerFor(cwd, sm, nil).RemoveSourceFromSettings("file:///a%2Fb", false)
	if err != nil || removed {
		t.Fatalf("empty packages: removed=%t err=%v; want false, nil", removed, err)
	}
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "../pkg"}}); err != nil {
		t.Fatal(err)
	}
	removed, err = packageManagerFor(cwd, sm, nil).RemoveSourceFromSettings("file:///a%2Fb", false)
	if removed || err == nil || err.Error() != invalidFileURLMessage() {
		t.Fatalf("removed=%t err=%v; want %q", removed, err, invalidFileURLMessage())
	}
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "file:///a%2Fb"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := packageManagerFor(cwd, sm, nil).RemoveSourceFromSettings("../pkg", false); err == nil || err.Error() != invalidFileURLMessage() {
		t.Fatalf("stored invalid URL: err=%v; want %q", err, invalidFileURLMessage())
	}
}

// package-manager.ts:1059-1077 update(source) computes getPackageIdentity(source)
// before scanning settings, so an invalid file: URL fails even with no packages.
func TestUpdateInvalidFileURLSourceSurfacesResolveError(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := packageManagerFor(cwd, sm, nil).Update("file:///a%2Fb"); err == nil || err.Error() != invalidFileURLMessage() {
		t.Fatalf("update error = %v; want %q", err, invalidFileURLMessage())
	}
}

// package-manager.ts:912-926 dedupePackages computes every configured package
// identity (project, then user) before installing or resolving anything, so an
// invalid file: URL in a packages array fails startup; the exact Pi 0.87.1 binary
// exits 1 with fileURLToPath's error and never reaches the model.
func TestStartupRejectsInvalidPackageFileURL(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, scope := range []string{"user", "project"} {
		t.Run(scope, func(t *testing.T) {
			root := t.TempDir()
			cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			settings := filepath.Join(agentDir, "settings.json")
			if scope == "project" {
				settings = filepath.Join(codingagent.ProjectConfigDir(cwd), "settings.json")
			}
			writeResourceLoaderFixture(t, settings, `{"packages":["file:///a%2Fb"]}`)
			run := runPigStartup(t, binary, root, agentDir, cwd, "", "--no-session", "--approve", "--print", "hello")
			var exitErr *exec.ExitError
			if !errors.As(run.err, &exitErr) || exitErr.ExitCode() != 1 || run.stdout != "" || run.stderr != "Error: "+invalidFileURLMessage()+"\n" {
				t.Fatalf("startup = %v, stdout %q, stderr %q", run.err, run.stdout, run.stderr)
			}
		})
	}
}

// package-manager.ts:824-831 addSourceToSettings uses findIndex over packageSourcesMatch, so a stored invalid file: URL ahead of any match throws.
func TestAddSourceSurfacesStoredInvalidFileURL(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "file:///a%2Fb"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := packageManagerFor(cwd, sm, nil).AddSourceToSettings("npm:left-pad", false); err == nil || err.Error() != invalidFileURLMessage() {
		t.Fatalf("add error = %v; want %q", err, invalidFileURLMessage())
	}
	if got := sm.GetGlobalSettings().Packages; len(got) != 1 {
		t.Fatalf("packages changed after the failure: %+v", got)
	}
}

// package-manager.ts:977-1000: `pi list` resolves each local package path and fails with fileURLToPath's error, printing no package list. Exact Pi 0.87.1: "Error: File URL path must not include encoded / characters", exit 1.
func TestListPackagesSurfacesInvalidFileURL(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "../pkg"}, {Source: "file:///a%2Fb"}}); err != nil {
		t.Fatal(err)
	}
	var code int
	stderr := captureStderr(t, func() { code = listPackages(cwd, sm) })
	if code != 1 || stderr != "Error: "+invalidFileURLMessage()+"\n" {
		t.Fatalf("list = %d, stderr %q", code, stderr)
	}
}

// package-manager-cli.ts:842-850 `pi config` runs packageManager.resolve() for the
// global and trusted project settings; an invalid settings file: URL throws there.
func TestConfigSelectorRejectsInvalidSettingsFileURL(t *testing.T) {
	for _, settings := range []string{`{"skills":["file:///a%2Fb"]}`, `{"packages":["file:///a%2Fb"]}`} {
		cwd := t.TempDir()
		agentDir := filepath.Join(t.TempDir(), "agent")
		writeResourceLoaderFixture(t, filepath.Join(agentDir, "settings.json"), settings)
		sm := codingagent.NewSettingsManager(cwd, agentDir)
		if _, err := newConfigSelector(cwd, agentDir, sm); err == nil || err.Error() != invalidFileURLMessage() {
			t.Fatalf("%s: config selector error = %v; want %q", settings, err, invalidFileURLMessage())
		}
	}
}
