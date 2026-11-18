package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's resource loader resolves its packages on every load (resource-loader.ts reload: resolve() installs each configured npm or git Package that is
// missing and refreshes the temporary -e sources): at startup, on /reload and on every session replacement. A Package whose install fails is skipped and
// listed with the end of its command's output, under [Extension issues]. These tests drive PiG's counterparts: the /reload snapshot provider and the
// runtime builder.

func setConfiguredPackages(t *testing.T, sm *codingagent.SettingsManager, sources ...string) {
	t.Helper()
	packages := make([]codingagent.PackageSource, 0, len(sources))
	for _, source := range sources {
		packages = append(packages, codingagent.PackageSource{Source: source})
	}
	if err := sm.SetPackages(packages); err != nil {
		t.Fatal(err)
	}
}

func TestReloadSnapshotInstallsAConfiguredPackageAddedAfterStartup(t *testing.T) {
	cwd, agentDir, sm := packageRunFixture(t)
	run := newPackageRun(appModeInteractive)
	run.stopAnnouncing()
	provider := reloadResourceSnapshotProvider(cwd, agentDir, sm, Args{packages: run}, nil)
	if snapshot := provider(); len(snapshot.PromptPaths) != 0 || len(snapshot.ExtensionWarnings) != 0 {
		t.Fatalf("snapshot before the package was added = %+v", snapshot)
	}
	setConfiguredPackages(t, sm, "npm:probe-npm")
	var snapshot codingagent.ReloadResourceSnapshot
	stdout, stderr, _ := captureStdoutStderr(t, func() int {
		snapshot = provider()
		return 0
	})
	if stdout != "" || stderr != "" {
		t.Errorf("stdout = %q stderr = %q, want nothing over the TUI", stdout, stderr)
	}
	if snapshot.Err != nil || len(snapshot.ExtensionWarnings) != 0 {
		t.Fatalf("snapshot = %+v, want the install to succeed", snapshot)
	}
	want := filepath.Join(agentDir, "npm", "node_modules", "probe-npm", "prompts")
	if len(snapshot.PromptPaths) != 1 || !strings.HasPrefix(snapshot.PromptPaths[0], want) {
		t.Fatalf("prompt paths = %v, want the installed Package's prompts under %s", snapshot.PromptPaths, want)
	}
}

func TestReloadSnapshotListsAFailedInstallWithTheEndOfItsOutput(t *testing.T) {
	cwd, agentDir, sm := packageRunFixture(t)
	run := newPackageRun(appModeInteractive)
	run.stopAnnouncing()
	setConfiguredPackages(t, sm, "npm:fail-pkg", "npm:probe-npm")
	var snapshot codingagent.ReloadResourceSnapshot
	stdout, stderr, _ := captureStdoutStderr(t, func() int {
		snapshot = reloadResourceSnapshotProvider(cwd, agentDir, sm, Args{packages: run}, nil)()
		return 0
	})
	if stdout != "" || stderr != "" {
		t.Errorf("stdout = %q stderr = %q, want nothing over the TUI", stdout, stderr)
	}
	if snapshot.Err != nil {
		t.Fatalf("snapshot error = %v, want the reload to go on past a failed install", snapshot.Err)
	}
	if len(snapshot.ExtensionWarnings) != 1 || snapshot.ExtensionWarnings[0].Path != "npm:fail-pkg" {
		t.Fatalf("warnings = %+v, want one for npm:fail-pkg", snapshot.ExtensionWarnings)
	}
	warning := snapshot.ExtensionWarnings[0].Warning
	for _, want := range []string{"failed to install (", "failed with code 1:\nnpm warn deprecated left-pad@1.0.0: use something else\nnpm ERR! code ELIFECYCLE\nnpm ERR! fail-pkg postinstall: exit 1", `Run "` + codingagent.AppName + ` remove npm:fail-pkg" to remove it from settings.json.`} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning = %q, want it to contain %q", warning, want)
		}
	}
	if len(snapshot.PromptPaths) != 1 {
		t.Errorf("prompt paths = %v, want the Package that installed to load", snapshot.PromptPaths)
	}
}

func TestRuntimeBuildInstallsConfiguredPackagesAndListsAFailureAsAnExtensionWarning(t *testing.T) {
	f := packageProcessFixture{packageResourceFixture: newPackageResourceFixture(t)}
	f.settings = codingagent.NewSettingsManager(f.cwd, f.agent)
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	fakeNpmOnPath(t)
	setConfiguredPackages(t, f.settings, "npm:fail-pkg", "npm:probe-npm")
	builder := newCLIGlobTestBuilder(t, f, "--print")
	builder.packages = newPackageRun(appModeInteractive)
	builder.packages.stopAnnouncing()
	var build *cliBuild
	var err error
	_, stderr, _ := captureStdoutStderr(t, func() int {
		build, err = builder.buildResources(t.Context(), cliBuildInput{CWD: f.cwd})
		return 0
	})
	if err != nil {
		t.Fatalf("build error = %v, want the build to go on past a failed install", err)
	}
	releaseCLIBuildAtCleanup(t, build)
	for _, unwanted := range []string{"Reinstalled", "Could not install", "npm warn deprecated", "added 1 package"} {
		if strings.Contains(stderr, unwanted) {
			t.Errorf("stderr = %q, want no %q: the install is silent over the TUI and a failure is a diagnostic", stderr, unwanted)
		}
	}
	if _, err := os.Stat(filepath.Join(f.agent, "npm", "node_modules", "probe-npm", "package.json")); err != nil {
		t.Errorf("the Package that can install was not installed: %v", err)
	}
	warned := false
	for _, diagnostic := range build.ExtensionDiagnostics {
		warned = warned || (diagnostic.Type == "warning" && strings.HasPrefix(diagnostic.Message, `Extension package "npm:fail-pkg": failed to install (`) && strings.Contains(diagnostic.Message, "npm ERR! fail-pkg postinstall: exit 1"))
	}
	if !warned {
		t.Errorf("no install warning in %+v", build.ExtensionDiagnostics)
	}
	if len(build.ExtensionWarnings) != 1 || build.ExtensionWarnings[0].Path != "npm:fail-pkg" {
		t.Errorf("extension warnings = %+v, want the failed install for the runtime's [Extension issues]", build.ExtensionWarnings)
	}
}

// PiG has no single loader step that every resource load goes through: the startup and replacement builds (buildResources) and the /reload snapshot provider
// (reloadResourceSnapshotProvider, which the interactive /reload and the headless reload both call) are separate entries. Each one calls installPackagesForLoad.
// This test lists the callers of both entries and of the installer, so a new path that loads resources fails here until it installs packages too.
func TestEveryResourceLoadInstallsPackages(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	callers := map[string][]string{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"installPackagesForLoad", "reloadResourceSnapshotProvider", "buildResources"} {
			for line := range strings.SplitSeq(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "func ") {
					continue
				}
				if strings.Contains(line, name+"(") {
					callers[name] = append(callers[name], file)
					break
				}
			}
		}
	}
	want := map[string][]string{
		"installPackagesForLoad":         {"cli_runtime_build.go", "reload_resources.go"},
		"reloadResourceSnapshotProvider": {"headless_reload.go", "interactive_build.go", "main.go"},
		"buildResources":                 {"main.go", "print_mode_build.go"},
	}
	for name, files := range want {
		if got := callers[name]; strings.Join(got, ",") != strings.Join(files, ",") {
			t.Errorf("callers of %s = %v, want %v: a path that loads resources must install packages (installPackagesForLoad) like the others", name, got, files)
		}
	}
}
