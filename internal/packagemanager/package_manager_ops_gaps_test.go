package packagemanager

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func packageSources(sm *codingagent.SettingsManager, local bool) []string {
	packages := sm.GetGlobalSettings().Packages
	if local {
		packages = sm.GetProjectSettings().Packages
	}
	var out []string
	for _, pkg := range packages {
		out = append(out, pkg.Source)
	}
	return out
}

// package-manager.ts addSourceToSettings/removeSourceFromSettings: an added source is stored in the scope's own settings, an identical or equivalent source is not written twice, a source with a new ref replaces the entry (keeping its filters), removal drops only the matching entries of its scope, and an unmatched source reports false.
// Pi: packages/coding-agent/src/core/package-manager.ts addSourceToSettings (:826-851), removeSourceFromSettings (:855-871).
func TestPackageManagerAddAndRemoveSourceInTheScopeSettings(t *testing.T) {
	_, _, sm, pm := resolveFixtureWithHome(t)
	if added, err := pm.AddSourceToSettings("npm:left-pad", false); err != nil || !added {
		t.Fatalf("first add = %v, %v; want true", added, err)
	}
	if added, err := pm.AddSourceToSettings("npm:left-pad", false); err != nil || added {
		t.Fatalf("adding the same source again = %v, %v; want false (no write)", added, err)
	}
	if got := packageSources(sm, false); !slices.Equal(got, []string{"npm:left-pad"}) {
		t.Fatalf("user packages = %v", got)
	}
	if got := packageSources(sm, true); len(got) != 0 {
		t.Fatalf("a user add wrote project packages: %v", got)
	}
	if added, err := pm.AddSourceToSettings("git:github.com/u/repo", true); err != nil || !added {
		t.Fatalf("project add = %v, %v; want true", added, err)
	}
	if got := packageSources(sm, true); !slices.Equal(got, []string{"git:github.com/u/repo"}) {
		t.Fatalf("project packages = %v", got)
	}
	if got := packageSources(sm, false); !slices.Equal(got, []string{"npm:left-pad"}) {
		t.Fatalf("a project add wrote user packages: %v", got)
	}

	// A new ref of the same repository replaces the entry in place and keeps its filters.
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:left-pad"}, {Source: "git:github.com/u/other@v1", WasObject: true, Extensions: []string{"a.ts"}}}); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "git:github.com/u/dup@v1"}, {Source: "git:github.com/u/dup@v2"}}); err != nil {
		t.Fatal(err)
	}
	if added, err := pm.AddSourceToSettings("git:github.com/u/dup@v3", false); err != nil || !added {
		t.Fatalf("ref change with duplicate entries = %v, %v", added, err)
	}
	if got := packageSources(sm, false); !slices.Equal(got, []string{"git:github.com/u/dup@v3", "git:github.com/u/dup@v2"}) {
		t.Fatalf("a new ref replaces the first matching entry only: %v", got)
	}
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:left-pad"}, {Source: "git:github.com/u/other@v1", WasObject: true, Extensions: []string{"a.ts"}}}); err != nil {
		t.Fatal(err)
	}
	if added, err := pm.AddSourceToSettings("git:github.com/u/other@v2", false); err != nil || !added {
		t.Fatalf("ref change = %v, %v; want true", added, err)
	}
	packages := sm.GetGlobalSettings().Packages
	if len(packages) != 2 || packages[1].Source != "git:github.com/u/other@v2" || !slices.Equal(packages[1].Extensions, []string{"a.ts"}) || packages[0].Source != "npm:left-pad" {
		t.Fatalf("packages after a ref change = %+v, want the second entry replaced in place with its filter kept", packages)
	}

	// An equivalent local spelling is stored normalised relative to the settings directory.
	localPkg := filepath.Join(t.TempDir(), "spelled")
	rel, err := filepath.Rel(sm.AgentDir(), localPkg)
	if err != nil {
		t.Fatal(err)
	}
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "./" + rel}}); err != nil {
		t.Fatal(err)
	}
	if added, err := pm.AddSourceToSettings(localPkg, false); err != nil || !added {
		t.Fatalf("adding another spelling of a local package = %v, %v; want true", added, err)
	}
	if got := packageSources(sm, false); !slices.Equal(got, []string{filepath.Clean(rel)}) {
		t.Fatalf("local package stored as %v, want the normalised relative path %q", got, filepath.Clean(rel))
	}
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:left-pad"}, {Source: "git:github.com/u/other@v2", WasObject: true, Extensions: []string{"a.ts"}}}); err != nil {
		t.Fatal(err)
	}
	if removed, err := pm.RemoveSourceFromSettings("npm:never-added", false); err != nil || removed {
		t.Fatalf("removing an unmatched source = %v, %v; want false", removed, err)
	}
	if removed, err := pm.RemoveSourceFromSettings("npm:left-pad", true); err != nil || removed {
		t.Fatalf("removing a user source from the project scope = %v, %v; want false", removed, err)
	}
	if removed, err := pm.RemoveSourceFromSettings("git:github.com/u/other@v3", false); err != nil || !removed {
		t.Fatalf("removing a package by identity (another ref) = %v, %v; want true", removed, err)
	}
	if got := packageSources(sm, false); !slices.Equal(got, []string{"npm:left-pad"}) {
		t.Fatalf("user packages after removal = %v", got)
	}
	if got := packageSources(sm, true); !slices.Equal(got, []string{"git:github.com/u/repo"}) {
		t.Fatalf("project packages after a user removal = %v", got)
	}
	if removed, err := pm.RemoveSourceFromSettings("git:github.com/u/repo", true); err != nil || !removed {
		t.Fatalf("project removal = %v, %v; want true", removed, err)
	}
	if got := packageSources(sm, true); len(got) != 0 {
		t.Fatalf("project packages after removal = %v", got)
	}
}

