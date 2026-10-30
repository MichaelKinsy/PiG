package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/internal/testenv"
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
	testenv.RequireDirectoryLink(t, home, link)
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

// D216: a configured local Package whose directory does not exist is an extension warning of the startup, naming the path it resolved to and how to
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

// D216, #389: `pig install --validate-only` with no source checks each configured local Package. A deleted directory makes it exit 1 and name the
// entry, its resolved path and the `pig remove` that drops it; a Package that resolves passes.
func TestInstallValidateOnlyWithoutSourceReportsConfiguredLocalPackages(t *testing.T) {
	tmp := t.TempDir()
	pigHome := filepath.Join(tmp, "home")
	t.Setenv("PIG_HOME", pigHome)
	cwd := filepath.Join(tmp, "work")
	present := filepath.Join(tmp, "pkgsrc", "present")
	gone := filepath.Join(tmp, "pkgsrc", "gone")
	for _, dir := range []string{present, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(cwd)
	sm := codingagent.NewSettingsManager(cwd, codingagent.AgentDir())
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: present}}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", "--validate-only", "--json"}) })
	if code != 0 {
		t.Fatalf("code = %d with every Package present, want 0\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	type missingPackage struct {
		Source string `json:"source"`
		Scope  string `json:"scope"`
		Path   string `json:"path"`
		Error  string `json:"error"`
		Remove string `json:"remove"`
	}
	type report struct {
		Valid   bool             `json:"valid"`
		Checked int              `json:"checked"`
		Missing []missingPackage `json:"missing"`
	}
	out, _, _ := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", "--validate-only"}) })
	if want := "valid: 1 configured local package resolves\n"; out != want {
		t.Fatalf("text summary = %q, want %q", out, want)
	}
	var ok report
	if err := json.Unmarshal([]byte(stdout), &ok); err != nil || !ok.Valid || ok.Checked != 1 || len(ok.Missing) != 0 {
		t.Fatalf("report = %s (%v), want valid, checked 1, none missing", stdout, err)
	}

	if err := sm.SetPackages([]codingagent.PackageSource{{Source: present}, {Source: gone}}); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code = captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", "--validate-only", "--json"}) })
	if code != 1 {
		t.Fatalf("code = %d with a deleted Package, want 1\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	var bad report
	if err := json.Unmarshal([]byte(stdout), &bad); err != nil {
		t.Fatalf("report %q: %v", stdout, err)
	}
	want := missingPackage{Source: gone, Scope: "user", Path: gone, Error: "Path does not exist: " + gone, Remove: codingagent.AppName + " remove " + gone}
	if bad.Valid || bad.Checked != 2 || len(bad.Missing) != 1 || bad.Missing[0] != want {
		t.Fatalf("report = %s, want invalid, checked 2, one missing %+v", stdout, want)
	}

	_, stderr, code = captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", "--validate-only"}) })
	if code != 1 || !strings.Contains(stderr, gone) || !strings.Contains(stderr, `Run "`+codingagent.AppName+` remove `+gone+`"`) {
		t.Fatalf("text mode code = %d stderr = %q, want 1 naming %s and its remove command", code, stderr, gone)
	}

	// JSON.stringify never escapes <, > or &, so a path holding them is written as it is.
	special := filepath.Join(tmp, "pkgsrc", "a<b>&c")
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: special}}); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code = captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", "--validate-only", "--json"}) })
	if code != 1 || strings.Count(stdout, "a<b>&c") != 4 || strings.Contains(stdout, `\u003`) || !strings.HasSuffix(stdout, "\n}\n") {
		t.Fatalf("code = %d, stdout = %q (stderr %q), want 1 with the path unescaped in source, path, error and remove and Pi's 2-space layout", code, stdout, stderr)
	}
}
