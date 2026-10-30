package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// D215, #389: a Package installed by absolute path through one spelling of the agent directory (a symlinked PIG_HOME) still resolves when the agent
// directory is reached through another spelling at another depth. Pi stores the path relative to the agent directory as spelled at install.
func TestInstalledAbsoluteLocalPackageResolvesThroughAnotherAgentDirSpelling(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "x", "one", "pig-home")
	realAgent := filepath.Join(home, "agent")
	pkg := filepath.Join(tmp, "pkgsrc", "my-pkg")
	cwd := filepath.Join(tmp, "work")
	for _, dir := range []string{realAgent, filepath.Join(pkg, "extensions"), cwd, filepath.Join(tmp, "h4", "a", "b", "c")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(tmp, "h4", "a", "b", "c", "pig-home")
	if err := os.Symlink(home, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linkAgent := filepath.Join(link, "agent")

	viaLink := codingagent.NewSettingsManager(cwd, linkAgent)
	if _, err := packageManagerFor(cwd, viaLink, nil).AddSourceToSettings(pkg, false); err != nil {
		t.Fatal(err)
	}
	if got := viaLink.GetGlobalSettings().Packages[0].Source; got != pkg {
		t.Fatalf("stored source = %q, want the absolute path as typed %q", got, pkg)
	}

	direct := codingagent.NewSettingsManager(cwd, realAgent)
	if missing := packagemanager.MissingLocalPackages(cwd, realAgent, direct); len(missing) != 0 {
		t.Fatalf("the Package is missing through the real agent directory: %+v", missing)
	}
	packages := packagemanager.ConfiguredPackagesForResolution(cwd, realAgent, direct)
	if len(packages) != 1 || packages[0].InstalledPath != pkg {
		t.Fatalf("resolved packages = %+v, want one at %s", packages, pkg)
	}
}

// D215: a configured local Package whose directory does not exist is an extension warning of the startup, naming the path it resolved to and how to
// drop it, where Pi skips it without a word.
func TestStartupWarnsAboutAConfiguredLocalPackageThatDoesNotExist(t *testing.T) {
	f := newPackageProcessFixture(t, "process.exit(1);")
	missing := filepath.Join(t.TempDir(), "gone", "my-pkg")
	if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: missing}, {Source: "../gone-relative"}}); err != nil {
		t.Fatal(err)
	}
	build, err := newCLIGlobTestBuilder(t, f, "--print").buildResources(t.Context(), cliBuildInput{CWD: f.cwd})
	if err != nil {
		t.Fatalf("startup error = %v, want startup to go on past a missing local Package", err)
	}
	releaseCLIBuildAtCleanup(t, build)
	for source, resolved := range map[string]string{missing: missing, "../gone-relative": filepath.Join(filepath.Dir(f.agent), "gone-relative")} {
		want := "Extension package \"" + source + "\": failed to install (Path does not exist: " + resolved + "). Run \"" + codingagent.AppName + " remove " + source + "\""
		warned := false
		for _, diagnostic := range build.ExtensionDiagnostics {
			warned = warned || strings.Contains(diagnostic.Message, want)
		}
		if !warned {
			t.Fatalf("no warning %q in %+v", want, build.ExtensionDiagnostics)
		}
	}
}