// package-manager.ts remove: a git checkout is deleted with its empty parents, an unmanaged npm source with no install root and a local source are a no-op, and a project removal needs a trusted project.
// Pi: packages/coding-agent/src/core/package-manager.ts remove (:1033-1060), uninstallManagedNPM.
func TestPackageManagerRemoveDeletesGitCheckoutsAndKeepsSharedOnes(t *testing.T) {
	cwd, agentDir, _, pm := resolveFixtureWithHome(t)
	source := "git:github.com/u/repo"
	checkout, err := GitCheckoutPath(cwd, agentDir, source, false)
	if err != nil {
		t.Fatal(err)
	}
	writeResolveFile(t, filepath.Join(checkout, "package.json"), "{}")
	marker := writeResolveFile(t, GitUpdateMarkerPath(checkout), "")
	if err := pm.Remove(source, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(checkout); !os.IsNotExist(err) {
		t.Fatalf("the git checkout survived Remove: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the incomplete-update marker survived Remove: %v", err)
	}
	root := codingagent.GitInstallRoot(cwd, agentDir, false)
	if _, err := os.Stat(filepath.Join(root, "github.com", "u")); !os.IsNotExist(err) {
		t.Fatalf("empty parent directories survived Remove: %v", err)
	}

	local := t.TempDir()
	if err := pm.Remove(local, false); err != nil {
		t.Fatalf("removing a local source = %v, want a no-op", err)
	}
	if _, err := os.Stat(local); err != nil {
		t.Fatalf("Remove deleted a local package directory: %v", err)
	}
	if err := pm.Remove("npm:not-installed", false); err != nil {
		t.Fatalf("removing an npm package with no install root = %v, want nil", err)
	}
	untrusted := NewPackageManager(PackageManagerOptions{CWD: t.TempDir(), SettingsManager: codingagent.NewSettingsManagerWithProjectTrust(t.TempDir(), t.TempDir(), false)})
	if err := untrusted.Remove(source, false); err != nil {
		t.Fatalf("user removal in an untrusted project = %v, want it allowed", err)
	}
	if err := untrusted.Remove(source, true); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("project removal in an untrusted project = %v, want a trust error", err)
	}
}

// package-manager.ts installAndPersist: an install that fails records nothing, and a Package scope's settings only change on success; removeAndPersist runs the removal before it edits settings.
// Pi: packages/coding-agent/src/core/package-manager.ts installAndPersist, removeAndPersist.
func TestPackageManagerInstallAndRemoveAndPersistOrderErrorsBeforeSettings(t *testing.T) {
	_, _, sm, pm := resolveFixtureWithHome(t)
	missing := filepath.Join(t.TempDir(), "missing-package")
	if err := pm.InstallAndPersist(missing, false); err == nil {
		t.Fatal("installing a missing local package succeeded")
	}
	if got := packageSources(sm, false); len(got) != 0 {
		t.Fatalf("a failed install was persisted: %v", got)
	}

	pkg := localPackage(t)
	if err := pm.InstallAndPersist(pkg, true); err != nil {
		t.Fatal(err)
	}
	if got := packageSources(sm, true); len(got) != 1 {
		t.Fatalf("project packages after a project install = %v", got)
	}
	if got := packageSources(sm, false); len(got) != 0 {
		t.Fatalf("a project install wrote user packages: %v", got)
	}

	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:keep-me"}}); err != nil {
		t.Fatal(err)
	}
	if removed, err := pm.RemoveAndPersist("npm:keep-me", false); err != nil || !removed {
		t.Fatalf("RemoveAndPersist = %v, %v; want true", removed, err)
	}
	if got := packageSources(sm, false); len(got) != 0 {
		t.Fatalf("user packages after RemoveAndPersist = %v", got)
	}

	cwd := t.TempDir()
	distrust := codingagent.NewSettingsManagerWithProjectTrust(cwd, t.TempDir(), false)
	dpm := NewPackageManager(PackageManagerOptions{CWD: cwd, SettingsManager: distrust})
	if err := dpm.Install(pkg, true); err == nil {
		t.Fatal("a project install in an untrusted project succeeded")
	}
	if err := dpm.Install(pkg, false); err != nil {
		t.Fatalf("a user install in an untrusted project = %v, want it allowed", err)
	}
	if removed, err := dpm.RemoveAndPersist("npm:x", true); err == nil || removed {
		t.Fatalf("RemoveAndPersist in an untrusted project = %v, %v; want the trust error and no settings edit", removed, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// package-manager.ts checkForAvailableUpdates: an installed, unpinned git package whose remote HEAD moved is reported once with its host/path display name and scope; a pinned git ref, a not-installed package, offline mode, and no packages all yield an empty (non-nil) list.
// Pi: packages/coding-agent/src/core/package-manager.ts checkForAvailableUpdates (:1186-1253).
func TestPackageManagerCheckForAvailableUpdatesReportsMovedGitRemotes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cwd, agentDir, sm, pm := resolveFixtureWithHome(t)
	if got := pm.CheckForAvailableUpdates(); got == nil || len(got) != 0 {
		t.Fatalf("no packages = %#v, want an empty non-nil list", got)
	}

	remote := filepath.Join(t.TempDir(), "remote.git")
	work := filepath.Join(t.TempDir(), "work")
	runGit(t, "", "init", "-q", "--bare", "-b", "main", remote)
	runGit(t, "", "clone", "-q", remote, work)
	writeResolveFile(t, filepath.Join(work, "package.json"), "{}")
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-q", "-m", "one")
	runGit(t, work, "push", "-q", "origin", "HEAD:main")

	source := "git:github.com/u/repo"
	checkout, err := GitCheckoutPath(cwd, agentDir, source, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(checkout), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "-q", remote, checkout)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: source}}); err != nil {
		t.Fatal(err)
	}
	if got := pm.CheckForAvailableUpdates(); len(got) != 0 {
		t.Fatalf("an up-to-date checkout reported %+v", got)
	}

	writeResolveFile(t, filepath.Join(work, "more.txt"), "x")
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-q", "-m", "two")
	runGit(t, work, "push", "-q", "origin", "HEAD:main")
	got := pm.CheckForAvailableUpdates()
	if len(got) != 1 || got[0].Source != source || got[0].Type != "git" || got[0].Scope != "user" || got[0].DisplayName != "github.com/u/repo" {
		t.Fatalf("moved remote reported %+v, want one git update for %s", got, source)
	}

	t.Setenv("PI_OFFLINE", "1")
	if got := pm.CheckForAvailableUpdates(); got == nil || len(got) != 0 {
		t.Fatalf("offline = %#v, want an empty non-nil list", got)
	}
	t.Setenv("PI_OFFLINE", "")

	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "git:github.com/u/repo@main"}}); err != nil {
		t.Fatal(err)
	}
	if got := pm.CheckForAvailableUpdates(); len(got) != 0 {
		t.Fatalf("a pinned git ref reported %+v", got)
	}
	t.Chdir(checkout) // a stale checkout in the working directory must not be mistaken for the missing package
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "git:github.com/u/not-installed"}}); err != nil {
		t.Fatal(err)
	}
	if got := pm.CheckForAvailableUpdates(); len(got) != 0 {
		t.Fatalf("a package that is not installed reported %+v", got)
	}
}

func fakeNpm(t *testing.T, latest string) (command, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls.log")
	command = filepath.Join(dir, "npm")
	script := "#!/bin/sh\necho \"$@\" >> '" + log + "'\ncase \"$*\" in *\" view \"*|view*) echo '\"" + latest + "\"';; esac\nexit 0\n"
	if err := os.WriteFile(command, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return command, log
}

// package-manager.ts remove / checkForAvailableUpdates for npm sources: an installed managed npm package is uninstalled with the configured npm command and `--prefix`, and an unpinned installed package is reported when the registry's version is newer while an exact pinned version is never queried.
// Pi: packages/coding-agent/src/core/package-manager.ts remove, uninstallManagedNPM, checkForAvailableUpdates (:1186-1253).
func TestPackageManagerNpmUninstallAndUpdateCheckUseTheConfiguredCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the npm command")
	}
	cwd, agentDir, sm, pm := resolveFixtureWithHome(t)
	command, log := fakeNpm(t, "2.0.0")
	if err := sm.SetNpmCommand([]string{command}); err != nil {
		t.Fatal(err)
	}
	ref, err := ParseNpmInstallRef("npm:left-pad")
	if err != nil {
		t.Fatal(err)
	}
	root := NpmInstallRoot(cwd, agentDir, ref, false)
	installed := writeResolveFile(t, filepath.Join(root, "node_modules", "left-pad", "package.json"), `{"name":"left-pad","version":"1.0.0"}`)
	if got := pm.GetInstalledPath("npm:left-pad", "user"); got != filepath.Dir(installed) {
		t.Fatalf("installed path = %q, want %q", got, filepath.Dir(installed))
	}

	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:left-pad"}}); err != nil {
		t.Fatal(err)
	}
	updates := pm.CheckForAvailableUpdates()
	if len(updates) != 1 || updates[0].Source != "npm:left-pad" || updates[0].Type != "npm" || updates[0].Scope != "user" || updates[0].DisplayName != "left-pad" {
		t.Fatalf("updates = %+v, want one npm update for left-pad", updates)
	}
	t.Setenv("PI_OFFLINE", "1")
	if got := pm.CheckForAvailableUpdates(); got == nil || len(got) != 0 {
		t.Fatalf("offline = %#v, want an empty non-nil list without registry lookups", got)
	}
	t.Setenv("PI_OFFLINE", "")
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:left-pad@1.0.0"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(log)
	if got := pm.CheckForAvailableUpdates(); len(got) != 0 {
		t.Fatalf("an exact pinned npm version reported %+v", got)
	}
	if after, _ := os.ReadFile(log); !bytes.Equal(after, before) {
		t.Fatalf("an exact pinned npm version queried the registry: %q", after)
	}

	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if err := pm.Remove("npm:left-pad", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("removing an npm package from a scope with no install root ran npm: %v", err)
	}
	if err := pm.Remove("npm:left-pad", false); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "uninstall left-pad --prefix "+root) || !strings.Contains(string(calls), "--legacy-peer-deps") {
		t.Fatalf("npm calls = %q, want an uninstall of left-pad with --prefix %s", calls, root)
	}
}

// pig divergence (D110): Git sources select a repository subdirectory with #subdirectory=, so two configured sources can share one checkout. PackageManager.Remove of one keeps the
// checkout while the other is configured in the same scope, and removes it (and its marker) once none is. Pi's removeGit (package-manager.ts:2033) always deletes it, which cannot
// strand another source because Pi has no subdirectory form.
func TestPackageManagerRemoveKeepsAGitCheckoutAnotherConfiguredSourceShares(t *testing.T) {
	cwd, agentDir, sm, pm := resolveFixtureWithHome(t)
	removed := "git:https://github.com/acme/tools#subdirectory=plugins%2Freview"
	sibling := "git:https://github.com/acme/tools#subdirectory=plugins%2Ftrace"
	checkout, err := GitCheckoutPath(cwd, agentDir, removed, false)
	if err != nil {
		t.Fatal(err)
	}
	writeResolveFile(t, filepath.Join(checkout, "package.json"), "{}")
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: sibling}}); err != nil {
		t.Fatal(err)
	}
	if err := (*PackageManager).Remove(pm, removed, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(checkout); err != nil {
		t.Fatalf("the checkout another configured source shares was removed: %v", err)
	}
	// A source of another repository, or of the other scope, shares nothing with this user-scope checkout.
	other := "git:https://github.com/acme/other#subdirectory=plugins%2Freview"
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: other}}); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetProjectPackages([]codingagent.PackageSource{{Source: sibling}}); err != nil {
		t.Fatal(err)
	}
	if err := pm.Remove(removed, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(checkout); !os.IsNotExist(err) {
		t.Fatalf("the checkout survived Remove although only another repository and another scope are configured: %v", err)
	}

	// The same holds in the project scope, where only a project-scope source shares the checkout.
	projectCheckout, err := GitCheckoutPath(cwd, agentDir, removed, true)
	if err != nil {
		t.Fatal(err)
	}
	writeResolveFile(t, filepath.Join(projectCheckout, "package.json"), "{}")
	if err := pm.Remove(removed, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(projectCheckout); err != nil {
		t.Fatalf("the project checkout a project-scope source shares was removed: %v", err)
	}
	if err := sm.SetProjectPackages(nil); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: sibling}}); err != nil {
		t.Fatal(err)
	}
	if err := pm.Remove(removed, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(projectCheckout); !os.IsNotExist(err) {
		t.Fatalf("the project checkout survived Remove although only a user-scope source is configured: %v", err)
	}
}
